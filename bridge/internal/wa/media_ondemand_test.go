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

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-mcp/bridge/internal/testutil"
)

// buildDownloadableImageMessage builds an image message carrying every field
// DownloadMedia needs (URL, media key, both hashes, length). The shared
// buildImageMessage fixture deliberately omits them, which would make an
// "it did not download" assertion vacuous.
func buildDownloadableImageMessage(chat, sender types.JID, caption string) *events.Message {
	img := &waProto.ImageMessage{
		URL:           proto.String("https://mmg.whatsapp.net/v/t62.7118-24/fake.enc?ccb=11-4&oh=x&oe=y"),
		DirectPath:    proto.String("/v/t62.7118-24/fake.enc"),
		MediaKey:      []byte("0123456789abcdef0123456789abcdef"),
		FileSHA256:    []byte("sha256-of-plaintext-------------"),
		FileEncSHA256: []byte("sha256-of-ciphertext------------"),
		FileLength:    proto.Uint64(4096),
		Mimetype:      proto.String("image/jpeg"),
	}
	if caption != "" {
		img.Caption = proto.String(caption)
	}
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     chat,
				Sender:   sender,
				IsFromMe: false,
			},
			ID:        "test-img-001",
			Timestamp: time.Now(),
		},
		Message: &waProto.Message{ImageMessage: img},
	}
}

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

	// Full download metadata: under the old behaviour this is exactly the
	// message that triggered a synchronous fetch.
	newHandler(client, ms).HandleMessage(buildDownloadableImageMessage(phonePN, phonePN, ""))

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
	newHandler(client, ms).HandleMessage(buildDownloadableImageMessage(phonePN, phonePN, "caption"))

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
