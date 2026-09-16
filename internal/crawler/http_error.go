package crawler

import "fmt"

// HTTPError describes a fetch failure with a known HTTP response status.
// Use errors.As to retrieve it through wrapped errors.
type HTTPError struct {
	// StatusCode is the response status. It may be 200 if reading the body failed.
	StatusCode int

	// Err is the underlying failure, or nil for an unexpected response status.
	Err error
}

func (e *HTTPError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("HTTP status %d: %v", e.StatusCode, e.Err)
	}
	return fmt.Sprintf("unexpected HTTP status %d", e.StatusCode)
}

// Unwrap preserves the underlying error for errors.Is and errors.As.
func (e *HTTPError) Unwrap() error {
	return e.Err
}
