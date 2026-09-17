package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/korotkovfedor/skitter/crawler"
)

func main() {
	targetURL, err := url.Parse("https://quotes.toscrape.com/")
	if err != nil {
		panic(err)
	}

	client := http.DefaultClient
	settings := crawler.Settings{
		TargetHost:      targetURL.Host,
		MaxLinksPerPage: 10,
		MaxDepth:        2,
		MaxConcurrency:  10,
	}

	c, err := crawler.New(client, settings)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*90)
	defer cancel()

	in, err := c.Run(ctx, targetURL)
	if err != nil {
		panic(err)
	}

	for result := range in {
		if result.Err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", result.OriginalURL, result.Err)
		}
		if result.Page != nil {
			fmt.Println(result.Page.URL)
		}
	}
}
