package crawler

import "errors"

// PageResult describes the outcome of fetching a page and, when needed, extracting its URLs.
// Results emitted by [Crawler.Run] have one of three forms:
//   - Fetch failure: Page is nil and Err identifies [StageFetch].
//   - Extraction failure: Page is non-nil and Err identifies [StageExtractLinks].
//   - Success: Page is non-nil and Err is nil.
//
// Success at MaxDepth means extraction was skipped, not that the page has no links.
type PageResult struct {
	// Depth counts link transitions along the route that first enqueued the URL.
	// The starting page has depth zero; redirects add no depth. With concurrent
	// traversal this need not be the shortest route to the page.
	Depth int

	// OriginalURL is the requested address before redirects, with its fragment
	// removed. For a deduplicated final page, this is the request whose result
	// was accepted first, not necessarily the first request that was started.
	OriginalURL string

	// StatusCode is the HTTP status associated with this result, or zero if
	// unavailable. It can be 2xx even on failure, for example if reading the
	// body failed. It does not preserve statuses from discarded retry attempts.
	StatusCode int

	// Page is present after an entire 2xx response body was successfully read
	// within MaxResponseBytes, even if URL extraction fails. A fetch failure
	// does not expose a partial body. Empty HTML does not imply a missing Page.
	Page *Page

	// Err is nil on success; otherwise it contains a *CrawlError.
	// Use errors.As to inspect the stage or an underlying *HTTPError, and
	// errors.Is to check causes such as [ErrResponseTooLarge].
	Err error
}

// Page contains a completely downloaded successful HTTP response.
// Its presence does not imply successful HTML parsing or a text/html content type.
type Page struct {
	// URL is the final response address after redirects, with its fragment removed.
	URL string

	// HTML is the response body as a string, without sanitization or explicit
	// character-set conversion. The crawler does not validate Content-Type.
	// An empty body is a valid successful response.
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
