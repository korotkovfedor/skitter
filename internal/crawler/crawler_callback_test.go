package crawler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunCancelsActiveRequest(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
	}))
	defer server.Close()

	crawler, startURL := newTestCrawler(t, server, Settings{MaxDepth: 0})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results, err := crawler.Run(ctx, startURL)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	select {
	case <-requestStarted:
		cancel()
	case <-time.After(asyncTestTimeout):
		t.Fatal("request did not start")
	}

	timer := time.NewTimer(asyncTestTimeout)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-results:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("result channel did not close after context cancellation")
		}
	}
}

func TestRunReportsResultsThroughChannel(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `<a href="/child">child</a>`)
	}))
	defer server.Close()

	crawler, startURL := newTestCrawler(t, server, Settings{MaxDepth: 1})
	results := collectResults(t, crawler, startURL)
	if len(results) != 2 {
		t.Fatalf("result count = %d, want 2", len(results))
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestRunFetchesPagesConcurrently(t *testing.T) {
	childStarted := make(chan struct{}, 3)
	releaseChildren := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = io.WriteString(w, `<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a>`)
			return
		}
		childStarted <- struct{}{}
		select {
		case <-releaseChildren:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "leaf")
	}))
	defer server.Close()

	crawler, startURL := newTestCrawler(t, server, Settings{
		MaxDepth:       1,
		MaxConcurrency: 2,
	})
	ctx, cancel := context.WithTimeout(context.Background(), asyncTestTimeout)
	defer cancel()

	resultsCh, err := crawler.Run(ctx, startURL)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	select {
	case root := <-resultsCh:
		if pathFromURL(t, root.FinalURL) != "/" {
			t.Fatalf("first result path = %q, want root", root.FinalURL)
		}
	case <-ctx.Done():
		t.Fatalf("root result was not delivered: %v", ctx.Err())
	}

	for range 2 {
		select {
		case <-childStarted:
		case <-ctx.Done():
			t.Fatalf("children were not fetched concurrently: %v", ctx.Err())
		}
	}
	select {
	case <-childStarted:
		t.Fatal("crawler exceeded MaxConcurrency")
	default:
	}

	close(releaseChildren)

	resultCount := 1
	for {
		select {
		case _, ok := <-resultsCh:
			if !ok {
				if resultCount != 4 {
					t.Fatalf("result count = %d, want 4", resultCount)
				}
				return
			}
			resultCount++
		case <-ctx.Done():
			t.Fatalf("result channel did not close: %v", ctx.Err())
		}
	}
}

func TestRunReportsChildFetchErrorAndContinues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = io.WriteString(w, `<a href="/unavailable">unavailable</a>`)
			return
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	crawler, startURL := newTestCrawler(t, server, Settings{MaxDepth: 1})
	results := collectResults(t, crawler, startURL)
	if len(results) != 2 {
		t.Fatalf("result count = %d, want 2", len(results))
	}

	var failed PageResult
	found := false
	for _, result := range results {
		if pathFromURL(t, result.OriginalURL) == "/unavailable" {
			failed = result
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("results = %+v, want failed /unavailable result", results)
	}
	if pathFromURL(t, failed.OriginalURL) != "/unavailable" || failed.FinalURL != "" {
		t.Fatalf("failed result URLs = original %q, final %q", failed.OriginalURL, failed.FinalURL)
	}
	if failed.Depth != 1 || failed.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("failed result depth/status = %d/%d", failed.Depth, failed.StatusCode)
	}
	if _, ok := errors.AsType[*HTTPError](failed.Err); !ok {
		t.Fatalf("failed result error = %T, want *HTTPError", failed.Err)
	}
}
