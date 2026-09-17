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
		return errors.New("MaxConnections is less than 0")
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
func (c *Crawler) Run(ctx context.Context, startUrl *url.URL) (<-chan PageResult, error) {
	if startUrl == nil {
		return nil, errors.New("startUrl is nil")
	}

	if !isHttp(startUrl) {
		return nil, errors.New("startUrl does not support HTTP")
	}

	if c.settings.TargetHost != "" && startUrl.Host != c.settings.TargetHost {
		return nil, errors.New("targetHost does not match startUrl")
	}

	return c.crawl(ctx, startUrl), nil
}

type linkEntry struct {
	url   string
	depth int
}

type workResult struct {
	link           linkEntry
	page           *fetchedPage
	extractedLinks []string
	err            error
}

type crawlState struct {
	queue []linkEntry
	seen  map[string]struct{}
	// Unlike seen, processed contains only URLs whose page was fetched.
	// It includes both the requested address and the final redirect address.
	processed map[string]struct{}
}

func (s *crawlState) registerPage(originalURL, finalURL string) bool {
	alreadyProcessed := s.isProcessed(finalURL)

	s.processed[originalURL] = struct{}{}
	s.processed[finalURL] = struct{}{}
	s.seen[finalURL] = struct{}{}

	return alreadyProcessed
}

// enqueueLinks добавляет в очередь ещё не встречавшиеся ссылки с переданной глубиной,
// применяя лимит после исключения уже встреченных. Здесь же обновляет seen.
// TODO: Надо определиться, что значит link и что url. Здесь оперируем строками, не linkEntry
func (s *crawlState) enqueueLinks(links []string, depth, limit int) {
	added := 0

	for _, link := range links {
		if s.hasSeen(link) {
			continue
		}

		if limit > 0 && added >= limit {
			break
		}

		s.seen[link] = struct{}{}

		s.pushLink(linkEntry{
			url:   link,
			depth: depth,
		})

		added++
	}
}

// TODO: точно норм, что работаем с linkEntry?
func (s *crawlState) popLink() linkEntry {
	link := s.queue[0]
	clear(s.queue[:1])
	s.queue = s.queue[1:]
	return link
}

func (s *crawlState) pushLink(link linkEntry) {
	s.queue = append(s.queue, link)
}

func (s *crawlState) isProcessed(url string) bool {
	_, ok := s.processed[url]
	return ok
}

func (s *crawlState) hasSeen(url string) bool {
	_, ok := s.seen[url]
	return ok
}

// Bounded goroutines
// [crawl]: owns q, seen, processed
// spawns at most MaxConnections goroutines for fetch and extract
// [spawned]: fetch n parse
func (c *Crawler) crawl(ctx context.Context, startUrl *url.URL) <-chan PageResult {
	out := make(chan PageResult)

	go func() {
		defer close(out)

		cleanUrl := cleanUpUrl(*startUrl)

		state := crawlState{
			queue:     []linkEntry{{url: cleanUrl.String(), depth: 0}},
			seen:      map[string]struct{}{cleanUrl.String(): {}},
			processed: map[string]struct{}{},
		}

		inFlight := 0
		done := make(chan workResult)

		for {
			if ctx.Err() != nil {
				return
			}

			for len(state.queue) > 0 && inFlight < c.settings.MaxConcurrency {
				link := state.popLink()

				if state.isProcessed(link.url) {
					continue
				}

				inFlight++

				go func() {
					result := c.processLink(ctx, link)
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
					alreadyProcessed := state.registerPage(result.link.url, result.page.url.String())

					if alreadyProcessed {
						continue
					}

					if result.err == nil {
						state.enqueueLinks(result.extractedLinks, result.link.depth+1, c.settings.MaxLinksPerPage)
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

func (c *Crawler) processLink(ctx context.Context, link linkEntry) workResult {
	page, err := c.fetch(ctx, link.url)
	if err != nil {
		return workResult{
			link: link,
			err:  &CrawlError{Stage: StageFetch, Err: err},
		}
	}

	finalURL := page.url.String()

	if link.depth < c.settings.MaxDepth {
		var err error
		extractedLinks, err := c.pageLinks(page.body, finalURL)
		if err != nil {
			return workResult{
				link: link,
				page: &page,
				err:  &CrawlError{Stage: StageExtractLinks, Err: err},
			}
		}

		return workResult{
			link:           link,
			page:           &page,
			extractedLinks: extractedLinks,
		}
	}

	return workResult{
		link: link,
		page: &page,
	}
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
