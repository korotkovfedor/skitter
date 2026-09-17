package crawler

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunSendsUserAgentOnRetriesRedirectsAndChildPages(t *testing.T) {
	tests := []struct {
		name      string
		userAgent string
		want      string
	}{
		{name: "default", want: "SkitterBot/0.1"},
		{name: "custom", userAgent: "CatalogIndexer/1.0 (+https://example.com/bot)", want: "CatalogIndexer/1.0 (+https://example.com/bot)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests, rootAttempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if got := r.Header.Get("User-Agent"); got != tt.want {
					t.Errorf("%s User-Agent = %q, want %q", r.URL.Path, got, tt.want)
				}
				switch r.URL.Path {
				case "/":
					if rootAttempts.Add(1) == 1 {
						http.Error(w, "temporary", http.StatusServiceUnavailable)
						return
					}
					http.Redirect(w, r, "/landing", http.StatusFound)
				case "/landing":
					_, _ = io.WriteString(w, `<a href="/child">child</a>`)
				case "/child":
					_, _ = io.WriteString(w, "leaf")
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			crawler, startURL := newTestCrawler(t, server, Settings{
				UserAgent:  tt.userAgent,
				MaxDepth:   1,
				MaxRetries: 1,
			})
			results := collectResults(t, crawler, startURL)
			if len(results) != 2 {
				t.Fatalf("result count = %d, want 2", len(results))
			}
			for _, result := range results {
				if result.Err != nil || result.Page == nil {
					t.Fatalf("result = %+v, want successful page", result)
				}
			}
			if got := requests.Load(); got != 4 {
				t.Fatalf("requests = %d, want initial request, retry, redirect, and child", got)
			}
		})
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
	results := collectResults(t, crawler, startURL)
	if len(results) != 1 {
		t.Fatalf("result count = %d, want 1", len(results))
	}
	if !errors.Is(results[0].Err, ErrResponseTooLarge) {
		t.Fatalf("PageResult.Err = %v, want ErrResponseTooLarge", results[0].Err)
	}
	if results[0].StatusCode != http.StatusOK {
		t.Fatalf("PageResult.StatusCode = %d, want %d", results[0].StatusCode, http.StatusOK)
	}
	if results[0].Page != nil {
		t.Fatalf("failed PageResult contains a page: %+v", results[0])
	}
}

func TestRunAcceptsAll2xxResponses(t *testing.T) {
	statuses := []struct {
		name string
		code int
	}{
		{name: "200 OK", code: http.StatusOK},
		{name: "201 Created", code: http.StatusCreated},
		{name: "204 No Content", code: http.StatusNoContent},
		{name: "299", code: 299},
	}

	for _, status := range statuses {
		t.Run(status.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status.code)
			}))
			defer server.Close()

			crawler, startURL := newTestCrawler(t, server, Settings{MaxDepth: 0})
			results := collectResults(t, crawler, startURL)
			if len(results) != 1 || results[0].Err != nil {
				t.Fatalf("results = %+v, want one successful result", results)
			}
			if results[0].StatusCode != status.code {
				t.Fatalf("PageResult.StatusCode = %d, want %d", results[0].StatusCode, status.code)
			}
		})
	}
}

func TestRunRetriesTemporaryResponse(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "recovered")
	}))
	defer server.Close()

	crawler, startURL := newTestCrawler(t, server, Settings{
		MaxDepth:     0,
		MaxRetries:   1,
		RetryLatency: 0,
	})
	results := collectResults(t, crawler, startURL)
	if attempts.Load() != 2 {
		t.Fatalf("request attempts = %d, want 2", attempts.Load())
	}
	if len(results) != 1 || results[0].StatusCode != http.StatusOK || results[0].Page == nil || results[0].Page.HTML != "recovered" {
		t.Fatalf("results = %+v, want recovered 200 response", results)
	}
}

func TestRunKeepsFinalTemporaryResponse(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "temporary", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	crawler, startURL := newTestCrawler(t, server, Settings{
		MaxDepth:     0,
		MaxRetries:   2,
		RetryLatency: 0,
	})
	results := collectResults(t, crawler, startURL)
	if attempts.Load() != 3 {
		t.Fatalf("request attempts = %d, want 3", attempts.Load())
	}
	if len(results) != 1 || results[0].StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("results = %+v, want final 503 response", results)
	}
	if _, ok := errors.AsType[*HTTPError](results[0].Err); !ok {
		t.Fatalf("PageResult.Err = %T, want *HTTPError", results[0].Err)
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

func TestNewRejectsInvalidRetrySettings(t *testing.T) {
	tests := []Settings{
		{MaxRetries: -1},
		{RetryLatency: -time.Nanosecond},
	}

	for _, settings := range tests {
		if _, err := New(http.DefaultClient, settings); err == nil {
			t.Fatalf("New(%+v) error = nil, want validation error", settings)
		}
	}
}
