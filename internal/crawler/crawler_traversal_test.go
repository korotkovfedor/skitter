package crawler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRunVisitsPagesAtExpectedDepths(t *testing.T) {
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

	crawler, startURL := newTestCrawler(t, server, Settings{
		MaxDepth:       2,
		MaxConcurrency: 3,
	})
	results := collectResults(t, crawler, startURL)

	wantPaths := []string{"/", "/a", "/b", "/a/one", "/b/one"}
	if got := sortedPaths(finalPaths(t, results)); !reflect.DeepEqual(got, sortedPaths(wantPaths)) {
		t.Fatalf("result paths = %v, want pages %v", got, sortedPaths(wantPaths))
	}

	wantDepths := map[string]int{
		"/":      0,
		"/a":     1,
		"/b":     1,
		"/a/one": 2,
		"/b/one": 2,
	}
	for _, result := range results {
		path := pathFromURL(t, result.FinalURL)
		if result.Depth != wantDepths[path] {
			t.Errorf("result %q depth = %d, want %d", path, result.Depth, wantDepths[path])
		}
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
			name:     "per-page link limit keeps the first links",
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
			results := collectResults(t, crawler, startURL)

			if got := sortedPaths(finalPaths(t, results)); !reflect.DeepEqual(got, sortedPaths(tt.want)) {
				t.Fatalf("result paths = %v, want %v", got, sortedPaths(tt.want))
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
	results := collectResults(t, crawler, startURL)

	want := []string{"/", "/c"}
	if got := sortedPaths(finalPaths(t, results)); !reflect.DeepEqual(got, sortedPaths(want)) {
		t.Fatalf("result paths = %v, want %v", got, sortedPaths(want))
	}
}
