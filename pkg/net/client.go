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

const (
	// GitHubTimeout is the default timeout for GitHub API calls.
	GitHubTimeout = 10 * time.Second

	// GitHubAccept is the standard Accept header for GitHub API v3.
	GitHubAccept = "application/vnd.github+json"

	// QuotaCheckTimeout is a short timeout for lightweight API quota checks.
	QuotaCheckTimeout = 5 * time.Second
)

// GitHubClient is a shared HTTP client for GitHub API calls.
var GitHubClient = &http.Client{Timeout: GitHubTimeout}

// QuotaCheckClient is a shared HTTP client for lightweight rate-limit checks.
var QuotaCheckClient = &http.Client{Timeout: QuotaCheckTimeout}

// SetGitHubHeaders sets the standard Authorization and Accept headers for GitHub API requests.
func SetGitHubHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", GitHubAccept)
}
