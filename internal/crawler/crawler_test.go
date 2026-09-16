package crawler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const asyncTestTimeout = 5 * time.Second

func TestRunVisitsPagesInBFSOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = io.WriteString(w, `<a href="/a">a</a><a href="/b">b</a><a href="/a">duplicate</a>`)
		case "/a":
			_, _ = io.WriteString(w, `<a href="/a/one">a-one</a>`)
		case "/b":
			_, _ = io.WriteString(w, `<a href="/b/one">b-one</a>`)
		case "/a/one", "/b/one":
			_, _ = io.WriteString(w, "leaf")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	crawler, startURL := newTestCrawler(t, server, Settings{MaxDepth: 2})
	var results []PageResult
	err := crawler.Run(context.Background(), startURL, func(page PageResult) error {
		results = append(results, page)
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	wantPaths := []string{"/", "/a", "/b", "/a/one", "/b/one"}
	if got := finalPaths(t, results); !reflect.DeepEqual(got, wantPaths) {
		t.Fatalf("callback paths = %v, want BFS order %v", got, wantPaths)
	}
}

func TestRunRespectsTraversalLimits(t *testing.T) {
	tests := []struct {
		name     string
		settings Settings
		want     []string
	}{
		{
			name:     "zero depth fetches only start page",
			settings: Settings{MaxDepth: 0},
			want:     []string{"/"},
		},
		{
			name:     "max depth includes boundary pages",
			settings: Settings{MaxDepth: 1},
			want:     []string{"/", "/a", "/b", "/c"},
		},
		{
			name:     "per-page link limit preserves source order",
			settings: Settings{MaxDepth: 1, MaxLinksPerPage: 2},
			want:     []string{"/", "/a", "/b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					_, _ = io.WriteString(w, `<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a>`)
				case "/a", "/b", "/c":
					_, _ = io.WriteString(w, `<a href="/leaf">leaf</a>`)
				case "/leaf":
					_, _ = io.WriteString(w, "leaf")
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			crawler, startURL := newTestCrawler(t, server, tt.settings)
			var results []PageResult
			err := crawler.Run(context.Background(), startURL, func(page PageResult) error {
				results = append(results, page)
				return nil
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}

			if got := finalPaths(t, results); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("callback paths = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunAppliesLinkLimitAfterSeenFiltering(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = io.WriteString(w, `<a href="/">already seen</a><a href="/c">c</a>`)
		case "/c":
			_, _ = io.WriteString(w, "leaf")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	crawler, startURL := newTestCrawler(t, server, Settings{
		MaxDepth:        1,
		MaxLinksPerPage: 1,
	})
	var results []PageResult
	err := crawler.Run(context.Background(), startURL, func(page PageResult) error {
		results = append(results, page)
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []string{"/", "/c"}
	if got := finalPaths(t, results); !reflect.DeepEqual(got, want) {
		t.Fatalf("callback paths = %v, want %v", got, want)
	}
}

func TestRunReportsResponseSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "four")
	}))
	defer server.Close()

	crawler, startURL := newTestCrawler(t, server, Settings{
		MaxDepth:         0,
		MaxResponseBytes: 3,
	})
	var results []PageResult
	err := crawler.Run(context.Background(), startURL, func(page PageResult) error {
		results = append(results, page)
		return nil
	})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("Run() error = %v, want ErrResponseTooLarge", err)
	}
	if len(results) != 1 {
		t.Fatalf("callback count = %d, want 1", len(results))
	}
	if !errors.Is(results[0].Err, ErrResponseTooLarge) {
		t.Fatalf("PageResult.Err = %v, want ErrResponseTooLarge", results[0].Err)
	}
	if results[0].StatusCode != http.StatusOK {
		t.Fatalf("PageResult.StatusCode = %d, want %d", results[0].StatusCode, http.StatusOK)
	}
	if results[0].HTML != "" || results[0].FinalURL != "" {
		t.Fatalf("failed PageResult contains HTML or FinalURL: %+v", results[0])
	}
}

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
	if err != callbackErr {
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

func TestReadResponseBodyLimit(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		limit   int64
		want    string
		wantErr error
	}{
		{
			name:  "unlimited",
			body:  "four",
			limit: 0,
			want:  "four",
		},
		{
			name:  "below limit",
			body:  "two",
			limit: 3,
			want:  "two",
		},
		{
			name:  "at limit",
			body:  "three",
			limit: 5,
			want:  "three",
		},
		{
			name:    "over limit",
			body:    "four",
			limit:   3,
			wantErr: ErrResponseTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readResponseBody(strings.NewReader(tt.body), tt.limit)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("readResponseBody() error = %v, want errors.Is(..., %v)", err, tt.wantErr)
			}
			if string(got) != tt.want {
				t.Fatalf("readResponseBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewRejectsNegativeMaxResponseBytes(t *testing.T) {
	_, err := New(http.DefaultClient, Settings{MaxResponseBytes: -1})
	if err == nil {
		t.Fatal("New() error = nil, want validation error")
	}
}

func newTestCrawler(t *testing.T, server *httptest.Server, settings Settings) (*Crawler, *url.URL) {
	t.Helper()
	return newTestCrawlerAtPath(t, server, settings, "/")
}

func newTestCrawlerAtPath(t *testing.T, server *httptest.Server, settings Settings, path string) (*Crawler, *url.URL) {
	t.Helper()

	startURL, err := url.Parse(server.URL + path)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	settings.TargetHost = startURL.Host
	crawler, err := New(server.Client(), settings)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return crawler, startURL
}

func originalPaths(t *testing.T, results []PageResult) []string {
	t.Helper()
	paths := make([]string, 0, len(results))
	for _, result := range results {
		paths = append(paths, pathFromURL(t, result.OriginalURL))
	}
	return paths
}

func finalPaths(t *testing.T, results []PageResult) []string {
	t.Helper()
	paths := make([]string, 0, len(results))
	for _, result := range results {
		paths = append(paths, pathFromURL(t, result.FinalURL))
	}
	return paths
}

func pathFromURL(t *testing.T, rawURL string) string {
	t.Helper()
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse result URL %q: %v", rawURL, err)
	}
	return parsedURL.Path
}
