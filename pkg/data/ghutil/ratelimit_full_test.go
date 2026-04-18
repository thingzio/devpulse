package ghutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	devnet "github.com/thingzio/devpulse/pkg/net"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckTokenQuotaFull_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resources":{"core":{"limit":5000,"remaining":3500,"reset":1700000000}}}`))
	}))
	defer srv.Close()

	orig := devnet.QuotaCheckClient
	devnet.QuotaCheckClient = srv.Client()
	t.Cleanup(func() { devnet.QuotaCheckClient = orig })

	// Override URL by providing a custom request — but CheckTokenQuotaFull hardcodes
	// the URL, so we test it through a transport that redirects.
	transport := &rewriteTransport{base: srv.Client().Transport, target: srv.URL}
	devnet.QuotaCheckClient = &http.Client{Transport: transport}

	q := CheckTokenQuotaFull(context.Background(), "test-token")
	require.NotNil(t, q)
	assert.Equal(t, 5000, q.Limit)
	assert.Equal(t, 3500, q.Remaining)
}

func TestCheckTokenQuotaFull_Error(t *testing.T) {
	// Use a client that always fails.
	orig := devnet.QuotaCheckClient
	devnet.QuotaCheckClient = &http.Client{Transport: &failTransport{}}
	t.Cleanup(func() { devnet.QuotaCheckClient = orig })

	q := CheckTokenQuotaFull(context.Background(), "test-token")
	assert.Nil(t, q)
}

func TestCheckTokenQuotaFull_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	orig := devnet.QuotaCheckClient
	transport := &rewriteTransport{base: srv.Client().Transport, target: srv.URL}
	devnet.QuotaCheckClient = &http.Client{Transport: transport}
	t.Cleanup(func() { devnet.QuotaCheckClient = orig })

	q := CheckTokenQuotaFull(context.Background(), "test-token")
	assert.Nil(t, q)
}

func TestTokenQuota_Struct(t *testing.T) {
	q := TokenQuota{Limit: 5000, Remaining: 3000}
	assert.Equal(t, 5000, q.Limit)
	assert.Equal(t, 3000, q.Remaining)
}

// rewriteTransport redirects all requests to the test server.
type rewriteTransport struct {
	base   http.RoundTripper
	target string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = t.target[len("http://"):]
	if t.base != nil {
		return t.base.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// failTransport always returns an error.
type failTransport struct{}

func (t *failTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, http.ErrServerClosed
}
