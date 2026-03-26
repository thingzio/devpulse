package tenant

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

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
		baseURL = "https://api.github.com"
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", baseURL, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating installation token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting installation token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("installation token request: status %d", resp.StatusCode)
	}

	var it InstallationToken
	if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
		return nil, fmt.Errorf("decoding installation token: %w", err)
	}
	return &it, nil
}
