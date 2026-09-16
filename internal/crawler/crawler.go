package crawler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"
)

type Settings struct {
	// Zero is interpreted as single connection
	MaxConnections int

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

func (s Settings) MustLimitLinks() bool {
	return s.MaxLinksPerPage > 0
}

func (s Settings) validate() error {
	if s.MaxDepth < 0 {
		return errors.New("MaxDepth is less than 0")
	}

	if s.MaxConnections < 0 {
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

	if settings.MaxConnections == 0 {
		settings.MaxConnections = 1
	}

	return &Crawler{
		client:   newCrawlerClient(client, settings),
		settings: settings,
	}, nil
}

func (c *Crawler) Run(ctx context.Context, startUrl *url.URL, onResult func(page PageResult) error) error {
	if startUrl == nil {
		return errors.New("startUrl is nil")
	}

	if !isHttp(startUrl) {
		return errors.New("startUrl does not support HTTP")
	}

	if c.settings.TargetHost != "" && startUrl.Host != c.settings.TargetHost {
		return errors.New("targetHost does not match startUrl")
	}

	if onResult == nil {
		return errors.New("onResult is nil")
	}

	if err := c.crawl(ctx, startUrl, onResult); err != nil {
		return err
	}

	return nil
}

type linkEntry struct {
	url   string
	depth int
}

func (c *Crawler) crawl(ctx context.Context, startUrl *url.URL, onResult func(page PageResult) error) error {
	cleanUrl := cleanUpUrl(*startUrl)

	links := []linkEntry{{url: cleanUrl.String(), depth: 0}}
	seen := map[string]struct{}{cleanUrl.String(): {}}
	// Unlike seen, processed contains only URLs whose page was fetched.
	// It includes both the requested address and the final redirect address.
	processed := map[string]struct{}{}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if len(links) == 0 {
			return nil
		}

		link := links[0]
		clear(links[:1])
		links = links[1:]

		if _, ok := processed[link.url]; ok {
			continue
		}

		page, err := c.fetch(ctx, link.url, processed)
		if err != nil {
			if errors.Is(err, errAlreadyProcessed) {
				continue
			}

			result := PageResult{
				OriginalURL: link.url,
				Depth:       link.depth,
				Err:         err,
			}
			if httpErr, ok := errors.AsType[*HTTPError](err); ok {
				result.StatusCode = httpErr.StatusCode
			}
			clientErr := onResult(result)

			if clientErr != nil {
				return clientErr
			}
			if link.depth == 0 {
				return fmt.Errorf("start page fetch: %w", err)
			}
			log.Printf("skip url <%s> with error: %v", link.url, err)
			continue
		}

		finalURL := page.url.String()
		_, alreadyProcessed := processed[finalURL]
		seen[finalURL] = struct{}{}
		processed[link.url] = struct{}{}
		processed[finalURL] = struct{}{}
		if alreadyProcessed {
			continue
		}

		clientErr := onResult(PageResult{
			OriginalURL: link.url,
			FinalURL:    finalURL,
			Depth:       link.depth,
			StatusCode:  page.statusCode,
			HTML:        page.body,
			Err:         nil,
		})
		if clientErr != nil {
			return clientErr
		}

		if link.depth >= c.settings.MaxDepth {
			continue
		}

		extractedLinks, err := c.pageLinks(page.body, finalURL, seen)
		if err != nil {
			return err
		}

		for _, extractedLink := range extractedLinks {
			seen[extractedLink] = struct{}{}
			links = append(links, linkEntry{
				url:   extractedLink,
				depth: link.depth + 1,
			})
		}
	}
}
