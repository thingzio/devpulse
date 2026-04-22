package tenant

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAppJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	cfg := &GitHubAppConfig{
		AppID:      12345,
		PrivateKey: key,
	}

	token, err := CreateAppJWT(cfg)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// JWT should have 3 dot-separated parts
	assert.Equal(t, 3, len(strings.Split(token, ".")))
}

func TestMintInstallationToken_Success(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	expiry := time.Now().Add(time.Hour)
	it := InstallationToken{Token: "ghs_test123", ExpiresAt: expiry}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(it)
	}))
	defer srv.Close()

	cfg := &GitHubAppConfig{AppID: 1, PrivateKey: key, BaseURL: srv.URL}
	got, err := MintInstallationToken(context.Background(), cfg, 42)
	require.NoError(t, err)
	assert.Equal(t, "ghs_test123", got.Token)
}

func TestMintInstallationToken_NonCreatedStatus(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := &GitHubAppConfig{AppID: 1, PrivateKey: key, BaseURL: srv.URL}
	_, err = MintInstallationToken(context.Background(), cfg, 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestLoadGitHubAppConfig_MissingEnv(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_APP_KEY_PATH", "")
	_, err := LoadGitHubAppConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestLoadGitHubAppConfig_InvalidAppID(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "not-a-number")
	t.Setenv("GITHUB_APP_KEY_PATH", "/tmp/fake.pem")
	_, err := LoadGitHubAppConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GITHUB_APP_ID")
}

func TestLoadGitHubAppConfig_MissingKeyFile(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_APP_KEY_PATH", "/nonexistent/path/key.pem")
	_, err := LoadGitHubAppConfig()
	require.Error(t, err)
}

func TestLoadGitHubAppConfig_PKCS8Key(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	require.NoError(t, err)

	pemBlock := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Bytes})

	tmpFile := filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(tmpFile, pemBlock, 0600))

	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_APP_KEY_PATH", tmpFile)

	cfg, err := LoadGitHubAppConfig()
	require.NoError(t, err)
	assert.Equal(t, int64(12345), cfg.AppID)
	assert.NotNil(t, cfg.PrivateKey)
}

func TestLoadGitHubAppConfig_PKCS1Key(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pemBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(rsaKey),
	})

	tmpFile := filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(tmpFile, pemBlock, 0600))

	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_APP_KEY_PATH", tmpFile)

	cfg, err := LoadGitHubAppConfig()
	require.NoError(t, err)
	assert.Equal(t, int64(12345), cfg.AppID)
	assert.NotNil(t, cfg.PrivateKey)
}

func TestLoadGitHubAppConfig_InvalidPEM(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "key*.pem")
	require.NoError(t, err)
	f.WriteString("not a pem block")
	f.Close()

	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_APP_KEY_PATH", f.Name())
	_, err = LoadGitHubAppConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PEM")
}
