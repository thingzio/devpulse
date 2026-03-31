package net

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"time"
)

// GetHTTPClient returns a new HTTP client.
func GetHTTPClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	return &http.Client{
		Timeout:   time.Duration(timeoutInSeconds) * time.Second,
		Transport: reqTransport,
		Jar:       jar,
	}, nil
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
