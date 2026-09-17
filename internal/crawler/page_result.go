package crawler

import "errors"

type Page struct {
	URL  string
	HTML string
}

// PageResult describes the outcome of fetching a single page.
type PageResult struct {
	Depth       int
	OriginalURL string
	StatusCode  int
	Page        *Page
	Err         error
}

func (r workResult) pageResult() PageResult {
	result := PageResult{
		Depth:       r.link.depth,
		OriginalURL: r.link.url,
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
