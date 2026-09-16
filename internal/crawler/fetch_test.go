package crawler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
			var results []PageResult
			err := crawler.Run(context.Background(), startURL, func(page PageResult) error {
				results = append(results, page)
				return nil
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(results) != 1 || results[0].Err != nil {
				t.Fatalf("callback results = %+v, want one successful result", results)
			}
			if results[0].StatusCode != status.code {
				t.Fatalf("PageResult.StatusCode = %d, want %d", results[0].StatusCode, status.code)
			}
		})
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
