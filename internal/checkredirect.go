package internal

import "net/http"

// https://pkg.go.dev/net/http#Transport
func newHttpClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
