package wa

import (
	"fmt"
	"time"

	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// HandleHistorySync processes history sync events from WhatsApp.
func (h *Handler) HandleHistorySync(historySync *events.HistorySync) {
	client, messageStore, logger := h.Client, h.Store, h.Log

	// Log every history sync event with its shape. Different sync types
	// carry different payloads; logging type/chunk/progress makes it easy
	// to reason about what arrived from WhatsApp when debugging.
	logger.Infof("Received history sync: type=%s chunk=%d progress=%d conversations=%d",
		historySync.Data.GetSyncType(),
		historySync.Data.GetChunkOrder(),
		historySync.Data.GetProgress(),
		len(historySync.Data.Conversations),
	)

	syncedCount := 0
	for _, conversation := range historySync.Data.Conversations {
		// Parse JID from the conversation
		if conversation.ID == nil {
			continue
		}

		rawChatJID := *conversation.ID

		// Try to parse the JID
		jid, err := types.ParseJID(rawChatJID)
		if err != nil {
			logger.Warnf("Failed to parse JID %s: %v", rawChatJID, err)
			continue
		}

		// Resolve LID-based chats to phone-based JIDs.
		// History sync doesn't carry SenderAlt, so rely on the
		// LID store mapping populated during live message handling.
		resolved := ResolveLIDChat(client, jid, types.EmptyJID, types.EmptyJID, false)
		chatJID := resolved.String()

		// Get appropriate chat name by passing the history sync conversation directly
		name := GetChatName(client, messageStore, resolved, chatJID, conversation, "", logger)

		// Process messages
		messages := conversation.Messages
		if len(messages) == 0 {
			continue
		}

		// Update chat with latest message timestamp
		latestMsg := messages[0]
		if latestMsg == nil || latestMsg.Message == nil {
			continue
		}

		// Get timestamp from message info
		ts := latestMsg.Message.GetMessageTimestamp()
		if ts == 0 {
			continue
		}
		timestamp := time.Unix(int64(ts), 0)

		_ = messageStore.StoreChat(chatJID, name, timestamp)
		if err := messageStore.UpdateChatEphemeralSettings(
			chatJID,
			conversation.GetEphemeralExpiration(),
			conversation.GetEphemeralSettingTimestamp(),
		); err != nil {
			logger.Warnf("Failed to store history sync ephemeral settings for %s: %v", chatJID, err)
		}

		// Store messages
		for _, msg := range messages {
			if msg == nil || msg.Message == nil {
				continue
			}
			if h.storeHistoryMessage(msg, jid, chatJID, timestamp) {
				syncedCount++
			}
		}
	}

	fmt.Printf("History sync complete. Stored %d messages.\n", syncedCount)
}

// storeHistoryMessage persists one history-sync message row. Returns true
// when a row was written.
func (h *Handler) storeHistoryMessage(msg *waHistorySync.HistorySyncMsg, jid types.JID, chatJID string, timestamp time.Time) bool {
	client, messageStore, logger := h.Client, h.Store, h.Log

	// Extract text content
	var content string
	if msg.Message.Message != nil {
		if conv := msg.Message.Message.GetConversation(); conv != "" {
			content = conv
		} else if ext := msg.Message.Message.GetExtendedTextMessage(); ext != nil {
			content = ext.GetText()
		}
	}

	// Extract media info - pass message timestamp + ID for unique filenames
	var mediaType, filename, url string
	var mediaKey, fileSHA256, fileEncSHA256 []byte
	var fileLength uint64

	histMsgID := ""
	if msg.Message != nil && msg.Message.Key != nil && msg.Message.Key.ID != nil {
		histMsgID = *msg.Message.Key.ID
	}

	if msg.Message.Message != nil {
		mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength = ExtractMediaInfo(msg.Message.Message, timestamp, histMsgID)
	}

	// Log the message content for debugging
	logger.Infof("Message content: %v, Media Type: %v", content, mediaType)

	// Skip messages with no content and no media
	if content == "" && mediaType == "" {
		return false
	}

	// Determine sender. History-sync rows do not carry SenderAlt,
	// so any LID-based participant is resolved through the
	// whatsmeow LID store (populated during live message handling).
	var sender string
	isFromMe := false
	if msg.Message.Key != nil {
		if msg.Message.Key.FromMe != nil {
			isFromMe = *msg.Message.Key.FromMe
		}
		var rawSender types.JID
		switch {
		case isFromMe && client.Store.ID != nil:
			rawSender = client.Store.ID.ToNonAD()
		case msg.Message.Key.Participant != nil && *msg.Message.Key.Participant != "":
			if parsed, perr := types.ParseJID(*msg.Message.Key.Participant); perr == nil {
				rawSender = parsed
			} else {
				rawSender = types.JID{User: *msg.Message.Key.Participant}
			}
		default:
			rawSender = jid
		}
		var alt types.JID
		if isFromMe && client.Store.ID != nil {
			alt = client.Store.ID.ToNonAD()
		}
		sender = ResolveUserJID(client, rawSender, alt).User
	} else {
		sender = jid.User
	}

	// Store message
	msgID := ""
	if msg.Message.Key != nil && msg.Message.Key.ID != nil {
		msgID = *msg.Message.Key.ID
	}

	// Get message timestamp
	ts := msg.Message.GetMessageTimestamp()
	if ts == 0 {
		return false
	}
	msgTimestamp := time.Unix(int64(ts), 0)

	err := messageStore.StoreMessage(
		msgID,
		chatJID,
		sender,
		content,
		msgTimestamp,
		isFromMe,
		mediaType,
		filename,
		url,
		mediaKey,
		fileSHA256,
		fileEncSHA256,
		fileLength,
		"", // quoted_message_id: history sync does not carry ContextInfo
	)
	if err != nil {
		logger.Warnf("Failed to store history message: %v", err)
		return false
	}

	// Log successful message storage
	if mediaType != "" {
		logger.Infof("Stored message: [%s] %s -> %s: [%s: %s] %s",
			msgTimestamp.Format("2006-01-02 15:04:05"), sender, chatJID, mediaType, filename, content)
	} else {
		logger.Infof("Stored message: [%s] %s -> %s: %s",
			msgTimestamp.Format("2006-01-02 15:04:05"), sender, chatJID, content)
	}
	return true
}
