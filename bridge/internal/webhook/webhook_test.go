package webhook

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// setDefaultURL points the built-in fallback webhook URL (used when
// WEBHOOK_URL is unset) at a test server for the duration of a test, and
// restores the previous value on cleanup.
func setDefaultURL(t *testing.T, url string) {
	t.Helper()
	prev := DefaultURL
	DefaultURL = url
	t.Cleanup(func() { DefaultURL = prev })
}

// TestSendTextAttachesBridgeTokenHeader verifies that outbound webhook POSTs
// carry the shared bridge token as an "X-Bridge-Token" header so a fail-closed
// inbound-auth receiver accepts them. The token travels on a dedicated header,
// not Authorization, so it never collides with a receiver's own
// Authorization-based auth (see TestSendTextPreservesURLBasicAuth).
func TestSendTextAttachesBridgeTokenHeader(t *testing.T) {
	const token = "test-bridge-token-1234567890abcdef"

	var gotToken, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Bridge-Token")
		gotContentType = r.Header.Get("Content-Type")
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("WEBHOOK_URL", srv.URL)
	sender := &Sender{Token: token}

	sender.SendText("123@s.whatsapp.net", "hello", "123@s.whatsapp.net", false, "", "", "")

	if gotToken != token {
		t.Fatalf("X-Bridge-Token header = %q, want %q", gotToken, token)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type header = %q, want application/json", gotContentType)
	}
}

// TestSendTextOmitsBridgeTokenHeaderWhenNoToken verifies that when no bridge
// token is configured the webhook still fires WITHOUT an X-Bridge-Token header,
// so deployments that predate the token rollout keep working.
func TestSendTextOmitsBridgeTokenHeaderWhenNoToken(t *testing.T) {
	var gotToken string
	var received bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		gotToken = r.Header.Get("X-Bridge-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("WEBHOOK_URL", srv.URL)
	sender := &Sender{}

	sender.SendText("123@s.whatsapp.net", "hi", "123@s.whatsapp.net", false, "", "", "")

	if !received {
		t.Fatal("webhook was not delivered")
	}
	if gotToken != "" {
		t.Fatalf("expected no X-Bridge-Token header, got %q", gotToken)
	}
}

// TestSendTextPreservesURLBasicAuth: net/http automatically derives an
// "Authorization: Basic" header from credentials embedded in the webhook URL
// (http://user:pass@host/...) whenever the outgoing request's Authorization
// header is otherwise unset. An earlier design sent the bridge token via
// Authorization, which silently clobbered that behavior for any receiver
// relying on URL userinfo. Sending the token as X-Bridge-Token instead must
// leave Authorization untouched so Go's built-in URL-credential handling
// keeps working.
func TestSendTextPreservesURLBasicAuth(t *testing.T) {
	const user, pass = "hookuser", "hookpass"
	const token = "test-bridge-token-1234567890abcdef"

	var gotAuth, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotToken = r.Header.Get("X-Bridge-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("failed to parse test server URL: %v", err)
	}
	u.User = url.UserPassword(user, pass)

	t.Setenv("WEBHOOK_URL", u.String())
	sender := &Sender{Token: token}

	sender.SendText("123@s.whatsapp.net", "hello", "123@s.whatsapp.net", false, "", "", "")

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	if gotAuth != wantAuth {
		t.Fatalf("Authorization header = %q, want %q (URL basic auth must survive)", gotAuth, wantAuth)
	}
	if gotToken != token {
		t.Fatalf("X-Bridge-Token header = %q, want %q", gotToken, token)
	}
}

// TestSendTextOmitsBridgeTokenOnImplicitDefaultURL: when WEBHOOK_URL is left
// unset, send falls back to a hardcoded local default. That default is not
// something the operator configured or vetted, so the REST bridge token
// (which also authorizes /api/* calls like sending messages) must never be
// attached to it — otherwise any other local process that happens to bind
// that port could capture the token simply by being reachable. The token
// must only ever go to a WEBHOOK_URL the operator explicitly set.
func TestSendTextOmitsBridgeTokenOnImplicitDefaultURL(t *testing.T) {
	const token = "test-bridge-token-1234567890abcdef"

	var gotToken string
	var received bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		gotToken = r.Header.Get("X-Bridge-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("WEBHOOK_URL", "") // explicitly unset — exercise the fallback path
	setDefaultURL(t, srv.URL)
	sender := &Sender{Token: token}

	sender.SendText("123@s.whatsapp.net", "hi", "123@s.whatsapp.net", false, "", "", "")

	if !received {
		t.Fatal("webhook was not delivered to the default URL")
	}
	if gotToken != "" {
		t.Fatalf("expected no X-Bridge-Token header on the implicit default URL, got %q", gotToken)
	}
}

// TestSendTextDoesNotFollowRedirects: Go's default http.Client follows
// redirects and forwards custom headers to the redirect target regardless of
// host, unlike Authorization/Cookie which it strips cross-origin. A
// misconfigured or malicious WEBHOOK_URL that responds with a 3xx could
// otherwise cause the bridge to leak X-Bridge-Token to an arbitrary
// third-party host. The webhook client must not follow redirects at all, so a
// second host is never contacted.
func TestSendTextDoesNotFollowRedirects(t *testing.T) {
	const token = "test-bridge-token-1234567890abcdef"

	var targetHit bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	var redirectHit bool
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectHit = true
		http.Redirect(w, r, target.URL+"/whatsapp/webhook", http.StatusFound)
	}))
	defer redirector.Close()

	t.Setenv("WEBHOOK_URL", redirector.URL)
	sender := &Sender{Token: token}

	sender.SendText("123@s.whatsapp.net", "hi", "123@s.whatsapp.net", false, "", "", "")

	if !redirectHit {
		t.Fatal("expected the configured webhook URL to be hit")
	}
	if targetHit {
		t.Fatal("bridge must not follow redirects to a different host (would leak X-Bridge-Token)")
	}
}
