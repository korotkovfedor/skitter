# Skitter

A Go library for crawling HTTP/HTTPS pages and following anchor links. Supports concurrent requests, depth and host limits, retries, redirects, and response size limits.

Requires Go **1.26.0+**.

## Install

```sh
go get github.com/korotkovfedor/skitter/crawler
```

## Usage

Create a crawler with `crawler.New(client, settings)`, call `Run(ctx, startURL)`, and read `PageResult` values from the returned channel.

See the [basic example](examples/basic/main.go). From this repository, run:

```sh
go run ./examples/basic
```

## Behavior

- `MaxDepth: 0` fetches only the starting page. Concurrent traversal does not guarantee shortest-path depth or result order.
- Page failures arrive in `PageResult.Err`. A downloaded `Page` remains available if link extraction fails.
- Cancel the context if you stop reading results early. Channel closure alone does not indicate success.
- Default `User-Agent`: `SkitterBot/0.1`; override it with `Settings.UserAgent`.
- No JavaScript execution or `robots.txt` handling.

Full API documentation: `go doc -all ./crawler`.

## License

[MIT](LICENSE).
