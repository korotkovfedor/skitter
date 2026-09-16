package crawler

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

type Settings struct {
	// Zero is interpreted as single connection
	MaxConnections int

	// Zero is interpreted as no limit
	MaxLinksPerPage int

	// Zero is interpreted literally
	MaxDepth int

	// Can be empty, set to filter [TargetHost] host
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

	return &Crawler{
		client:   client,
		settings: settings,
	}, nil
}

func (c *Crawler) Run(startUrl *url.URL) error {
	if startUrl == nil {
		return errors.New("startUrl is nil")
	}

	if !isHttp(startUrl) {
		return errors.New("startUrl does not support HTTP")
	}

	if c.settings.TargetHost != "" && startUrl.Host != c.settings.TargetHost {
		return errors.New("targetHost does not match startUrl")
	}

	if err := c.crawl(startUrl); err != nil {
		return err
	}

	return nil
}

type linkEntry struct {
	url   string
	depth int
}

func (c *Crawler) crawl(startUrl *url.URL) error {
	cleanUrl := cleanUpUrl(*startUrl)

	links := []linkEntry{{url: cleanUrl.String(), depth: 0}}
	seen := map[string]struct{}{cleanUrl.String(): {}}

	for {
		if len(links) == 0 {
			return nil
		}

		link := links[0]
		clear(links[:1])
		links = links[1:]

		data, err := c.fetch(link.url)
		if err != nil {
			if link.depth == 0 {
				return fmt.Errorf("start page fetch: %w", err)
			}
			log.Printf("skip url <%s> with error: %v", link.url, err)
			continue
		}

		// DO WORK FOR RETURN

		if link.depth >= c.settings.MaxDepth {
			continue
		}

		body, err := html.Parse(strings.NewReader(data))
		if err != nil {
			return err
		}

		extractedLinks := extractLinks(body)
		extractedLinks, err = convertToAbs(link.url, extractedLinks)
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

func (c *Crawler) fetch(url string) (string, error) {
	fmt.Println(url)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", "SkitterBot/0.1 korotkoffst@gmail.com")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status code <%d>", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	return string(body), nil
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
