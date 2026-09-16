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

	var results []PageResult
	done := make(chan error, 1)
	go func() {
		done <- crawler.Run(ctx, startURL, func(page PageResult) error {
			results = append(results, page)
			return nil
		})
	}()

	select {
	case <-requestStarted:
		cancel()
	case <-time.After(asyncTestTimeout):
		t.Fatal("request did not start")
	}

	var err error
	select {
	case err = <-done:
	case <-time.After(asyncTestTimeout):
		t.Fatal("Run() did not return after context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if len(results) != 1 || !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("callback results = %+v, want one context cancellation", results)
	}
}

func TestRunReturnsCallbackErrorUnchangedAndStops(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `<a href="/child">child</a>`)
	}))
	defer server.Close()

	crawler, startURL := newTestCrawler(t, server, Settings{MaxDepth: 1})
	callbackErr := errors.New("stop callback")
	callbackCalls := 0
	err := crawler.Run(context.Background(), startURL, func(PageResult) error {
		callbackCalls++
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("Run() error = %v, want exact callback error %v", err, callbackErr)
	}
	if callbackCalls != 1 {
		t.Fatalf("callback calls = %d, want 1", callbackCalls)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestRunInvokesCallbackSequentially(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = io.WriteString(w, `<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a>`)
			return
		}
		_, _ = io.WriteString(w, "leaf")
	}))
	defer server.Close()

	crawler, startURL := newTestCrawler(t, server, Settings{MaxDepth: 1})
	var active atomic.Int32
	var overlap atomic.Bool
	err := crawler.Run(context.Background(), startURL, func(PageResult) error {
		if active.Add(1) != 1 {
			overlap.Store(true)
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if overlap.Load() {
		t.Fatal("callbacks overlapped")
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
	var results []PageResult
	err := crawler.Run(context.Background(), startURL, func(page PageResult) error {
		results = append(results, page)
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil after child fetch error", err)
	}
	if len(results) != 2 {
		t.Fatalf("callback count = %d, want 2", len(results))
	}

	failed := results[1]
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
