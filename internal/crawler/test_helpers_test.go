package crawler

import (
	"context"
	"net/http/httptest"
	"net/url"
	"sort"
	"testing"
	"time"
)

const asyncTestTimeout = 5 * time.Second

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

func collectResults(t *testing.T, crawler *Crawler, startURL *url.URL) []PageResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), asyncTestTimeout)
	defer cancel()

	resultCh, err := crawler.Run(ctx, startURL)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	results := make([]PageResult, 0)
	for {
		select {
		case result, ok := <-resultCh:
			if !ok {
				return results
			}
			results = append(results, result)
		case <-ctx.Done():
			t.Fatalf("result channel did not close: %v", ctx.Err())
		}
	}
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

func sortedPaths(paths []string) []string {
	sort.Strings(paths)
	return paths
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func pathFromURL(t *testing.T, rawURL string) string {
	t.Helper()
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse result URL %q: %v", rawURL, err)
	}
	return parsedURL.Path
}
