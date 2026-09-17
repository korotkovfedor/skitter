package crawler

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

const defaultUserAgent = "SkitterBot/0.1"

// Settings controls the requests and traversal performed by each [Crawler.Run].
// New rejects negative numeric values and copies the settings.
// The zero value fetches only the starting page, with one worker, no retries,
// no host restriction, and no response body size limit.
type Settings struct {
	// MaxConcurrency limits concurrent page-processing jobs within one run.
	// Each job fetches a page and, below MaxDepth, extracts its outgoing URLs.
	// Retry waits occupy a job slot. Zero means one worker.
	// This does not limit the queue size or concurrency across separate runs.
	MaxConcurrency int

	// MaxLinksPerPage limits new URLs enqueued from each page.
	// URLs are considered in document order after filtering and deduplication
	// against all URLs already seen by this run. Zero means no limit.
	// This does not limit HTML parsing, total pages, or total memory use.
	MaxLinksPerPage int

	// MaxResponseBytes limits bytes read from a successful HTTP response body.
	// A body exactly at the limit is accepted; a larger body produces
	// [ErrResponseTooLarge] and no Page. Zero means no limit.
	// The limit applies to bytes exposed by the client's response body, which
	// may already be decompressed by its transport.
	MaxResponseBytes int64

	// MaxDepth is the greatest traversal depth to fetch. The starting page
	// has depth zero; redirects do not increase depth. Pages at MaxDepth are
	// downloaded but their outgoing URLs are not extracted.
	// Zero therefore fetches only the starting page.
	// Depth follows the first discovered route, not necessarily the shortest
	// route when jobs run concurrently; see the package traversal documentation.
	MaxDepth int

	// TargetHost restricts the starting URL, extracted URLs, and redirect targets
	// to an exact URL.Host match, including case and an optional port.
	// It does not include a scheme and does not implicitly allow subdomains.
	// An empty value allows any host. HTTP and HTTPS are both allowed.
	TargetHost string

	// UserAgent is the User-Agent header set on page requests and retries.
	// An empty value uses "SkitterBot/0.1". The HTTP client carries the header
	// through redirects unless a custom redirect hook or transport changes it.
	UserAgent string

	// MaxRetries is the maximum number of additional HTTP request attempts.
	// Zero disables retries. Each attempt may follow redirects.
	// Retries apply to Client.Do errors without a response and HTTP statuses
	// 429, 502, 503, and 504. Rejected redirects with a response, body-reading
	// failures, and URL extraction failures are not retried.
	MaxRetries int

	// RetryLatency is the fallback delay between retry attempts.
	// A valid Retry-After header on a retried response overrides it; an absent
	// or invalid header uses this delay. A past HTTP date means no delay.
	// Zero retries immediately when no header overrides it. Waiting is canceled
	// with the run's context. This is not a delay between unrelated page requests.
	RetryLatency time.Duration
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

// Crawler fetches HTTP and HTTPS pages and follows their anchor links.
// Create a Crawler with [New]; its zero value is not ready for use.
// Runs have independent queues, deduplication state, and concurrency limits.
// A Crawler can run concurrently if the supplied client's shared transport,
// cookie jar, and redirect callback support concurrent use.
type Crawler struct {
	client   *http.Client
	settings Settings
}

// New creates a crawler, or returns an error if client is nil or settings
// contains a negative numeric value.
//
// New copies settings and the http.Client value without changing the caller's
// client. The copy shares the original transport, cookie jar, and callback
// dependencies. Configure them before use and avoid concurrent mutation.
// The client's timeout is retained; New does not add a request timeout.
//
// The crawler installs a redirect policy that rejects a next request when its
// redirect history contains ten requests. Below that limit, the caller's
// CheckRedirect callback runs first; if it returns nil, the crawler checks
// the next URL's scheme and TargetHost, including any changes made by the hook.
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
	if settings.UserAgent == "" {
		settings.UserAgent = defaultUserAgent
	}

	return &Crawler{
		client:   newCrawlerClient(client, settings),
		settings: settings,
	}, nil
}

// Run validates startURL and starts an asynchronous crawl.
// It returns a nil channel and an error if startURL is nil, does not have an
// http or https scheme and a hostname, or does not match a nonempty TargetHost.
// ctx must be non-nil. Do not modify startURL until the result channel closes.
//
// After validation, Run returns a receive-only channel and nil error. Failures
// of individual pages, including the starting page, are delivered through
// [PageResult.Err] and do not stop other queued work. URL extraction failures
// preserve the downloaded Page. Results have no guaranteed ordering when
// MaxConcurrency is greater than one, and duplicate final URLs are suppressed.
//
// The crawler closes the channel when work is exhausted or ctx is canceled.
// Cancellation interrupts HTTP requests and retry waits; pending results may
// be dropped, and no terminal cancellation result is guaranteed. Channel
// closure after cancellation does not wait for every worker to return.
// Inspect ctx.Err() after consuming results to detect context cancellation;
// closure alone does not mean every page succeeded.
//
// Result delivery applies backpressure: a slow consumer can pause scheduling.
// If the caller stops consuming before the channel closes, it must cancel ctx.
// There is no global page count limit or built-in deadline for the whole run.
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
	queue     []crawlJob
	seen      map[string]struct{}
	processed map[string]struct{}
}

func (s *crawlState) registerPage(originalURL, finalURL string) bool {
	alreadyProcessed := s.isProcessed(finalURL)

	s.processed[originalURL] = struct{}{}
	s.processed[finalURL] = struct{}{}
	s.seen[finalURL] = struct{}{}

	return alreadyProcessed
}

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
