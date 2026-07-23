package wa_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatsapp-mcp/bridge/internal/testutil"
)

// TestHandleMessage_MediaIsNotDownloadedOnArrival pins the on-demand policy.
//
// The bridge used to fetch every attachment the moment it arrived. A linked
// device that downloads 100% of media with no corresponding view activity is
// an archiving pattern, and a history-sync burst could launch hundreds of
// concurrent CDN fetches. Everything needed to fetch later is persisted, so
// nothing should touch the network here.
func TestHandleMessage_MediaIsNotDownloadedOnArrival(t *testing.T) {
	// DownloadMedia writes into store/<chat>/ relative to the working
	// directory; running from a temp dir makes any download observable as a
	// created directory, and keeps a real one out of the repo.
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()

	srv, webhookCh := captureWebhook(t)
	t.Setenv("WEBHOOK_URL", srv.URL)

	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)

	newHandler(client, ms).HandleMessage(buildImageMessage(phonePN, phonePN, false, ""))

	select {
	case <-webhookCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook call")
	}

	// Give any stray background download a chance to betray itself.
	time.Sleep(100 * time.Millisecond)

	if entries, err := os.ReadDir(filepath.Join(dir, "store")); err == nil && len(entries) > 0 {
		t.Fatalf("media was downloaded on arrival: store/ contains %d entries", len(entries))
	}

	// The row must still carry everything /api/download needs later.
	info, err := ms.GetMediaInfo("test-img-001", phonePN.String())
	if err != nil {
		t.Fatalf("GetMediaInfo: %v", err)
	}
	if info.MediaType != "image" {
		t.Errorf("media_type = %q, want image", info.MediaType)
	}
	if info.URL == "" || len(info.MediaKey) == 0 {
		t.Error("stored row lacks the url/media key needed to fetch on demand")
	}
}

// TestWebhookMediaPayloadCarriesNoBytes verifies the payload describes the
// attachment rather than embedding it — the contract that lets the bridge
// avoid downloading anything up front.
func TestWebhookMediaPayloadCarriesNoBytes(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()

	raw := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		raw <- buf
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("WEBHOOK_URL", srv.URL)

	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)
	newHandler(client, ms).HandleMessage(buildImageMessage(phonePN, phonePN, false, "caption"))

	select {
	case body := <-raw:
		if strings.Contains(string(body), "mediaBase64") {
			t.Errorf("webhook payload still embeds media bytes:\n%s", body)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		// The consumer needs these two to fetch the bytes itself.
		if payload["messageId"] == "" || payload["chatJID"] == "" {
			t.Errorf("payload lacks the identifiers needed to fetch on demand: %v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook call")
	}
}
