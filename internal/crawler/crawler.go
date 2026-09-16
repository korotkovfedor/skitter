package crawler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

const maxRedirects = 10

var (
	errAlreadyProcessed = errors.New("redirect target already processed")

	// ErrResponseTooLarge reports that a response body exceeds MaxResponseBytes.
	ErrResponseTooLarge = errors.New("response body exceeds configured size limit")
)

type Settings struct {
	// Zero is interpreted as single connection
	MaxConnections int

	// Zero is interpreted as no limit
	MaxLinksPerPage int

	// MaxResponseBytes is the largest response body accepted from one request.
	// Zero is interpreted as no limit.
	MaxResponseBytes int64

	// Zero is interpreted literally
	MaxDepth int

	// TargetHost must match URL.Host, including an optional port.
	// An empty value allows any host.
	TargetHost string
}

func (s Settings) MustLimitLinks() bool {
	return s.MaxLinksPerPage > 0
}

func (s Settings) validate() error {
	if s.MaxDepth < 0 {
		return errors.New("MaxDepth is less than 0")
	}

	if s.MaxConnections < 0 {
		return errors.New("MaxConnections is less than 0")
	}

	if s.MaxLinksPerPage < 0 {
		return errors.New("MaxLinksPerPage is less than 0")
	}

	if s.MaxResponseBytes < 0 {
		return errors.New("MaxResponseBytes is less than 0")
	}

	return nil
}

type Crawler struct {
	client   *http.Client
	settings Settings
}

func New(client *http.Client, settings Settings) (*Crawler, error) {
	if client == nil {
		return nil, errors.New("client is nil")
	}
	if err := settings.validate(); err != nil {
		return nil, err
	}

	if settings.MaxConnections == 0 {
		settings.MaxConnections = 1
	}

	// Keep the caller's client unchanged, including http.DefaultClient.
	crawlerClient := *client
	previousCheckRedirect := client.CheckRedirect
	crawlerClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}

		if previousCheckRedirect != nil {
			if err := previousCheckRedirect(req, via); err != nil {
				return err
			}
		}

		// Check after the caller's hook, which may modify the next request.
		if req.URL == nil || !isHttp(req.URL) {
			return errors.New("redirect URL must use HTTP or HTTPS and have a host")
		}
		if settings.TargetHost != "" && req.URL.Host != settings.TargetHost {
			return fmt.Errorf("redirect host %q does not match target host %q", req.URL.Host, settings.TargetHost)
		}

		return nil
	}

	return &Crawler{
		client:   &crawlerClient,
		settings: settings,
	}, nil
}

func (c *Crawler) Run(ctx context.Context, startUrl *url.URL, onResult func(page PageResult) error) error {
	if startUrl == nil {
		return errors.New("startUrl is nil")
	}

	if !isHttp(startUrl) {
		return errors.New("startUrl does not support HTTP")
	}

	if c.settings.TargetHost != "" && startUrl.Host != c.settings.TargetHost {
		return errors.New("targetHost does not match startUrl")
	}

	if onResult == nil {
		return errors.New("onResult is nil")
	}

	if err := c.crawl(ctx, startUrl, onResult); err != nil {
		return err
	}

	return nil
}

type linkEntry struct {
	url   string
	depth int
}

type fetchedPage struct {
	body string
	url  url.URL
}

func (c *Crawler) crawl(ctx context.Context, startUrl *url.URL, onResult func(page PageResult) error) error {
	cleanUrl := cleanUpUrl(*startUrl)

	links := []linkEntry{{url: cleanUrl.String(), depth: 0}}
	seen := map[string]struct{}{cleanUrl.String(): {}}
	// Unlike seen, processed contains only URLs whose page was fetched.
	// It includes both the requested address and the final redirect address.
	processed := map[string]struct{}{}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if len(links) == 0 {
			return nil
		}

		link := links[0]
		clear(links[:1])
		links = links[1:]

		if _, ok := processed[link.url]; ok {
			continue
		}

		page, err := c.fetch(ctx, link.url, processed)
		if err != nil {
			if errors.Is(err, errAlreadyProcessed) {
				continue
			}

			result := PageResult{
				OriginalURL: link.url,
				Depth:       link.depth,
				Err:         err,
			}
			if httpErr, ok := errors.AsType[*HTTPError](err); ok {
				result.StatusCode = httpErr.StatusCode
			}
			clientErr := onResult(result)

			if clientErr != nil {
				return clientErr
			}
			if link.depth == 0 {
				return fmt.Errorf("start page fetch: %w", err)
			}
			log.Printf("skip url <%s> with error: %v", link.url, err)
			continue
		}

		finalURL := page.url.String()
		_, alreadyProcessed := processed[finalURL]
		seen[finalURL] = struct{}{}
		processed[link.url] = struct{}{}
		processed[finalURL] = struct{}{}
		if alreadyProcessed {
			continue
		}

		clientErr := onResult(PageResult{
			OriginalURL: link.url,
			FinalURL:    finalURL,
			Depth:       link.depth,
			StatusCode:  200,
			HTML:        page.body,
			Err:         nil,
		})
		if clientErr != nil {
			return clientErr
		}

		if link.depth >= c.settings.MaxDepth {
			continue
		}

		body, err := html.Parse(strings.NewReader(page.body))
		if err != nil {
			return err
		}

		extractedLinks := extractLinks(body)
		extractedLinks, err = convertToAbs(finalURL, extractedLinks)
		if err != nil {
			return err
		}

		if c.settings.TargetHost != "" {
			extractedLinks, err = c.reduceUntargeted(extractedLinks)
			if err != nil {
				return err
			}
		}

		extractedLinks = removeDuplicates(extractedLinks)
		extractedLinks = removeSeen(extractedLinks, seen)
		if c.settings.MustLimitLinks() {
			extractedLinks = removeExceeds(extractedLinks, c.settings.MaxLinksPerPage)
		}

		for _, extractedLink := range extractedLinks {
			seen[extractedLink] = struct{}{}
			links = append(links, linkEntry{
				url:   extractedLink,
				depth: link.depth + 1,
			})
		}
	}
}

func (c *Crawler) fetch(ctx context.Context, targetURL string, processed map[string]struct{}) (fetchedPage, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return fetchedPage{}, fmt.Errorf("create request: %w", err)
	}

	client := *c.client
	checkRedirect := client.CheckRedirect
	finalURL := req.URL
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if err := checkRedirect(next, via); err != nil {
			return err
		}
		cleanURL := cleanUpUrl(*next.URL)
		if _, ok := processed[cleanURL.String()]; ok {
			return errAlreadyProcessed
		}
		finalURL = next.URL
		return nil
	}

	req.Header.Set("User-Agent", "SkitterBot/0.1 korotkoffst@gmail.com")
	resp, err := client.Do(req)
	if err != nil {
		// A rejected redirect can return both an error and a response.
		// Client.Do has already closed that response's body.
		if resp != nil {
			return fetchedPage{}, &HTTPError{
				StatusCode: resp.StatusCode,
				Err:        fmt.Errorf("fetch request: %w", err),
			}
		}
		return fetchedPage{}, fmt.Errorf("fetch request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fetchedPage{}, &HTTPError{StatusCode: resp.StatusCode}
	}

	body, err := readResponseBody(resp.Body, c.settings.MaxResponseBytes)
	if err != nil {
		return fetchedPage{}, &HTTPError{
			StatusCode: resp.StatusCode,
			Err:        fmt.Errorf("read body: %w", err),
		}
	}

	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL
	}
	return fetchedPage{
		body: string(body),
		url:  cleanUpUrl(*finalURL),
	}, nil
}

func readResponseBody(body io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes == 0 {
		return io.ReadAll(body)
	}

	limitedBody := &io.LimitedReader{R: body, N: maxBytes}
	contents, err := io.ReadAll(limitedBody)
	if err != nil {
		return nil, err
	}
	if limitedBody.N > 0 {
		return contents, nil
	}

	var extraByte [1]byte
	n, err := body.Read(extraByte[:])
	if n > 0 {
		return nil, fmt.Errorf("%w: limit is %d bytes", ErrResponseTooLarge, maxBytes)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	return contents, nil
}

func (c *Crawler) reduceUntargeted(links []string) ([]string, error) {
	if c.settings.TargetHost == "" {
		return nil, errors.New("target host is empty")
	}

	targetedLinks := make([]string, 0)
	for _, link := range links {
		u, err := url.Parse(link)
		if err != nil {
			return nil, fmt.Errorf("parse while reducing untargeted: %w", err)
		}

		if u.Host == c.settings.TargetHost {
			targetedLinks = append(targetedLinks, u.String())
		}
	}

	return targetedLinks, nil
}

func extractLinks(n *html.Node) []string {
	if n.Type == html.ElementNode && n.Data == "a" {
		for _, attr := range n.Attr {
			if attr.Key == "href" {

				return []string{attr.Val}
			}
		}
	}

	links := make([]string, 0)
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		links = append(links, extractLinks(child)...)
	}

	return links
}

func convertToAbs(baseUrlString string, links []string) ([]string, error) {
	baseUrl, err := url.Parse(baseUrlString)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}

	convertedLinks := make([]string, 0)

	for _, link := range links {
		l, err := url.Parse(link)
		if err != nil {
			continue
		}

		absoluteUrl := baseUrl.ResolveReference(l)

		if !isHttp(absoluteUrl) {
			continue
		}

		cleanedUrl := cleanUpUrl(*absoluteUrl)
		convertedLinks = append(convertedLinks, cleanedUrl.String())
	}

	return convertedLinks, nil
}

func isHttp(url *url.URL) bool {
	schemeValid := url.Scheme == "https" || url.Scheme == "http"
	hostValid := url.Hostname() != ""

	return schemeValid && hostValid
}

func cleanUpUrl(u url.URL) url.URL {
	u.Fragment = ""
	u.RawFragment = ""

	return u
}

func removeDuplicates(array []string) []string {
	seen := map[string]struct{}{}
	unique := make([]string, 0)

	for _, el := range array {
		_, ok := seen[el]
		if ok {
			continue
		}

		seen[el] = struct{}{}
		unique = append(unique, el)
	}

	return unique
}

func removeExceeds(array []string, limit int) []string {
	if len(array) > limit {
		return array[:limit]
	}

	return array
}

func removeSeen(array []string, seen map[string]struct{}) []string {
	unseen := make([]string, 0)

	for _, el := range array {
		_, ok := seen[el]
		if ok {
			continue
		}

		unseen = append(unseen, el)
	}

	return unseen
}
