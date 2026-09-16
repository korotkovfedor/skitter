package crawler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

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
