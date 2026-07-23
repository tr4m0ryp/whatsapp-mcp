package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"whatsapp-mcp/bridge/internal/api"
	"whatsapp-mcp/bridge/internal/ratelimit"
	"whatsapp-mcp/bridge/internal/store"
	"whatsapp-mcp/bridge/internal/testutil"
)

// newLimitedMux builds a mux whose send path is metered by a limiter over the
// given store, so the cold/warm decision comes from real archive rows.
func newLimitedMux(t *testing.T, ms *store.MessageStore, minInterval time.Duration, dailyCap int) *http.ServeMux {
	t.Helper()
	s := &api.Server{
		Client:      testutil.NewClient(&testutil.MockLIDStore{}),
		Store:       ms,
		Port:        8080,
		Token:       testToken,
		SendLimiter: ratelimit.New(ms, minInterval, dailyCap),
	}
	return s.NewMux()
}

func postSend(t *testing.T, mux *http.ServeMux, recipient string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"recipient":"` + recipient + `","message":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/send", strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)
	return resp
}

func decodeSend(t *testing.T, resp *httptest.ResponseRecorder) api.SendMessageResponse {
	t.Helper()
	var out api.SendMessageResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", resp.Body.String(), err)
	}
	return out
}

func seedInbound(t *testing.T, ms *store.MessageStore, chatJID string) {
	t.Helper()
	if err := ms.StoreMessage(
		"inbound-1", chatJID, "someone", "hello there",
		time.Now().Add(-time.Hour), false,
		"", "", "", nil, nil, nil, 0, "",
	); err != nil {
		t.Fatalf("seed inbound message: %v", err)
	}
}

func TestColdSendRefusedWhenCapIsZero(t *testing.T) {
	ms := testutil.NewMessageStore(t)
	mux := newLimitedMux(t, ms, 30*time.Second, 0)

	resp := postSend(t, mux, "31600000001")
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.Code)
	}
	out := decodeSend(t, resp)
	if out.Success {
		t.Fatal("a refused send must not report success")
	}
	if !strings.Contains(out.Message, "Rate limited") {
		t.Errorf("message = %q, want a rate-limit explanation", out.Message)
	}
}

func TestColdSendRefusedAtDailyCap(t *testing.T) {
	ms := testutil.NewMessageStore(t)
	// One conversation already opened today, with a cap of one.
	if err := ms.StoreMessage(
		"out-1", "31600000009@s.whatsapp.net", "me", "cold hello",
		time.Now(), true, "", "", "", nil, nil, nil, 0, "",
	); err != nil {
		t.Fatalf("seed outbound message: %v", err)
	}
	mux := newLimitedMux(t, ms, time.Second, 1)

	resp := postSend(t, mux, "31600000001")
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.Code)
	}
	out := decodeSend(t, resp)
	if out.RetryAfterSeconds <= 0 {
		t.Errorf("RetryAfterSeconds = %d, want a positive wait until midnight", out.RetryAfterSeconds)
	}
	if resp.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header missing on a 429")
	}
	if !strings.Contains(out.Message, "daily cap") {
		t.Errorf("message = %q, want it to name the daily cap", out.Message)
	}
}

func TestWarmSendIsNotRateLimited(t *testing.T) {
	ms := testutil.NewMessageStore(t)
	// They wrote first, so this is a reply — never metered, even with cold
	// sends switched off entirely.
	seedInbound(t, ms, "31600000002@s.whatsapp.net")
	mux := newLimitedMux(t, ms, time.Hour, 0)

	for i := 0; i < 3; i++ {
		resp := postSend(t, mux, "31600000002")
		if resp.Code == http.StatusTooManyRequests {
			t.Fatalf("reply %d was rate limited: %s", i, resp.Body.String())
		}
	}
}

func TestNilLimiterLeavesSendPathOpen(t *testing.T) {
	// Server without a SendLimiter (the api_test default) must still route.
	mux := newTestMux(t)
	resp := postSend(t, mux, "31600000003")
	if resp.Code == http.StatusTooManyRequests {
		t.Fatalf("unconfigured limiter rate-limited a send: %s", resp.Body.String())
	}
}
