package crawler

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

func (c *Crawler) extractPageURLs(pageHTML string, baseURL string) ([]string, error) {
	body, err := html.Parse(strings.NewReader(pageHTML))
	if err != nil {
		return nil, err
	}

	hrefs := extractHrefs(body)
	urls, err := resolveURLs(baseURL, hrefs)
	if err != nil {
		return nil, err
	}

	if c.settings.TargetHost != "" {
		urls, err = c.filterTargetURLs(urls)
		if err != nil {
			return nil, err
		}
	}

	return urls, nil
}

func (c *Crawler) filterTargetURLs(urls []string) ([]string, error) {
	if c.settings.TargetHost == "" {
		return nil, errors.New("target host is empty")
	}

	targetURLs := make([]string, 0)
	for _, rawURL := range urls {
		parsedURL, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("parse URL while filtering target host: %w", err)
		}

		if parsedURL.Host == c.settings.TargetHost {
			targetURLs = append(targetURLs, parsedURL.String())
		}
	}

	return targetURLs, nil
}

func extractHrefs(root *html.Node) []string {
	hrefs := make([]string, 0)
	stack := []*html.Node{root}

	for len(stack) > 0 {
		last := len(stack) - 1
		node := stack[last]
		stack = stack[:last]

		if href, ok := findHref(node); ok {
			hrefs = append(hrefs, href)
			continue
		}

		for child := node.LastChild; child != nil; child = child.PrevSibling {
			stack = append(stack, child)
		}
	}

	return hrefs
}

func findHref(node *html.Node) (string, bool) {
	if node.Type != html.ElementNode || node.Data != "a" {
		return "", false
	}

	for _, attr := range node.Attr {
		if attr.Key == "href" {
			return attr.Val, true
		}
	}

	return "", false
}

func resolveURLs(rawBaseURL string, hrefs []string) ([]string, error) {
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}

	convertedURLs := make([]string, 0)

	for _, href := range hrefs {
		referenceURL, err := url.Parse(href)
		if err != nil {
			continue
		}

		absoluteURL := baseURL.ResolveReference(referenceURL)

		if !isHTTP(absoluteURL) {
			continue
		}

		cleanedURL := cleanUpURL(*absoluteURL)
		convertedURLs = append(convertedURLs, cleanedURL.String())
	}

	return convertedURLs, nil
}

func isHTTP(parsedURL *url.URL) bool {
	schemeValid := parsedURL.Scheme == "https" || parsedURL.Scheme == "http"
	hostValid := parsedURL.Hostname() != ""

	return schemeValid && hostValid
}

func cleanUpURL(parsedURL url.URL) url.URL {
	parsedURL.Fragment = ""
	parsedURL.RawFragment = ""

	return parsedURL
}
