package crawler

import (
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/url"
	"slices"
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

func (s *Settings) validate() error {
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

func (c *Crawler) Run(targetUrl url.URL) error {
	if c.settings.TargetHost != "" && targetUrl.Host != c.settings.TargetHost {
		return errors.New("targetHost does not match targetUrl")
	}

	if err := c.crawl(targetUrl.String(), c.settings.MaxDepth, &map[string]struct{}{}); err != nil {
		return err
	}

	return nil
}

func (c *Crawler) crawl(targetUrl string, remainingDepth int, visited *map[string]struct{}) error {
	if remainingDepth < 0 {
		return errors.New("remainingDepth is below zero")
	}

	_, ok := (*visited)[targetUrl]
	if ok {
		return nil
	}

	(*visited)[targetUrl] = struct{}{}
	fmt.Println(targetUrl)
	data, err := c.fetch(targetUrl)
	if err != nil {
		if c.settings.MaxDepth == remainingDepth {
			return fmt.Errorf("start page fetch: %w", err)
		}
		log.Printf("skip url <%s> with error: %v", targetUrl, err)
		return nil
	}

	body, err := html.Parse(strings.NewReader(data))
	if err != nil {
		return err
	}

	links := extractLinks(body)
	links, err = convertToAbs(targetUrl, links)
	if err != nil {
		return err
	}

	if c.settings.TargetHost != "" {
		links, err = c.reduceUntargeted(links)
		if err != nil {
			return err
		}
	}

	// TODO: Just use golang-set
	uniqueLinksSet := map[string]struct{}{}
	for _, link := range links {
		_, ok = (*visited)[link]
		if ok {
			continue
		}

		uniqueLinksSet[link] = struct{}{}
	}

	uniqueLinks := slices.Collect(maps.Keys(uniqueLinksSet))
	maxLinksPerPage := c.settings.MaxLinksPerPage
	if maxLinksPerPage > 0 && len(uniqueLinks) > maxLinksPerPage {
		uniqueLinks = uniqueLinks[:maxLinksPerPage]
	}

	for _, link := range uniqueLinks {
		if remainingDepth == 0 {
			return nil
		}

		if err = c.crawl(link, remainingDepth-1, visited); err != nil {
			return err
		}
	}

	return nil
}

func (c *Crawler) fetch(url string) (string, error) {
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
		convertedLinks = append(convertedLinks, absoluteUrl.String())
	}

	return convertedLinks, nil
}
