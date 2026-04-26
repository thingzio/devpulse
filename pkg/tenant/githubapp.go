package tenant

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/thingzio/devpulse/pkg/net"
)

const defaultGitHubBaseURL = "https://api.github.com"

// GitHubAppConfig holds the GitHub App credentials for minting installation tokens.
type GitHubAppConfig struct {
	AppID      int64
	PrivateKey *rsa.PrivateKey
	BaseURL    string // override for testing, default: https://api.github.com
}

// InstallationToken is a short-lived token scoped to a GitHub App installation.
type InstallationToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CreateAppJWT creates a short-lived JWT signed with the GitHub App's private key.
func CreateAppJWT(cfg *GitHubAppConfig) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		// GitHub recommends backdating iat by 60s to account for clock drift
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		Issuer:    fmt.Sprintf("%d", cfg.AppID),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(cfg.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("signing app JWT: %w", err)
	}
	return signed, nil
}

// MintInstallationToken exchanges an App JWT for an installation-scoped access token.
// The returned token is valid for 1 hour and should never be persisted.
func MintInstallationToken(ctx context.Context, cfg *GitHubAppConfig, installationID int64) (*InstallationToken, error) {
	appJWT, err := CreateAppJWT(cfg)
	if err != nil {
		return nil, err
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultGitHubBaseURL
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", baseURL, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating installation token request: %w", err)
	}
	net.SetGitHubHeaders(req, appJWT)

	resp, err := net.GitHubClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting installation token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("installation token request: status %d", resp.StatusCode)
	}

	var it InstallationToken
	if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
		return nil, fmt.Errorf("decoding installation token: %w", err)
	}
	return &it, nil
}

// LoadGitHubAppConfig reads GitHub App credentials from environment variables.
func LoadGitHubAppConfig() (*GitHubAppConfig, error) {
	appIDStr := os.Getenv("GITHUB_APP_ID")
	keyPath := os.Getenv("GITHUB_APP_KEY_PATH")

	if appIDStr == "" || keyPath == "" {
		return nil, fmt.Errorf("GITHUB_APP_ID and GITHUB_APP_KEY_PATH are required")
	}

	appID, err := strconv.ParseInt(appIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing GITHUB_APP_ID: %w", err)
	}

	keyData, err := os.ReadFile(keyPath) //nolint:gosec // path from trusted GITHUB_APP_KEY_PATH env var
	if err != nil {
		return nil, fmt.Errorf("reading private key from %s: %w", keyPath, err)
	}
	// Wipe the raw PEM bytes once parsed so a memory dump or coredump can't
	// recover the key from process heap. The parsed *rsa.PrivateKey holds its
	// own copy of the key material.
	defer func() {
		for i := range keyData {
			keyData[i] = 0
		}
	}()

	key, err := jwt.ParseRSAPrivateKeyFromPEM(keyData)
	if err != nil {
		return nil, fmt.Errorf("parsing private key from %s: %w", keyPath, err)
	}

	return &GitHubAppConfig{
		AppID:      appID,
		PrivateKey: key,
	}, nil
}
