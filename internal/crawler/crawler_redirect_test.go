package crawler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestRunResolvesLinksAgainstRedirectDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/landing/", http.StatusFound)
		case "/landing/":
			_, _ = io.WriteString(w, `<a href="child">child</a>`)
		case "/landing/child":
			_, _ = io.WriteString(w, "child")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	crawler, startURL := newTestCrawlerAtPath(t, server, Settings{MaxDepth: 1}, "/start")
	var results []PageResult
	err := crawler.Run(context.Background(), startURL, func(page PageResult) error {
		results = append(results, page)
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	wantOriginal := []string{"/start", "/landing/child"}
	wantFinal := []string{"/landing/", "/landing/child"}
	if got := originalPaths(t, results); !reflect.DeepEqual(got, wantOriginal) {
		t.Fatalf("original paths = %v, want %v", got, wantOriginal)
	}
	if got := finalPaths(t, results); !reflect.DeepEqual(got, wantFinal) {
		t.Fatalf("final paths = %v, want %v", got, wantFinal)
	}
	if results[0].Depth != 0 || results[1].Depth != 1 {
		t.Fatalf("result depths = [%d %d], want [0 1]", results[0].Depth, results[1].Depth)
	}
}

func TestRunRejectsRedirectToDifferentHost(t *testing.T) {
	var destinationRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationRequests.Add(1)
		_, _ = io.WriteString(w, "must not be requested")
	}))
	defer destination.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()

	crawler, startURL := newTestCrawler(t, source, Settings{MaxDepth: 0})
	var results []PageResult
	err := crawler.Run(context.Background(), startURL, func(page PageResult) error {
		results = append(results, page)
		return nil
	})
	if err == nil {
		t.Fatal("Run() error = nil, want rejected redirect error")
	}
	if destinationRequests.Load() != 0 {
		t.Fatalf("destination requests = %d, want 0", destinationRequests.Load())
	}
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("callback results = %+v, want one failed result", results)
	}
	if results[0].StatusCode != http.StatusFound {
		t.Fatalf("PageResult.StatusCode = %d, want %d", results[0].StatusCode, http.StatusFound)
	}
}

func TestRunSuppressesRedirectToProcessedPage(t *testing.T) {
	var targetRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = io.WriteString(w, `<a href="/a">a</a><a href="/b">b</a>`)
		case "/a", "/b":
			http.Redirect(w, r, "/target", http.StatusFound)
		case "/target":
			targetRequests.Add(1)
			_, _ = io.WriteString(w, "target")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	crawler, startURL := newTestCrawler(t, server, Settings{MaxDepth: 1})
	var results []PageResult
	err := crawler.Run(context.Background(), startURL, func(page PageResult) error {
		results = append(results, page)
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if targetRequests.Load() != 1 {
		t.Fatalf("target requests = %d, want 1", targetRequests.Load())
	}
	wantOriginal := []string{"/", "/a"}
	if got := originalPaths(t, results); !reflect.DeepEqual(got, wantOriginal) {
		t.Fatalf("callback original paths = %v, want %v", got, wantOriginal)
	}
}

func TestRunLimitsRedirectChain(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer server.Close()

	crawler, startURL := newTestCrawlerAtPath(t, server, Settings{MaxDepth: 0}, "/loop")
	var results []PageResult
	err := crawler.Run(context.Background(), startURL, func(page PageResult) error {
		results = append(results, page)
		return nil
	})
	if err == nil {
		t.Fatal("Run() error = nil, want redirect limit error")
	}
	if requests.Load() != maxRedirects {
		t.Fatalf("redirect chain requests = %d, want %d", requests.Load(), maxRedirects)
	}
	if len(results) != 1 || results[0].StatusCode != http.StatusFound {
		t.Fatalf("callback results = %+v, want one failed redirect result", results)
	}
}
