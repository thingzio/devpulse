package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/thingzio/devpulse/pkg/tenant"
)

func TestVerifyWebhookSignature(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"action":"created"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	tests := []struct {
		name      string
		payload   []byte
		signature string
		secret    string
		want      bool
	}{
		{
			name:      "valid signature",
			payload:   payload,
			signature: validSig,
			secret:    secret,
			want:      true,
		},
		{
			name:      "wrong secret",
			payload:   payload,
			signature: validSig,
			secret:    "wrong-secret",
			want:      false,
		},
		{
			name:      "missing sha256 prefix",
			payload:   payload,
			signature: hex.EncodeToString(mac.Sum(nil)),
			secret:    secret,
			want:      false,
		},
		{
			name:      "invalid hex encoding",
			payload:   payload,
			signature: "sha256=notvalidhex!!!",
			secret:    secret,
			want:      false,
		},
		{
			name:      "empty signature",
			payload:   payload,
			signature: "",
			secret:    secret,
			want:      false,
		},
		{
			name:      "tampered payload",
			payload:   []byte(`{"action":"deleted"}`),
			signature: validSig,
			secret:    secret,
			want:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := verifyWebhookSignature(tc.payload, tc.signature, tc.secret)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestWebhookHandler(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"action":"created"}`)

	validSig := func(body []byte) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	t.Run("empty secret returns 503", func(t *testing.T) {
		h := WebhookHandler(nil, "")
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(payload))
		h(w, r)
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("invalid signature returns 401", func(t *testing.T) {
		h := WebhookHandler(nil, secret)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(payload))
		r.Header.Set("X-Hub-Signature-256", "sha256=badsig")
		h(w, r)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("valid sig with unknown event returns 200", func(t *testing.T) {
		h := WebhookHandler(nil, secret)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(payload))
		r.Header.Set("X-Hub-Signature-256", validSig(payload))
		r.Header.Set("X-GitHub-Event", "push") // not handled, ignored
		h(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestParseRepoNames(t *testing.T) {
	tests := []struct {
		name  string
		input []struct {
			FullName string `json:"full_name"`
		}
		want []tenant.OrgRepo
	}{
		{
			name:  "empty input",
			input: nil,
			want:  []tenant.OrgRepo{},
		},
		{
			name: "valid repos",
			input: []struct {
				FullName string `json:"full_name"`
			}{
				{FullName: "org1/repo1"},
				{FullName: "org2/repo2"},
			},
			want: []tenant.OrgRepo{
				{Org: "org1", Repo: "repo1"},
				{Org: "org2", Repo: "repo2"},
			},
		},
		{
			name: "missing slash skipped",
			input: []struct {
				FullName string `json:"full_name"`
			}{
				{FullName: "noslash"},
				{FullName: "org/repo"},
			},
			want: []tenant.OrgRepo{
				{Org: "org", Repo: "repo"},
			},
		},
		{
			name: "extra slash uses first two parts",
			input: []struct {
				FullName string `json:"full_name"`
			}{
				{FullName: "org/repo/extra"},
			},
			want: []tenant.OrgRepo{
				{Org: "org", Repo: "repo/extra"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRepoNames(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}
