package crawler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

var (
	errAlreadyProcessed = errors.New("redirect target already processed")

	// ErrResponseTooLarge reports that a response body exceeds MaxResponseBytes.
	ErrResponseTooLarge = errors.New("response body exceeds configured size limit")
)

type fetchedPage struct {
	body       string
	url        url.URL
	statusCode int
}

func (c *Crawler) fetch(ctx context.Context, targetURL string, processed map[string]struct{}) (fetchedPage, error) {
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
		cleanURL := cleanUpUrl(*next.URL)
		if _, ok := processed[cleanURL.String()]; ok {
			return errAlreadyProcessed
		}
		finalURL = next.URL
		return nil
	}

	req.Header.Set("User-Agent", "SkitterBot/0.1 korotkoffst@gmail.com")
	resp, err := client.Do(req)
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
		url:        cleanUpUrl(*finalURL),
		statusCode: resp.StatusCode,
	}, nil
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
