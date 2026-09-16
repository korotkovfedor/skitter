package crawler

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

func (c *Crawler) pageLinks(pageHTML string, baseURL string) ([]string, error) {
	body, err := html.Parse(strings.NewReader(pageHTML))
	if err != nil {
		return nil, err
	}

	extractedLinks := extractLinks(body)
	extractedLinks, err = convertToAbs(baseURL, extractedLinks)
	if err != nil {
		return nil, err
	}

	if c.settings.TargetHost != "" {
		extractedLinks, err = c.reduceUntargeted(extractedLinks)
		if err != nil {
			return nil, err
		}
	}

	return extractedLinks, nil
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

func extractLinks(root *html.Node) []string {
	links := make([]string, 0)
	stack := []*html.Node{root}

	for len(stack) > 0 {
		last := len(stack) - 1
		node := stack[last]
		stack = stack[:last]

		if href, ok := linkHref(node); ok {
			links = append(links, href)
			continue
		}

		for child := node.LastChild; child != nil; child = child.PrevSibling {
			stack = append(stack, child)
		}
	}

	return links
}

func linkHref(node *html.Node) (string, bool) {
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
