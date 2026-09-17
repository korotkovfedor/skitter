package crawler

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

type Settings struct {
	// Number of max concurrent fetch'n'parse jobs.
	// Zero is interpreted as single working fetch goroutine.
	MaxConcurrency int

	// Zero is interpreted as no limit
	MaxLinksPerPage int

	// MaxResponseBytes is the largest response body accepted from one request.
	// Zero is interpreted as no limit.
	MaxResponseBytes int64

	// Zero is interpreted literally
	MaxDepth int

	// TargetHost must match URL.Host, including an optional port.
	// An empty value allows any host.
	TargetHost string

	// MaxRetries is the number of additional attempts after the first request.
	// Zero performs one request without retries.
	MaxRetries int

	// RetryLatency is the delay between retry attempts.
	// Zero retries immediately.
	// Ignored if response contains 'Retry-After' header.
	RetryLatency time.Duration
}

func (s Settings) HasLinkLimit() bool {
	return s.MaxLinksPerPage > 0
}

func (s Settings) validate() error {
	if s.MaxDepth < 0 {
		return errors.New("MaxDepth is less than 0")
	}

	if s.MaxConcurrency < 0 {
		return errors.New("MaxConcurrency is less than 0")
	}

	if s.MaxLinksPerPage < 0 {
		return errors.New("MaxLinksPerPage is less than 0")
	}

	if s.MaxResponseBytes < 0 {
		return errors.New("MaxResponseBytes is less than 0")
	}

	if s.MaxRetries < 0 {
		return errors.New("MaxRetries is less than 0")
	}

	if s.RetryLatency < 0 {
		return errors.New("RetryLatency is less than 0")
	}

	return nil
}

type Crawler struct {
	client   *http.Client
	settings Settings
}

func New(client *http.Client, settings Settings) (*Crawler, error) {
	if client == nil {
		return nil, errors.New("client is nil")
	}
	if err := settings.validate(); err != nil {
		return nil, err
	}

	if settings.MaxConcurrency == 0 {
		settings.MaxConcurrency = 1
	}

	return &Crawler{
		client:   newCrawlerClient(client, settings),
		settings: settings,
	}, nil
}

// Run starts crawling from startURL.
//
// If the caller stops consuming results before the returned channel is closed,
// it must cancel ctx.
func (c *Crawler) Run(ctx context.Context, startURL *url.URL) (<-chan PageResult, error) {
	if startURL == nil {
		return nil, errors.New("startURL is nil")
	}

	if !isHTTP(startURL) {
		return nil, errors.New("startURL does not support HTTP")
	}

	if c.settings.TargetHost != "" && startURL.Host != c.settings.TargetHost {
		return nil, errors.New("targetHost does not match startURL")
	}

	return c.crawl(ctx, startURL), nil
}

type crawlJob struct {
	url   string
	depth int
}

type workResult struct {
	job           crawlJob
	page          *fetchedPage
	extractedURLs []string
	err           error
}

type crawlState struct {
	queue []crawlJob
	seen  map[string]struct{}
	// Unlike seen, processed contains only URLs whose page was fetched.
	// It includes both the requested address and the final redirect address.
	processed map[string]struct{}
}

// registerPage records both addresses and reports whether finalURL was already processed.
func (s *crawlState) registerPage(originalURL, finalURL string) bool {
	alreadyProcessed := s.isProcessed(finalURL)

	s.processed[originalURL] = struct{}{}
	s.processed[finalURL] = struct{}{}
	s.seen[finalURL] = struct{}{}

	return alreadyProcessed
}

// enqueueURLs добавляет в очередь ещё не встречавшиеся ссылки с переданной глубиной,
// применяя лимит после исключения уже встреченных. Здесь же обновляет seen.
func (s *crawlState) enqueueURLs(urls []string, depth, limit int) {
	added := 0

	for _, targetURL := range urls {
		if s.hasSeen(targetURL) {
			continue
		}

		if limit > 0 && added >= limit {
			break
		}

		s.seen[targetURL] = struct{}{}

		s.pushJob(crawlJob{
			url:   targetURL,
			depth: depth,
		})

		added++
	}
}

func (s *crawlState) popJob() (crawlJob, bool) {
	if len(s.queue) == 0 {
		return crawlJob{}, false
	}

	job := s.queue[0]
	clear(s.queue[:1])
	s.queue = s.queue[1:]

	return job, true
}

func (s *crawlState) pushJob(job crawlJob) {
	s.queue = append(s.queue, job)
}

func (s *crawlState) isProcessed(targetURL string) bool {
	_, ok := s.processed[targetURL]
	return ok
}

func (s *crawlState) hasSeen(targetURL string) bool {
	_, ok := s.seen[targetURL]
	return ok
}

// crawl keeps the queue, seen, and processed in one coordinator goroutine.
// At most MaxConcurrency workers fetch pages and extract URLs concurrently.
func (c *Crawler) crawl(ctx context.Context, startURL *url.URL) <-chan PageResult {
	out := make(chan PageResult)

	go func() {
		defer close(out)

		cleanURL := cleanUpURL(*startURL)

		state := crawlState{
			queue:     []crawlJob{{url: cleanURL.String(), depth: 0}},
			seen:      map[string]struct{}{cleanURL.String(): {}},
			processed: map[string]struct{}{},
		}

		inFlight := 0
		done := make(chan workResult)

		for {
			if ctx.Err() != nil {
				return
			}

			for inFlight < c.settings.MaxConcurrency {
				job, ok := state.popJob()
				if !ok {
					break
				}

				if state.isProcessed(job.url) {
					continue
				}

				inFlight++

				go func() {
					result := c.processJob(ctx, job)
					select {
					case done <- result:
					case <-ctx.Done():
						return
					}
				}()
			}

			if len(state.queue) == 0 && inFlight == 0 {
				return
			}

			select {
			case <-ctx.Done():
				return
			case result := <-done:
				inFlight--

				if result.page != nil {
					alreadyProcessed := state.registerPage(result.job.url, result.page.url.String())

					if alreadyProcessed {
						continue
					}

					if result.err == nil {
						state.enqueueURLs(result.extractedURLs, result.job.depth+1, c.settings.MaxLinksPerPage)
					}
				}

				pageResult := result.pageResult()

				if !sendResult(ctx, out, pageResult) {
					return
				}
			}
		}
	}()

	return out
}

func (c *Crawler) processJob(ctx context.Context, job crawlJob) workResult {
	page, err := c.fetch(ctx, job.url)
	if err != nil {
		return workResult{
			job: job,
			err: &CrawlError{Stage: StageFetch, Err: err},
		}
	}

	result := workResult{
		job:  job,
		page: &page,
	}

	if job.depth < c.settings.MaxDepth {
		urls, err := c.extractPageURLs(page.body, page.url.String())
		if err != nil {
			result.err = &CrawlError{Stage: StageExtractLinks, Err: err}

			return result
		}

		result.extractedURLs = urls
	}

	return result
}

func sendResult(
	ctx context.Context,
	out chan<- PageResult,
	result PageResult,
) bool {
	select {
	case out <- result:
		return true
	case <-ctx.Done():
		return false
	}
}
