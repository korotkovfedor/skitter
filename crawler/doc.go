// Package crawler downloads HTTP and HTTPS pages and follows anchor links
// with configurable depth, concurrency, retries, and response body limits.
// Create a crawler with [New], then consume the channel returned by [Crawler.Run].
//
// # Traversal and URL identity
//
// The starting page has depth zero. Outgoing addresses are taken from anchor
// href attributes in document order and resolved against the final response URL
// after redirects. Malformed references and non-HTTP(S) URLs are skipped.
// URL fragments are removed before deduplication; query strings are retained.
// Identity uses the resulting URL strings, with no additional normalization of
// host case, default ports, or query parameter order.
//
// Each run maintains its own FIFO queue and set of seen addresses. An address
// is marked seen when enqueued and is not re-enqueued after failure; request
// retries are controlled separately by Settings.MaxRetries. Pages downloaded
// through redirects are deduplicated by final URL. Concurrent requests may
// still download the same final page, but only the first accepted page result
// is published and considered for traversal, even if its URL extraction failed.
//
// Concurrent traversal does not wait for an entire depth level to finish.
// A fast, longer route can discover an address before a slow, shorter route.
// Its assigned depth is not revised later. As a result, MaxDepth does not
// guarantee visiting every page within that many steps of the starting page:
// discovering a page first at the depth limit can prevent visiting its children.
// Result order and which redirect source supplies a result can also vary.
//
// # Results and errors
//
// Run returns only input-validation errors directly. Processing errors are
// delivered in [PageResult], including errors on the starting page; they do
// not stop other queued work. A [CrawlError] identifies the failed [Stage],
// and its cause can be inspected with errors.Is or errors.As. A downloaded
// [Page] is preserved if outgoing URL extraction fails. Always inspect Page
// independently of Err when useful content should survive extraction errors.
//
// Cancellation closes the result stream without a guaranteed final error
// result. Callers must cancel the run's context if they stop receiving early.
// See [Crawler.Run] for cancellation and result-delivery guarantees.
//
// # HTTP behavior and limits
//
// Requests use GET. Only 2xx responses with a fully read body count as fetched
// pages. Other statuses and body-reading failures produce [HTTPError] when
// a status is available. Retries restart the request from its original address;
// their status and delay rules are described in [Settings].
//
// The supplied http.Client controls transport, cookies, and request timeout.
// The crawler does not impose a whole-run deadline, a total page count limit,
// a queue size limit, or a delay between unrelated requests. Use a context
// deadline and appropriate settings to bound a run for the intended workload.
// Settings.UserAgent controls the User-Agent header; an empty value uses "SkitterBot/0.1".
//
// The crawler does not execute JavaScript, honor HTML base elements, check
// Content-Type before parsing, or consult robots.txt. These behaviors are not
// inferred from the supplied client or the target website.
package crawler
