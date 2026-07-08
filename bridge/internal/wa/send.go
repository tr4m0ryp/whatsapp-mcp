package wa

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"whatsapp-mcp/bridge/internal/store"
)

// ResolveRecipientJID parses a phone number or JID string and resolves
// PN -> LID for personal chats before sending.
func ResolveRecipientJID(client *whatsmeow.Client, recipient string) (types.JID, error) {
	var recipientJID types.JID
	var err error

	if strings.Contains(recipient, "@") {
		recipientJID, err = types.ParseJID(recipient)
		if err != nil {
			return types.JID{}, fmt.Errorf("error parsing JID: %v", err)
		}
	} else {
		recipientJID = types.JID{
			User:   recipient,
			Server: "s.whatsapp.net", // For personal chats
		}
	}

	// For personal chats, resolve phone number JID to LID (Linked Identity).
	// WhatsApp is migrating to LID-based addressing; messages sent to the
	// phone JID silently fail for migrated contacts.
	if recipientJID.Server == types.DefaultUserServer {
		ctx := context.Background()
		lid, lidErr := client.Store.LIDs.GetLIDForPN(ctx, recipientJID)
		if lidErr == nil && !lid.IsEmpty() {
			fmt.Printf("Resolved %s -> %s (LID)\n", recipientJID, lid)
			recipientJID = lid
		} else {
			// Cache miss or cache error — ask the WhatsApp server.
			if lidErr != nil {
				fmt.Printf("Warning: LID cache lookup failed for %s: %v, falling back to server\n", recipientJID, lidErr)
			}
			info, infoErr := client.GetUserInfo(ctx, []types.JID{recipientJID})
			if infoErr != nil {
				fmt.Printf("Warning: server LID lookup failed for %s: %v\n", recipientJID, infoErr)
			} else if userInfo, ok := info[recipientJID]; ok && !userInfo.LID.IsEmpty() {
				fmt.Printf("Resolved %s -> %s (LID via server)\n", recipientJID, userInfo.LID)
				recipientJID = userInfo.LID
			}
		}
	}

	return recipientJID, nil
}

// SendMessage sends a WhatsApp message, optionally with media or as a quoted
// reply, and persists the outbound message locally.
func SendMessage(client *whatsmeow.Client, messageStore *store.MessageStore, recipient string, message string, mediaPath string, quotedMsgID string, quotedSenderJID string, quotedContent string) (bool, string) {
	if !client.IsConnected() {
		return false, "Not connected to WhatsApp"
	}

	var settingsLookupJID types.JID
	var err error

	if strings.Contains(recipient, "@") {
		settingsLookupJID, err = types.ParseJID(recipient)
		if err != nil {
			return false, fmt.Sprintf("Error parsing JID: %v", err)
		}
	} else {
		settingsLookupJID = types.JID{
			User:   recipient,
			Server: "s.whatsapp.net", // For personal chats
		}
	}

	// Capture pre-LID-resolution JID for SQLite storage.
	// HandleMessage uses ResolveLIDChat to map LID→phone for incoming events;
	// for outbound we keep the pre-resolution form so the chat stays unified
	// under @s.whatsapp.net (matches what list_chats / list_messages expect).
	storageJID := settingsLookupJID

	recipientJID, err := ResolveRecipientJID(client, recipient)
	if err != nil {
		return false, err.Error()
	}

	msg := &waProto.Message{}

	// Check if we have media to send
	if mediaPath != "" {
		built, buildErr := buildMediaMessage(client, mediaPath, message)
		if buildErr != nil {
			return false, capitalizeError(buildErr)
		}
		msg = built
	} else if quotedMsgID != "" {
		// Quoted reply: use ExtendedTextMessage so we can attach ContextInfo.
		// Only text quoting is supported; quoting media messages is not exposed
		// because the quoted preview on the recipient's device requires the
		// original media's key/URL, which is not available to the API caller.
		ctx := &waProto.ContextInfo{
			StanzaID:      proto.String(quotedMsgID),
			Participant:   proto.String(quotedSenderJID),
			QuotedMessage: &waProto.Message{Conversation: proto.String(quotedContent)},
		}
		msg.ExtendedTextMessage = &waProto.ExtendedTextMessage{
			Text:        proto.String(message),
			ContextInfo: ctx,
		}
	} else {
		msg.Conversation = proto.String(message)
	}

	// Normalize @lid recipients to phone JID before the lookup. Chats are
	// persisted under @s.whatsapp.net (HandleMessage normalizes via
	// ResolveLIDChat); without this step, an API caller passing an @lid
	// recipient would silently miss the disappearing-message settings row.
	settings, err := messageStore.GetChatEphemeralSettings(ResolveUserJID(client, settingsLookupJID, types.EmptyJID).String())
	if err != nil && err != sql.ErrNoRows {
		return false, fmt.Sprintf("Error loading chat settings: %v", err)
	}
	if err == nil {
		ApplyChatEphemeralSettings(msg, settings)
	}

	// Send message
	resp, err := client.SendMessage(context.Background(), recipientJID, msg)

	if err != nil {
		return false, fmt.Sprintf("Error sending message: %v", err)
	}

	// whatsmeow does not re-emit events.Message for messages this client
	// itself just sent, so without an explicit StoreMessage call here
	// list_messages / get_last_interaction never see our own outbound
	// traffic until WhatsApp's multi-device sync echoes them back.
	if messageStore != nil && client.Store != nil && client.Store.ID != nil {
		persistOutbound(client, messageStore, storageJID, resp, message, mediaPath, quotedMsgID)
	}

	return true, fmt.Sprintf("Message sent to %s", recipient)
}

// persistOutbound stores the just-sent message and its chat row.
func persistOutbound(client *whatsmeow.Client, messageStore *store.MessageStore, storageJID types.JID, resp whatsmeow.SendResponse, message, mediaPath, quotedMsgID string) {
	// Normalize @lid recipients to phone JID so outbound rows land in
	// the same chat row as inbound (which HandleMessage normalizes via
	// ResolveLIDChat). Otherwise sending to an @lid input would
	// fragment the chat under a separate jid.
	persistJID := ResolveUserJID(client, storageJID, types.EmptyJID)
	chatJID := persistJID.String()
	senderUser := client.Store.ID.User
	timestamp := resp.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	var mediaType, filename string
	if mediaPath != "" {
		filename = filepath.Base(mediaPath)
		_, _, mediaType = classifyMediaPath(mediaPath)
	}

	// Pass empty name so StoreChat preserves any existing resolved
	// contact/group name; we don't have one available here and
	// must not clobber names from inbound handling or history sync.
	if chatErr := messageStore.StoreChat(chatJID, "", timestamp); chatErr != nil {
		fmt.Printf("Warning: failed to store outbound chat metadata: %v\n", chatErr)
	}
	if storeErr := messageStore.StoreMessage(
		resp.ID, chatJID, senderUser, message, timestamp, true,
		mediaType, filename, "", nil, nil, nil, 0, quotedMsgID,
	); storeErr != nil {
		fmt.Printf("Warning: failed to persist outbound message: %v\n", storeErr)
	}
}

// capitalizeError renders an error for API responses that historically
// started with an uppercase letter ("Error reading media file: ...").
func capitalizeError(err error) string {
	s := err.Error()
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
