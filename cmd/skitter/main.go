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
		MaxLinksPerPage: 20,
		MaxDepth:        2,
	}

	c, err := crawler.New(client, settings)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	err = c.Run(ctx, targetUrl, func(page crawler.PageResult) error {
		fmt.Println(page.FinalURL)
		return nil
	})
	if err != nil {
		panic(err)
	}
}
