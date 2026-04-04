package net

import (
	"context"
	"net/http"
	"time"
)

const timeoutInSeconds = 60

var reqTransport = &http.Transport{
	MaxIdleConns:          50,
	MaxIdleConnsPerHost:   20,
	IdleConnTimeout:       time.Duration(timeoutInSeconds) * time.Second,
	ResponseHeaderTimeout: time.Duration(timeoutInSeconds) * time.Second,
}

// GetOAuthClient returns an HTTP client that injects a Bearer token into every request.
func GetOAuthClient(_ context.Context, token string) *http.Client {
	return &http.Client{
		Timeout:   time.Duration(timeoutInSeconds) * time.Second,
		Transport: &tokenTransport{token: token, base: reqTransport},
	}
}

type tokenTransport struct {
	token string
	base  http.RoundTripper
}

func (t *tokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "token "+t.token)
	return t.base.RoundTrip(req)
}
