package crawler

import (
	"errors"
	"fmt"
	"net/http"
)

const maxRedirects = 10

func newCrawlerClient(client *http.Client, settings Settings) *http.Client {
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

	return &crawlerClient
}
