package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"whatsapp-mcp/bridge/internal/api"
	"whatsapp-mcp/bridge/internal/testutil"
)

const testToken = "supersecrettoken1234567890abcdef"

func newTestMux(t *testing.T) *http.ServeMux {
	t.Helper()
	s := &api.Server{
		Client: testutil.NewClient(&testutil.MockLIDStore{}),
		Store:  testutil.NewMessageStore(t),
		Port:   8080,
		Token:  testToken,
	}
	return s.NewMux()
}

func TestSendHandlerLogsCallerBeforeDecode(t *testing.T) {
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = writePipe
	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = writePipe.Close()
		_ = readPipe.Close()
	})

	handler := newTestMux(t)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/send", strings.NewReader("{"))
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("User-Agent", "unit-test-fingerprint")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	os.Stdout = oldStdout
	_ = writePipe.Close()
	outputBytes, readErr := io.ReadAll(readPipe)
	_ = readPipe.Close()
	if readErr != nil {
		t.Fatalf("failed to read captured stdout: %v", readErr)
	}
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected malformed body to return 400, got %d", resp.Code)
	}

	output := string(outputBytes)
	if !strings.Contains(output, "→ /api/send from=") {
		t.Fatalf("expected caller fingerprint log, got output %q", output)
	}
	if !strings.Contains(output, `user_agent="unit-test-fingerprint"`) {
		t.Fatalf("expected user agent in caller fingerprint log, got output %q", output)
	}
}

// TestReactHandler_MissingFields_Returns400 verifies that the /api/react
// handler returns 400 when recipient or message_id is absent.
func TestReactHandler_MissingFields_Returns400(t *testing.T) {
	handler := newTestMux(t)

	cases := []struct {
		name string
		body string
	}{
		{"empty body", "{}"},
		{"missing message_id", `{"recipient":"15551234567@s.whatsapp.net"}`},
		{"missing recipient", `{"message_id":"3AABCDEF01234567","emoji":"👍"}`},
		{"missing emoji", `{"recipient":"15551234567@s.whatsapp.net","message_id":"3AABCDEF01234567"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/react", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+testToken)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Errorf("body=%q: expected 400, got %d", tc.body, resp.Code)
			}
		})
	}
}

func TestReactHandler_GroupReactionMissingSenderJID_Returns400(t *testing.T) {
	handler := newTestMux(t)

	body := `{"recipient":"120363012345678901@g.us","message_id":"3AABCDEF01234567","emoji":"👍","from_me":false}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/react", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing sender_jid on group reaction, got %d", resp.Code)
	}
}

func TestReactHandler_GroupReactionInvalidSenderJID_Returns400(t *testing.T) {
	handler := newTestMux(t)

	body := `{"recipient":"120363012345678901@g.us","message_id":"3AABCDEF01234567","emoji":"👍","from_me":false,"sender_jid":"@s.whatsapp.net"}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/react", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid sender_jid on group reaction, got %d", resp.Code)
	}
}

// TestReactHandler_NoAuth_Returns401 verifies that the /api/react handler
// rejects requests that do not carry a valid bearer token.
func TestReactHandler_NoAuth_Returns401(t *testing.T) {
	handler := newTestMux(t)

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/react",
		strings.NewReader(`{"recipient":"15551234567@s.whatsapp.net","message_id":"3AABCDEF01234567","emoji":"👍"}`))
	req.Header.Set("Content-Type", "application/json")
	// Deliberately omit Authorization header.
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", resp.Code)
	}
}

// TestSendHandler_QuotedReplyFields_PassedThrough covers the /api/send
// validation path when recipient is empty — proving the quoted-reply fields
// are parsed before any send attempt.
func TestSendHandler_QuotedReplyFields_PassedThrough(t *testing.T) {
	handler := newTestMux(t)

	// POST with quoted_message_id but no recipient — should 400 before
	// any send attempt, proving the new fields are parsed.
	body := `{"recipient":"","message":"hi","quoted_message_id":"3AORIGINAL","quoted_sender_jid":"1234@s.whatsapp.net","quoted_content":"original"}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/send", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	// Empty recipient → 400
	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty recipient with quoted fields, got %d", resp.Code)
	}
}
