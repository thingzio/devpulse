package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	defaultAuthURL  = "https://github.com/login/oauth/authorize"
	defaultTokenURL = "https://github.com/login/oauth/access_token"
	defaultUserURL  = "https://api.github.com/user"
	oauthScope      = "read:user user:email"
)

// Config holds GitHub OAuth App credentials.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	AuthURL      string // override for testing
	TokenURL     string // override for testing
	UserURL      string // override for testing
}

// GitHubUser represents the authenticated GitHub user profile.
type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

// BuildAuthURL returns the GitHub OAuth authorization URL and a random state token.
func BuildAuthURL(cfg *Config) (string, string) {
	state := randomState()
	authURL := cfg.AuthURL
	if authURL == "" {
		authURL = defaultAuthURL
	}
	v := url.Values{
		"client_id":    {cfg.ClientID},
		"redirect_uri": {cfg.RedirectURL},
		"scope":        {oauthScope},
		"state":        {state},
	}
	return authURL + "?" + v.Encode(), state
}

// ExchangeCode exchanges an authorization code for an access token.
func ExchangeCode(ctx context.Context, cfg *Config, code string) (string, error) {
	tokenURL := cfg.TokenURL
	if tokenURL == "" {
		tokenURL = defaultTokenURL
	}

	v := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(v.Encode()))
	if err != nil {
		return "", fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL from config
	if err != nil {
		return "", fmt.Errorf("exchanging code: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("oauth error: %s", result.Error)
	}
	if result.AccessToken == "" {
		return "", errors.New("empty access token")
	}
	return result.AccessToken, nil
}

// FetchUser retrieves the authenticated user's GitHub profile.
// The token is used only for this call and should be discarded afterward.
func FetchUser(ctx context.Context, cfg *Config, token string) (*GitHubUser, error) {
	userURL := cfg.UserURL
	if userURL == "" {
		userURL = defaultUserURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL from config
	if err != nil {
		return nil, fmt.Errorf("fetching user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github user API: status %d", resp.StatusCode)
	}

	var user GitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decoding user: %w", err)
	}
	return &user, nil
}

func randomState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
