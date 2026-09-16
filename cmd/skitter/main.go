package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/korotkovfedor/skitter/internal/crawler"
)

func main() {
	targetUrl, err := url.Parse("https://quotes.toscrape.com/")
	if err != nil {
		panic(err)
	}

	client := http.DefaultClient
	settings := crawler.Settings{
		TargetHost:      targetUrl.Host,
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

	in, err := c.Run(ctx, targetUrl)
	if err != nil {
		panic(err)
	}

	for page := range in {
		fmt.Println(page.FinalURL)
	}
}
