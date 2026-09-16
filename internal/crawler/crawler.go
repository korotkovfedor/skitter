package crawler

import (
	"context"
	"errors"
	"fmt"
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

// Bounded goroutines
// [crawl]: owns q, seen, processed
// spawns at most MaxConnections goroutines for fetch and extract
// [spawned]: fetch n parse
func (c *Crawler) crawl(ctx context.Context, startUrl *url.URL) <-chan PageResult {
	out := make(chan PageResult)

	go func() {
		defer close(out)

		cleanUrl := cleanUpUrl(*startUrl)

		links := []linkEntry{{url: cleanUrl.String(), depth: 0}}
		seen := map[string]struct{}{cleanUrl.String(): {}}
		// Unlike seen, processed contains only URLs whose page was fetched.
		// It includes both the requested address and the final redirect address.
		processed := map[string]struct{}{}

		inFlight := 0
		done := make(chan workResult)

		for {
			if ctx.Err() != nil {
				return
			}

			for len(links) > 0 && inFlight < c.settings.MaxConcurrency {
				link := links[0]
				clear(links[:1])
				links = links[1:]

				if _, ok := processed[link.url]; ok {
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

			if len(links) == 0 && inFlight == 0 {
				return
			}

			select {
			case <-ctx.Done():
				return
			case result := <-done:
				inFlight--

				link := result.link
				extractedLinks := result.extractedLinks
				originalURL := link.url

				if result.page == nil {
					pageResult := PageResult{
						OriginalURL: originalURL,
						Depth:       result.link.depth,
						Err:         result.err,
					}

					if httpErr, ok := errors.AsType[*HTTPError](result.err); ok {
						pageResult.StatusCode = httpErr.StatusCode
					}

					if !sendResult(ctx, out, pageResult) {
						return
					}

					continue
				}

				page := result.page
				finalURL := page.url.String()

				_, alreadyProcessed := processed[finalURL]
				processed[originalURL] = struct{}{}
				processed[finalURL] = struct{}{}
				seen[finalURL] = struct{}{}

				if alreadyProcessed {
					continue
				}

				if result.err != nil {
					if !sendResult(ctx, out, PageResult{
						OriginalURL: result.link.url,
						FinalURL:    finalURL,
						Depth:       result.link.depth,
						StatusCode:  page.statusCode,
						HTML:        page.body,
						Err:         result.err,
					}) {
						return
					}

					continue
				}

				added := 0

				for _, extractedLink := range extractedLinks {
					if _, ok := seen[extractedLink]; ok {
						continue
					}

					if c.settings.HasLinkLimit() &&
						added >= c.settings.MaxLinksPerPage {
						break
					}

					seen[extractedLink] = struct{}{}

					links = append(links, linkEntry{
						url:   extractedLink,
						depth: link.depth + 1,
					})

					added++
				}

				if !sendResult(ctx, out, PageResult{
					OriginalURL: link.url,
					FinalURL:    finalURL,
					Depth:       link.depth,
					StatusCode:  page.statusCode,
					HTML:        page.body,
				}) {
					return
				}
			}
		}
	}()

	return out
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

func (c *Crawler) processLink(ctx context.Context, link linkEntry) workResult {
	page, err := c.fetch(ctx, link.url)
	if err != nil {
		return workResult{
			link: link,
			err:  fmt.Errorf("fetch: %w", err),
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
				err:  fmt.Errorf("extract links: %w", err),
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
