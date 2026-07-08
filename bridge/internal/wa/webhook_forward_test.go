package wa_test

import (
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-mcp/bridge/internal/testutil"
)

// TestHandleMessage_ImageOnly_WebhookForwarded verifies that an image message
// with no text caption is forwarded to the webhook endpoint (not silently
// dropped), and that the webhook payload contains the expected media fields.
func TestHandleMessage_ImageOnly_WebhookForwarded(t *testing.T) {
	srv, webhookCh := captureWebhook(t)
	t.Setenv("WEBHOOK_URL", srv.URL)

	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)

	msg := buildImageMessage(phonePN, phonePN, false, "") // no caption

	newHandler(client, ms).HandleMessage(msg)

	// The image-only message must be stored.
	if count := testutil.QueryMessageCount(ms, phonePN.String()); count != 1 {
		t.Errorf("expected 1 message stored, got %d", count)
	}

	// The webhook must have been called.
	select {
	case payload := <-webhookCh:
		if payload.MediaType != "image" {
			t.Errorf("expected mediaType=image, got %q", payload.MediaType)
		}
		if payload.MessageID != "test-img-001" {
			t.Errorf("expected messageId=test-img-001, got %q", payload.MessageID)
		}
		if payload.Content != "" {
			t.Errorf("expected empty content for image-only message, got %q", payload.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook call")
	}
}

// TestHandleMessage_ImageWithCaption_WebhookForwarded verifies that an image
// message WITH a text caption is forwarded and that the caption is included in
// the webhook content field (ExtractTextContent surfaces image captions).
func TestHandleMessage_ImageWithCaption_WebhookForwarded(t *testing.T) {
	srv, webhookCh := captureWebhook(t)
	t.Setenv("WEBHOOK_URL", srv.URL)

	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)

	msg := buildImageMessage(phonePN, phonePN, false, "look at this!")

	newHandler(client, ms).HandleMessage(msg)

	select {
	case payload := <-webhookCh:
		if payload.MediaType != "image" {
			t.Errorf("expected mediaType=image, got %q", payload.MediaType)
		}
		if payload.MessageID != "test-img-001" {
			t.Errorf("expected messageId=test-img-001, got %q", payload.MessageID)
		}
		if payload.Content != "look at this!" {
			t.Errorf("expected caption in content, got %q", payload.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook call")
	}
}

// --- Reaction tests ---

// TestHandleMessage_InboundReaction_Stored verifies that an inbound reaction is
// stored as media_type="reaction" with the emoji in content and the
// reacted-to message ID in the filename column.
func TestHandleMessage_InboundReaction_Stored(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)
	chatJID := phonePN.String()
	targetID := "3AABCDEF01234567"
	emoji := "👍"

	msg := buildReactionMessage(phonePN, phonePN, false, targetID, emoji)
	newHandler(client, ms).HandleMessage(msg)

	mediaType, filename, found := queryMessageMediaTypeAndFilename(ms, chatJID, msg.Info.ID)
	if !found {
		t.Fatalf("expected reaction to be stored, but message row not found")
	}
	if mediaType != "reaction" {
		t.Errorf("media_type = %q, want %q", mediaType, "reaction")
	}
	if filename != targetID {
		t.Errorf("filename (reacted-to ID) = %q, want %q", filename, targetID)
	}

	// Verify the emoji is stored in the content column.
	var content string
	if err := ms.DB.QueryRow(
		"SELECT content FROM messages WHERE id = ? AND chat_jid = ?",
		msg.Info.ID, chatJID,
	).Scan(&content); err != nil {
		t.Fatalf("read content: %v", err)
	}
	if content != emoji {
		t.Errorf("content = %q, want emoji %q", content, emoji)
	}
}

// TestHandleMessage_EmptyEmojiReaction_Stored verifies that a reaction with an
// empty emoji (reaction removal) is stored rather than silently dropped.
// Consumers can detect removal by checking content == "".
func TestHandleMessage_EmptyEmojiReaction_Stored(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)
	chatJID := phonePN.String()
	targetID := "3AABCDEF01234568"

	msg := buildReactionMessage(phonePN, phonePN, false, targetID, "" /* empty = removal */)
	newHandler(client, ms).HandleMessage(msg)

	mediaType, filename, found := queryMessageMediaTypeAndFilename(ms, chatJID, msg.Info.ID)
	if !found {
		t.Fatalf("empty-emoji reaction (removal) must be stored, but message row not found")
	}
	if mediaType != "reaction" {
		t.Errorf("media_type = %q, want %q", mediaType, "reaction")
	}
	if filename != targetID {
		t.Errorf("filename = %q, want %q", filename, targetID)
	}
}

// TestHandleMessage_InboundReaction_WebhookForwarded verifies that inbound
// reactions are forwarded as typed webhook events after being stored.
func TestHandleMessage_InboundReaction_WebhookForwarded(t *testing.T) {
	srv, webhookCh := captureRawWebhook(t)
	t.Setenv("WEBHOOK_URL", srv.URL)

	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)
	chatJID := phonePN.String()
	targetID := "3AABCDEF01234569"
	emoji := "👍"

	msg := buildReactionMessage(phonePN, phonePN, false, targetID, emoji)
	newHandler(client, ms).HandleMessage(msg)

	mediaType, filename, found := queryMessageMediaTypeAndFilename(ms, chatJID, msg.Info.ID)
	if !found {
		t.Fatalf("expected reaction to be stored, but message row not found")
	}
	if mediaType != "reaction" {
		t.Errorf("media_type = %q, want %q", mediaType, "reaction")
	}
	if filename != targetID {
		t.Errorf("filename = %q, want %q", filename, targetID)
	}

	select {
	case payload := <-webhookCh:
		if payload["eventType"] != "reaction" {
			t.Errorf("eventType = %v, want reaction", payload["eventType"])
		}
		if payload["mediaType"] != "reaction" {
			t.Errorf("mediaType = %v, want reaction", payload["mediaType"])
		}
		if payload["messageId"] != msg.Info.ID {
			t.Errorf("messageId = %v, want %s", payload["messageId"], msg.Info.ID)
		}
		if payload["reactionToMessageId"] != targetID {
			t.Errorf("reactionToMessageId = %v, want %s", payload["reactionToMessageId"], targetID)
		}
		if payload["reactionEmoji"] != emoji {
			t.Errorf("reactionEmoji = %v, want %s", payload["reactionEmoji"], emoji)
		}
		if payload["reactionRemoved"] != false {
			t.Errorf("reactionRemoved = %v, want false", payload["reactionRemoved"])
		}
		if payload["content"] != emoji {
			t.Errorf("content = %v, want %s", payload["content"], emoji)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reaction webhook call")
	}
}

// TestHandleMessage_EmptyEmojiReaction_WebhookForwarded verifies that reaction
// removals are forwarded even though their content is the empty string.
func TestHandleMessage_EmptyEmojiReaction_WebhookForwarded(t *testing.T) {
	srv, webhookCh := captureRawWebhook(t)
	t.Setenv("WEBHOOK_URL", srv.URL)

	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)
	targetID := "3AABCDEF01234570"

	msg := buildReactionMessage(phonePN, phonePN, false, targetID, "")
	newHandler(client, ms).HandleMessage(msg)

	select {
	case payload := <-webhookCh:
		if payload["eventType"] != "reaction" {
			t.Errorf("eventType = %v, want reaction", payload["eventType"])
		}
		if payload["content"] != "" {
			t.Errorf("content = %v, want empty string", payload["content"])
		}
		if payload["reactionEmoji"] != "" {
			t.Errorf("reactionEmoji = %v, want empty string", payload["reactionEmoji"])
		}
		if payload["reactionRemoved"] != true {
			t.Errorf("reactionRemoved = %v, want true", payload["reactionRemoved"])
		}
		if payload["reactionToMessageId"] != targetID {
			t.Errorf("reactionToMessageId = %v, want %s", payload["reactionToMessageId"], targetID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reaction removal webhook call")
	}
}

// TestHandleMessage_SelfReactionWebhook_RespectsForwardSelf verifies that
// self-authored reactions use the same FORWARD_SELF behavior as normal messages.
func TestHandleMessage_SelfReactionWebhook_RespectsForwardSelf(t *testing.T) {
	srv, webhookCh := captureRawWebhook(t)
	t.Setenv("WEBHOOK_URL", srv.URL)

	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)

	h := newHandler(client, ms)
	h.ForwardSelf = false

	msg := buildReactionMessage(phonePN, phonePN, true, "3AABCDEF01234571", "👍")
	h.HandleMessage(msg)

	if count := testutil.QueryMessageCount(ms, phonePN.String()); count != 1 {
		t.Errorf("expected self reaction to be stored, got %d stored messages", count)
	}

	select {
	case payload := <-webhookCh:
		t.Fatalf("unexpected webhook for self reaction when ForwardSelf=false: %#v", payload)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestHandleMessage_ReactionWithoutKey_NotStored verifies that a reaction
// with no key (no reacted-to message ID) is silently ignored and not stored.
func TestHandleMessage_ReactionWithoutKey_NotStored(t *testing.T) {
	srv, webhookCh := captureRawWebhook(t)
	t.Setenv("WEBHOOK_URL", srv.URL)

	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)

	msg := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     phonePN,
				Sender:   phonePN,
				IsFromMe: false,
			},
			ID:        "react-no-key",
			Timestamp: time.Now(),
		},
		Message: &waProto.Message{
			ReactionMessage: &waProto.ReactionMessage{
				Key:  nil, // no key — no reacted-to ID
				Text: proto.String("👍"),
			},
		},
	}
	newHandler(client, ms).HandleMessage(msg)

	if count := testutil.QueryMessageCount(ms, phonePN.String()); count != 0 {
		t.Errorf("expected reaction without key to be discarded, got %d stored messages", count)
	}

	select {
	case payload := <-webhookCh:
		t.Fatalf("unexpected webhook for reaction without key: %#v", payload)
	case <-time.After(200 * time.Millisecond):
	}
}
