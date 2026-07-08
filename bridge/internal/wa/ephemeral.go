package wa

import (
	waProto "go.mau.fi/whatsmeow/binary/proto"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"whatsapp-mcp/bridge/internal/store"
)

func buildDisappearingMode() *waProto.DisappearingMode {
	return &waProto.DisappearingMode{
		Initiator: waProto.DisappearingMode_CHANGED_IN_CHAT.Enum(),
		Trigger:   waProto.DisappearingMode_CHAT_SETTING.Enum(),
	}
}

func mergeEphemeralContextInfo(existing *waProto.ContextInfo, settings store.ChatEphemeralSettings) *waProto.ContextInfo {
	if existing == nil {
		existing = &waProto.ContextInfo{}
	}
	existing.Expiration = proto.Uint32(settings.Expiration)
	existing.EphemeralSettingTimestamp = proto.Int64(settings.SettingTimestamp)
	existing.DisappearingMode = buildDisappearingMode()
	return existing
}

// ApplyChatEphemeralSettings stamps the chat's disappearing-message timer
// onto an outbound message so it expires like messages sent from the phone.
func ApplyChatEphemeralSettings(msg *waProto.Message, settings store.ChatEphemeralSettings) {
	if msg == nil || settings.Expiration == 0 || settings.SettingTimestamp == 0 {
		return
	}

	switch {
	case msg.ExtendedTextMessage != nil:
		msg.ExtendedTextMessage.ContextInfo = mergeEphemeralContextInfo(msg.ExtendedTextMessage.GetContextInfo(), settings)
	case msg.ImageMessage != nil:
		msg.ImageMessage.ContextInfo = mergeEphemeralContextInfo(msg.ImageMessage.GetContextInfo(), settings)
	case msg.AudioMessage != nil:
		msg.AudioMessage.ContextInfo = mergeEphemeralContextInfo(msg.AudioMessage.GetContextInfo(), settings)
	case msg.VideoMessage != nil:
		msg.VideoMessage.ContextInfo = mergeEphemeralContextInfo(msg.VideoMessage.GetContextInfo(), settings)
	case msg.DocumentMessage != nil:
		msg.DocumentMessage.ContextInfo = mergeEphemeralContextInfo(msg.DocumentMessage.GetContextInfo(), settings)
	case msg.Conversation != nil:
		text := msg.GetConversation()
		msg.Conversation = nil
		msg.ExtendedTextMessage = &waProto.ExtendedTextMessage{
			Text:        proto.String(text),
			ContextInfo: mergeEphemeralContextInfo(nil, settings),
		}
	}
}

// ExtractChatEphemeralFromMessage reads the chat's ephemeral state off an
// inbound message's ContextInfo. Every regular message in an ephemeral chat
// stamps Expiration / EphemeralSettingTimestamp on the sub-message's
// ContextInfo, which lets the bridge backfill chats whose disappearing state
// was set before the bridge ever saw an EPHEMERAL_SETTING toggle or a
// fresh history sync. Returns the zero ChatEphemeralSettings when no
// ContextInfo is present (e.g. plain Conversation, ProtocolMessage).
func ExtractChatEphemeralFromMessage(msg *waProto.Message) store.ChatEphemeralSettings {
	if msg == nil {
		return store.ChatEphemeralSettings{}
	}
	var ctx *waProto.ContextInfo
	switch {
	case msg.ExtendedTextMessage != nil:
		ctx = msg.ExtendedTextMessage.GetContextInfo()
	case msg.ImageMessage != nil:
		ctx = msg.ImageMessage.GetContextInfo()
	case msg.AudioMessage != nil:
		ctx = msg.AudioMessage.GetContextInfo()
	case msg.VideoMessage != nil:
		ctx = msg.VideoMessage.GetContextInfo()
	case msg.DocumentMessage != nil:
		ctx = msg.DocumentMessage.GetContextInfo()
	case msg.StickerMessage != nil:
		ctx = msg.StickerMessage.GetContextInfo()
	}
	if ctx == nil {
		return store.ChatEphemeralSettings{}
	}
	return store.ChatEphemeralSettings{
		Expiration:       ctx.GetExpiration(),
		SettingTimestamp: ctx.GetEphemeralSettingTimestamp(),
	}
}

func updateChatEphemeralSettingsFromProtocolMessage(messageStore *store.MessageStore, chatJID string, msg *waProto.Message, eventTimestamp int64, logger waLog.Logger) {
	if msg == nil || msg.GetProtocolMessage() == nil {
		return
	}

	protoMsg := msg.GetProtocolMessage()
	if protoMsg.GetType() != waProto.ProtocolMessage_EPHEMERAL_SETTING {
		return
	}

	expiration := protoMsg.GetEphemeralExpiration()
	settingTimestamp := protoMsg.GetEphemeralSettingTimestamp()
	// Fall back to the carrier event's timestamp rather than time.Now() so a
	// late-arriving older event doesn't get stamped "newer than" subsequent
	// updates and then block them via the monotonic WHERE clause in
	// UpdateChatEphemeralSettings.
	if settingTimestamp == 0 {
		settingTimestamp = eventTimestamp
	}

	if err := messageStore.UpdateChatEphemeralSettings(chatJID, expiration, settingTimestamp); err != nil {
		logger.Warnf("Failed to update ephemeral settings for %s: %v", chatJID, err)
	}
}
