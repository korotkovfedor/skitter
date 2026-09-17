package crawler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ErrResponseTooLarge reports that a response body exceeds MaxResponseBytes.
// Use errors.Is on [PageResult.Err] to detect it through [CrawlError] and
// [HTTPError]. The result retains the HTTP status but has no Page or partial body.
var ErrResponseTooLarge = errors.New("response body exceeds configured size limit")

type fetchedPage struct {
	body       string
	url        url.URL
	statusCode int
}

func (c *Crawler) fetch(ctx context.Context, targetURL string) (fetchedPage, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return fetchedPage{}, fmt.Errorf("create request: %w", err)
	}

	client := *c.client
	checkRedirect := client.CheckRedirect
	finalURL := req.URL
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if err := checkRedirect(next, via); err != nil {
			return err
		}

		finalURL = next.URL
		return nil
	}

	req.Header.Set("User-Agent", "SkitterBot/0.1")
	resp, err := doWithRetry(ctx, &client, req, c.settings.MaxRetries, c.settings.RetryLatency)
	if err != nil {
		// A rejected redirect can return both an error and a response.
		// Client.Do has already closed that response's body.
		if resp != nil {
			return fetchedPage{}, &HTTPError{
				StatusCode: resp.StatusCode,
				Err:        fmt.Errorf("fetch request: %w", err),
			}
		}
		return fetchedPage{}, fmt.Errorf("fetch request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fetchedPage{}, &HTTPError{StatusCode: resp.StatusCode}
	}

	body, err := readResponseBody(resp.Body, c.settings.MaxResponseBytes)
	if err != nil {
		return fetchedPage{}, &HTTPError{
			StatusCode: resp.StatusCode,
			Err:        fmt.Errorf("read body: %w", err),
		}
	}

	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL
	}
	return fetchedPage{
		body:       string(body),
		url:        cleanUpURL(*finalURL),
		statusCode: resp.StatusCode,
	}, nil
}

func doWithRetry(
	ctx context.Context,
	client *http.Client,
	req *http.Request,
	retries int,
	retryLatency time.Duration,
) (*http.Response, error) {
	for attempt := 0; attempt <= retries; attempt++ {
		resp, err := client.Do(req)

		if err != nil {
			// Per net/http's contract, a response with an error is returned only
			// when CheckRedirect rejects the next request.
			// Preserve its status and do not retry a policy decision.
			if resp != nil {
				return resp, err
			}
			if attempt == retries {
				return nil, fmt.Errorf("request failed after %d attempts: %w", retries+1, err)
			}
			if err := waitForRetry(ctx, retryLatency); err != nil {
				return nil, err
			}
			continue
		}

		if !shouldRetry(resp.StatusCode) || attempt == retries {
			return resp, nil
		}

		delay := retryDelay(resp, retryLatency)
		resp.Body.Close()

		if err := waitForRetry(ctx, delay); err != nil {
			return nil, err
		}
	}

	return nil, errors.New("unreachable retry state")
}

func retryDelay(resp *http.Response, fallback time.Duration) time.Duration {
	if resp == nil {
		return fallback
	}

	value := resp.Header.Get("Retry-After")
	if value == "" {
		return fallback
	}

	// Retry-After: <delay-seconds>
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return fallback
		}

		return time.Duration(seconds) * time.Second
	}

	// Retry-After: <http-date>
	if retryAt, err := http.ParseTime(value); err == nil {
		delay := time.Until(retryAt)
		if delay > 0 {
			return delay
		}

		return 0
	}

	return fallback
}

func waitForRetry(ctx context.Context, retryLatency time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(retryLatency):
		return nil
	}
}

func shouldRetry(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func readResponseBody(body io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes == 0 {
		return io.ReadAll(body)
	}

	limitedBody := &io.LimitedReader{R: body, N: maxBytes}
	contents, err := io.ReadAll(limitedBody)
	if err != nil {
		return nil, err
	}
	if limitedBody.N > 0 {
		return contents, nil
	}

	var extraByte [1]byte
	n, err := body.Read(extraByte[:])
	if n > 0 {
		return nil, fmt.Errorf("%w: limit is %d bytes", ErrResponseTooLarge, maxBytes)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	return contents, nil
}
