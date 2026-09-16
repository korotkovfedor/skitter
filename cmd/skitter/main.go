package main

import (
	"context"
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
		MaxLinksPerPage: 20,
		MaxDepth:        2,
	}

	c, err := crawler.New(client, settings)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	err = c.Run(ctx, targetUrl)
	if err != nil {
		panic(err)
	}
}
