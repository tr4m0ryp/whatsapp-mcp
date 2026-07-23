package wa

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"whatsapp-mcp/bridge/internal/store"
	"whatsapp-mcp/bridge/internal/webhook"
)

// Handler processes live WhatsApp events into the message store and the
// webhook. It replaces the package-level globals of a monolithic design so
// tests can construct one with any combination of client, store, webhook
// sender, and forwarding policy.
type Handler struct {
	Client  *whatsmeow.Client
	Store   *store.MessageStore
	Webhook *webhook.Sender
	// ForwardSelf controls whether messages sent by the user's own account
	// are forwarded to the webhook.
	ForwardSelf bool
	Log         waLog.Logger
	// Halter records terminal WhatsApp conditions (ban, logout, outdated
	// client) and signals shutdown. Nil disables halting, which is what tests
	// constructing a bare Handler want.
	Halter *Halter
}

// handleMessageRevoke records a "delete for everyone" event by stamping
// deleted_at on the target message row. The original content is kept on
// purpose so the local archive can still surface what was retracted.
//
// chatJID is the already-LID-normalised chat from the carrier event;
// using it (rather than Key.RemoteJID, which may carry the raw @lid
// form) keeps the UPDATE aligned with how StoreMessage wrote the row.
func handleMessageRevoke(messageStore *store.MessageStore, msg *waProto.Message, chatJID string, eventTimestamp int64, logger waLog.Logger) {
	if msg == nil || msg.GetProtocolMessage() == nil {
		return
	}
	protoMsg := msg.GetProtocolMessage()
	if protoMsg.GetType() != waProto.ProtocolMessage_REVOKE {
		return
	}
	key := protoMsg.GetKey()
	if key == nil {
		return
	}
	targetID := key.GetID()
	if targetID == "" {
		return
	}
	deletedAt := time.Unix(eventTimestamp, 0)
	if err := messageStore.MarkMessageDeleted(targetID, chatJID, deletedAt); err != nil {
		logger.Warnf("Failed to mark message %s in %s as deleted: %v", targetID, chatJID, err)
	}
}

// HandleMessage handles regular incoming messages with media support.
func (h *Handler) HandleMessage(msg *events.Message) {
	client, messageStore, logger := h.Client, h.Store, h.Log

	// Resolve LID-based chats to phone-based JIDs so that incoming
	// and outgoing messages land in the same chat entry.
	resolvedChat := ResolveLIDChat(client, msg.Info.Chat, msg.Info.SenderAlt, msg.Info.RecipientAlt, msg.Info.IsFromMe)
	chatJID := resolvedChat.String()
	// Resolve the *sender* with a sender-specific alt so that outgoing-from-self
	// messages don't get tagged with the recipient's phone number, and incoming
	// messages from LID-only peers get rewritten to their phone user-part when
	// the LID store has a mapping.
	resolvedSender := ResolveUserJID(client, msg.Info.Sender, SenderAltForMessage(client, msg.Info))
	sender := resolvedSender.User

	// Get appropriate chat name (pass resolved JID so contact lookup works)
	name := GetChatName(client, messageStore, resolvedChat, chatJID, nil, sender, true, logger)

	// If contact resolution fails (common for LIDs), PushName is often the best available display name.
	// Only apply for direct messages (not groups) and only when the stored name is the numeric JID user.
	if !msg.Info.IsFromMe && msg.Info.Chat.Server != "g.us" && strings.TrimSpace(msg.Info.PushName) != "" {
		pushName := strings.TrimSpace(msg.Info.PushName)
		if name == "" || name == msg.Info.Chat.User {
			logger.Infof("Updating chat name from PushName for %s: %s -> %s", chatJID, name, pushName)
			name = pushName
		}
	}

	// Update chat in database with the message timestamp (keeps last message time updated)
	err := messageStore.StoreChat(chatJID, name, msg.Info.Timestamp)
	if err != nil {
		logger.Warnf("Failed to store chat: %v", err)
	}

	updateChatEphemeralSettingsFromProtocolMessage(messageStore, chatJID, msg.Message, msg.Info.Timestamp.Unix(), logger)
	handleMessageRevoke(messageStore, msg.Message, chatJID, msg.Info.Timestamp.Unix(), logger)

	// Backfill ephemeral state from any regular message's ContextInfo.
	// EPHEMERAL_SETTING ProtocolMessages and GroupInfo events only fire on
	// changes, so chats whose disappearing timer was set before the bridge
	// started (or before this code shipped) would otherwise stay invisible
	// to outgoing-message logic.
	if backfill := ExtractChatEphemeralFromMessage(msg.Message); backfill.SettingTimestamp != 0 {
		if err := messageStore.UpdateChatEphemeralSettings(chatJID, backfill.Expiration, backfill.SettingTimestamp); err != nil {
			logger.Warnf("Failed to backfill ephemeral settings for %s: %v", chatJID, err)
		}
	}

	// Reactions arrive as their own message stanza rather than message content.
	// Persist them in the messages table as media_type="reaction", with the
	// emoji in `content` and the reacted-to message ID in `filename`, then
	// return — a reaction is not a normal content message. An empty emoji is a
	// valid event meaning "reaction removed"; we store it (so consumers see the
	// removal) rather than dropping it.
	if reaction := msg.Message.GetReactionMessage(); reaction != nil {
		reactedToID := ""
		if key := reaction.GetKey(); key != nil {
			reactedToID = key.GetID()
		}
		if reactedToID != "" {
			emoji := reaction.GetText()
			if err := messageStore.StoreMessage(
				msg.Info.ID, chatJID, sender, emoji,
				msg.Info.Timestamp, msg.Info.IsFromMe,
				"reaction", reactedToID, "", nil, nil, nil, 0, "",
			); err != nil {
				logger.Warnf("Failed to store reaction: %v", err)
			}
			if h.ForwardSelf || !msg.Info.IsFromMe {
				h.Webhook.SendReaction(sender, chatJID, msg.Info.IsFromMe, msg.Info.ID, reactedToID, emoji)
			}
		}
		return
	}

	// Extract text content
	content := ExtractTextContent(msg.Message)

	// Extract media info - pass message timestamp + ID for unique filenames
	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength := ExtractMediaInfo(msg.Message, msg.Info.Timestamp, msg.Info.ID)

	// Extract quoted message info
	quotedMessageID, quotedSender, quotedContent := ExtractQuotedMessageInfo(msg.Message)

	// Skip if there's no content and no media
	if content == "" && mediaType == "" {
		return
	}

	// Store message in database first so that DownloadMedia (which queries the DB
	// by message ID) can find the row when we call it synchronously below.
	err = messageStore.StoreMessage(
		msg.Info.ID,
		chatJID,
		sender,
		content,
		msg.Info.Timestamp,
		msg.Info.IsFromMe,
		mediaType,
		filename,
		url,
		mediaKey,
		fileSHA256,
		fileEncSHA256,
		fileLength,
		quotedMessageID,
	)
	if err != nil {
		logger.Warnf("Failed to store message: %v", err)
	}

	// Media is NOT downloaded here.
	//
	// The bridge used to fetch every attachment the moment it arrived — images
	// synchronously, everything else in an unbounded goroutine. A real linked
	// device downloads lazily, only what the user opens, so pulling 100% of
	// media from every chat is an archiving pattern with no view activity
	// behind it, and a history-sync burst could launch hundreds of concurrent
	// CDN fetches. The row below carries everything needed to fetch on demand
	// (url, media key, both hashes, length), so /api/download can retrieve it
	// whenever a caller actually wants the bytes.

	// Send webhook for incoming messages.
	// Forward self-messages when ForwardSelf is set.
	// Media messages are always forwarded, even without a text caption, so a
	// consumer can decide whether to fetch the attachment.
	shouldForward := h.ForwardSelf || !msg.Info.IsFromMe
	hasText := content != ""
	hasMedia := mediaType != ""

	if shouldForward && (hasText || hasMedia) {
		if hasMedia {
			h.Webhook.SendWithMedia(
				sender, content, chatJID, msg.Info.IsFromMe,
				quotedMessageID, quotedSender, quotedContent,
				msg.Info.ID, mediaType, filename, fileLength,
			)
		} else {
			h.Webhook.SendText(sender, content, chatJID, msg.Info.IsFromMe, quotedMessageID, quotedSender, quotedContent)
		}
	}

	if err == nil {
		// Log message reception
		timestamp := msg.Info.Timestamp.Format("2006-01-02 15:04:05")
		direction := "←"
		if msg.Info.IsFromMe {
			direction = "→"
		}

		// Log based on message type
		if mediaType != "" {
			fmt.Printf("[%s] %s %s: [%s: %s] %s\n", timestamp, direction, sender, mediaType, filename, content)
		} else if content != "" {
			fmt.Printf("[%s] %s %s: %s\n", timestamp, direction, sender, content)
		}
	}
}

// sniffFileMimeType detects a file's MIME type by sniffing the actual bytes
// rather than trusting the generated filename extension (always .jpg).
func sniffFileMimeType(path string) string {
	mimeType := ""
	if f, openErr := os.Open(path); openErr == nil {
		buf := make([]byte, 512)
		if n, readErr := f.Read(buf); readErr == nil || n > 0 {
			mimeType = http.DetectContentType(buf[:n])
		}
		_ = f.Close()
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return mimeType
}
