package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPushDeliveryClientPrivateCallbacks verifies loopback callbacks are
// blocked by default, delivered when explicitly allowed, and that redirects
// are refused in both modes.
func TestPushDeliveryClientPrivateCallbacks(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	blocked := newPushDeliveryClient(false)
	_, err := blocked.Post(target.URL, "application/json", nil)
	require.Error(t, err)

	allowed := newPushDeliveryClient(true)
	resp, err := allowed.Post(target.URL, "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	resp, err = allowed.Post(redirector.URL, "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode)
}
