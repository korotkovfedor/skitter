package crawler

import "errors"

// PageResult describes the outcome of fetching a page and, when needed, extracting its URLs.
type PageResult struct {
	Depth       int
	OriginalURL string
	// StatusCode is the HTTP response status, or zero if unavailable.
	StatusCode int
	// Page is present after a successful fetch, even if URL extraction fails.
	Page *Page
	// Err reports a fetch or URL extraction failure through *CrawlError.
	Err error
}

type Page struct {
	URL  string
	HTML string
}

func (r workResult) pageResult() PageResult {
	result := PageResult{
		Depth:       r.job.depth,
		OriginalURL: r.job.url,
		Err:         r.err,
	}

	if r.page != nil {
		result.StatusCode = r.page.statusCode
		result.Page = &Page{
			URL:  r.page.url.String(),
			HTML: r.page.body,
		}
	}

	if httpErr, ok := errors.AsType[*HTTPError](r.err); ok {
		result.StatusCode = httpErr.StatusCode
	}

	return result
}
