package crawler

// PageResult describes the outcome of fetching a single page.
type PageResult struct {
	// OriginalURL is the requested URL before redirects.
	OriginalURL string

	// FinalURL is the URL that produced the response after redirects.
	// It is populated on success and empty on failure.
	FinalURL string

	// Depth is the number of link transitions from the starting page.
	// The starting page has depth zero; redirects do not increase it.
	Depth int

	// StatusCode is the HTTP response status, or zero if no response was received.
	StatusCode int

	// HTML is the downloaded page content. It may be empty on failure.
	HTML string

	// Err describes a failure to fetch the page. It is nil on success.
	// When a response status is available, errors.As can extract an *HTTPError.
	Err error
}
