package auth

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
)

// WithAuth wraps an http.HandlerFunc with bearer-token + Host validation.
// The set of accepted Host values is computed once at server start; we pass
// it in instead of recomputing per request.
func WithAuth(token string, allowedHosts map[string]struct{}, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host, allowedHosts) {
			// 403 — request reached us with a Host we don't recognise.
			// Likely a DNS-rebinding attempt or misconfigured proxy.
			http.Error(w, "Forbidden: host not allowed", http.StatusForbidden)
			return
		}
		if !checkBearerToken(r.Header.Get("Authorization"), token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="whatsapp-bridge"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// hostAllowed performs an exact, case-insensitive match against the
// allow-list. r.Host already includes the port for non-default ports, which
// is exactly what we want — listening on :8080 means "localhost" without a
// port should not match.
func hostAllowed(host string, allowed map[string]struct{}) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	_, ok := allowed[h]
	return ok
}

// checkBearerToken returns true iff the Authorization header carries our
// token. Uses constant-time comparison to avoid timing leaks (the token is
// long enough that this is largely paranoia, but it's free).
func checkBearerToken(authHeader, expected string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return false
	}
	got := strings.TrimSpace(authHeader[len(prefix):])
	if len(got) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

// BuildAllowedHosts returns the static allow-list for a given bind port.
// We accept the three loopback spellings (IPv4, name, IPv6) because the
// MCP server's choice of WHATSAPP_API_URL determines which Host header the
// underlying HTTP client emits.
func BuildAllowedHosts(port int) map[string]struct{} {
	return map[string]struct{}{
		fmt.Sprintf("127.0.0.1:%d", port): {},
		fmt.Sprintf("localhost:%d", port): {},
		fmt.Sprintf("[::1]:%d", port):     {},
	}
}
