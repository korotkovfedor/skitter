package crawler

import "fmt"

// Stage identifies the operation that failed while processing a page.
// Results emitted by the crawler use [StageFetch] or [StageExtractLinks].
type Stage string

const (
	// StageFetch covers request creation, HTTP requests, redirects, response
	// status validation, and reading the response body. No Page is available.
	StageFetch Stage = "fetch"

	// StageExtractLinks covers HTML parsing and outgoing URL processing.
	// The downloaded Page remains available in the result.
	StageExtractLinks Stage = "extract links"
)

// CrawlError associates a page-processing failure with its stage.
// Use errors.As on [PageResult.Err] to retrieve it. Unwrap preserves the cause
// for errors.Is and errors.As, including [HTTPError] and [ErrResponseTooLarge].
type CrawlError struct {
	// Stage is the operation that failed.
	Stage Stage

	// Err is the underlying cause. Errors emitted by the crawler have a non-nil cause.
	Err error
}

// Error returns the stage and the underlying error message.
func (e *CrawlError) Error() string {
	return fmt.Sprintf("%s: %v", e.Stage, e.Err)
}

// Unwrap returns the underlying cause.
func (e *CrawlError) Unwrap() error {
	return e.Err
}
