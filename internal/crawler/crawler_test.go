package crawler

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

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
