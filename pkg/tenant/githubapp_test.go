package tenant

import (
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"

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
