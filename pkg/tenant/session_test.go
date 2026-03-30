package tenant

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashToken(t *testing.T) {
	t.Run("produces 64-char hex string", func(t *testing.T) {
		h := HashToken("sometoken")
		assert.Len(t, h, 64)
	})

	t.Run("idempotent", func(t *testing.T) {
		assert.Equal(t, HashToken("abc"), HashToken("abc"))
	})

	t.Run("different inputs produce different hashes", func(t *testing.T) {
		assert.NotEqual(t, HashToken("token1"), HashToken("token2"))
	})

	t.Run("known hash", func(t *testing.T) {
		// sha256("") = e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
		h := HashToken("")
		require.Len(t, h, 64)
		assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", h)
	})
}
