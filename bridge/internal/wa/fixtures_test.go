package wa_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-mcp/bridge/internal/store"
	"whatsapp-mcp/bridge/internal/testutil"
	"whatsapp-mcp/bridge/internal/wa"
	"whatsapp-mcp/bridge/internal/webhook"
)

// --- Test fixtures ---

var (
	phoneLID = types.JID{User: "185366493536339", Server: types.HiddenUserServer}
	phonePN  = types.JID{User: "11234567890", Server: types.DefaultUserServer}

	selfLID   = types.JID{User: "999888777666555", Server: types.HiddenUserServer}
	selfPhone = types.JID{User: "10000000000", Server: types.DefaultUserServer}
)

// newHandler builds a wa.Handler with the original bridge defaults
// (ForwardSelf=true, no webhook token).
func newHandler(client *whatsmeow.Client, ms *store.MessageStore) *wa.Handler {
	return &wa.Handler{
		Client:      client,
		Store:       ms,
		Webhook:     &webhook.Sender{},
		ForwardSelf: true,
		Log:         testutil.Logger(),
	}
}

// buildTextMessage constructs an events.Message with the given source fields.
func buildTextMessage(chat, sender, senderAlt, recipientAlt types.JID, isFromMe bool, text string) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:         chat,
				Sender:       sender,
				SenderAlt:    senderAlt,
				RecipientAlt: recipientAlt,
				IsFromMe:     isFromMe,
				IsGroup:      false,
			},
			ID:        "test-msg-001",
			Timestamp: time.Now(),
		},
		Message: &waProto.Message{
			Conversation: proto.String(text),
		},
	}
}

// buildImageMessage constructs an events.Message that carries an ImageMessage
// with no download metadata (URL/media-key are empty), so HandleMessage will
// classify it as an image but skip the synchronous download attempt.
func buildImageMessage(chat, sender types.JID, isFromMe bool, caption string) *events.Message {
	img := &waProto.ImageMessage{}
	if caption != "" {
		img.Caption = proto.String(caption)
	}
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     chat,
				Sender:   sender,
				IsFromMe: isFromMe,
			},
			ID:        "test-img-001",
			Timestamp: time.Now(),
		},
		Message: &waProto.Message{ImageMessage: img},
	}
}

// buildReactionMessage constructs an events.Message carrying a ReactionMessage
// targeting the given message ID with the given emoji.
func buildReactionMessage(chat, sender types.JID, isFromMe bool, reactedToID, emoji string) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     chat,
				Sender:   sender,
				IsFromMe: isFromMe,
			},
			ID:        "react-" + reactedToID,
			Timestamp: time.Now(),
		},
		Message: &waProto.Message{
			ReactionMessage: &waProto.ReactionMessage{
				Key: &waCommon.MessageKey{
					RemoteJID: proto.String(chat.String()),
					ID:        proto.String(reactedToID),
					FromMe:    proto.Bool(false),
				},
				Text: proto.String(emoji),
			},
		},
	}
}

// revokeEvent builds an inbound events.Message carrying a
// ProtocolMessage_REVOKE that targets the given message ID. Tests use
// this to drive HandleMessage end-to-end rather than reaching into
// internal helpers.
func revokeEvent(targetID string, ts time.Time) *events.Message {
	revokeType := waProto.ProtocolMessage_REVOKE
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     phonePN,
				Sender:   phonePN,
				IsFromMe: false,
			},
			ID:        "carrier-" + targetID,
			Timestamp: ts,
		},
		Message: &waProto.Message{
			ProtocolMessage: &waProto.ProtocolMessage{
				Type: &revokeType,
				Key: &waCommon.MessageKey{
					RemoteJID: proto.String(phonePN.String()),
					ID:        proto.String(targetID),
					FromMe:    proto.Bool(false),
				},
			},
		},
	}
}

// captureWebhook starts a local httptest server that records the first webhook
// payload it receives. It returns the server and a channel that yields the
// decoded payload.
func captureWebhook(t *testing.T) (*httptest.Server, <-chan webhook.Payload) {
	t.Helper()
	ch := make(chan webhook.Payload, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p webhook.Payload
		if err := json.Unmarshal(body, &p); err == nil {
			select {
			case ch <- p:
			default:
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

// captureRawWebhook starts a local httptest server that records the first raw
// JSON webhook payload it receives.
func captureRawWebhook(t *testing.T) (*httptest.Server, <-chan map[string]any) {
	t.Helper()
	ch := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]any
		if err := json.Unmarshal(body, &p); err == nil {
			select {
			case ch <- p:
			default:
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

// readDeletedAt returns the deleted_at value for a message row, with
// ok=false if the row doesn't exist or the column is NULL.
func readDeletedAt(t *testing.T, ms *store.MessageStore, chatJID, messageID string) (time.Time, bool) {
	t.Helper()
	var got sql.NullTime
	if err := ms.DB.QueryRow(
		"SELECT deleted_at FROM messages WHERE id = ? AND chat_jid = ?",
		messageID, chatJID,
	).Scan(&got); err != nil {
		if err == sql.ErrNoRows {
			return time.Time{}, false
		}
		t.Fatalf("read deleted_at: %v", err)
	}
	return got.Time, got.Valid
}

// queryQuotedMessageID returns the quoted_message_id column for a stored
// message row, or (empty, false) if the row does not exist.
func queryQuotedMessageID(ms *store.MessageStore, chatJID, msgID string) (string, bool) {
	var val sql.NullString
	err := ms.DB.QueryRow(
		"SELECT quoted_message_id FROM messages WHERE id = ? AND chat_jid = ?",
		msgID, chatJID,
	).Scan(&val)
	if err != nil {
		return "", false
	}
	return val.String, val.Valid
}

// queryMessageMediaTypeAndFilename returns the (media_type, filename) for a
// stored message, or empty strings if not found.
func queryMessageMediaTypeAndFilename(ms *store.MessageStore, chatJID, msgID string) (mediaType, filename string, found bool) {
	err := ms.DB.QueryRow(
		"SELECT COALESCE(media_type,''), COALESCE(filename,'') FROM messages WHERE id = ? AND chat_jid = ?",
		msgID, chatJID,
	).Scan(&mediaType, &filename)
	return mediaType, filename, err == nil
}
