package billing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWebhookHandler_InvalidSignature(t *testing.T) {
	cfg := &Config{WebhookSecret: "whsec_test"}
	handler := WebhookHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader("{}"))
	req.Header.Set("Stripe-Signature", "invalid")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
