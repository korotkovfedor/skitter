package crawler

import (
	"net/http/httptest"
	"net/url"
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
