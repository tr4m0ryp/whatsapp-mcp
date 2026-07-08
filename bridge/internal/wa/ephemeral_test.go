package wa_test

import (
	"testing"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"

	"whatsapp-mcp/bridge/internal/store"
	"whatsapp-mcp/bridge/internal/wa"
)

// TestExtractChatEphemeralFromMessage covers every concrete sub-message type
// that carries ContextInfo. Each regular message in an ephemeral chat stamps
// ContextInfo.Expiration / EphemeralSettingTimestamp; the bridge backfills
// from this so it doesn't depend on receiving a live EPHEMERAL_SETTING toggle
// or a fresh history sync.
func TestExtractChatEphemeralFromMessage(t *testing.T) {
	ctx := &waProto.ContextInfo{
		Expiration:                proto.Uint32(604800),
		EphemeralSettingTimestamp: proto.Int64(1710000000),
	}

	cases := []struct {
		name string
		msg  *waProto.Message
		want store.ChatEphemeralSettings
	}{
		{
			name: "ExtendedTextMessage",
			msg:  &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{ContextInfo: ctx}},
			want: store.ChatEphemeralSettings{Expiration: 604800, SettingTimestamp: 1710000000},
		},
		{
			name: "ImageMessage",
			msg:  &waProto.Message{ImageMessage: &waProto.ImageMessage{ContextInfo: ctx}},
			want: store.ChatEphemeralSettings{Expiration: 604800, SettingTimestamp: 1710000000},
		},
		{
			name: "VideoMessage",
			msg:  &waProto.Message{VideoMessage: &waProto.VideoMessage{ContextInfo: ctx}},
			want: store.ChatEphemeralSettings{Expiration: 604800, SettingTimestamp: 1710000000},
		},
		{
			name: "AudioMessage",
			msg:  &waProto.Message{AudioMessage: &waProto.AudioMessage{ContextInfo: ctx}},
			want: store.ChatEphemeralSettings{Expiration: 604800, SettingTimestamp: 1710000000},
		},
		{
			name: "DocumentMessage",
			msg:  &waProto.Message{DocumentMessage: &waProto.DocumentMessage{ContextInfo: ctx}},
			want: store.ChatEphemeralSettings{Expiration: 604800, SettingTimestamp: 1710000000},
		},
		{
			name: "StickerMessage",
			msg:  &waProto.Message{StickerMessage: &waProto.StickerMessage{ContextInfo: ctx}},
			want: store.ChatEphemeralSettings{Expiration: 604800, SettingTimestamp: 1710000000},
		},
		{
			name: "Conversation (no ContextInfo at all)",
			msg:  &waProto.Message{Conversation: proto.String("plain text")},
			want: store.ChatEphemeralSettings{},
		},
		{
			name: "Nil",
			msg:  nil,
			want: store.ChatEphemeralSettings{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wa.ExtractChatEphemeralFromMessage(tc.msg)
			if got != tc.want {
				t.Errorf("ExtractChatEphemeralFromMessage() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestApplyChatEphemeralSettingsConvertsConversation(t *testing.T) {
	msg := &waProto.Message{
		Conversation: proto.String("hello"),
	}

	wa.ApplyChatEphemeralSettings(msg, store.ChatEphemeralSettings{
		Expiration:       604800,
		SettingTimestamp: 1710000000,
	})

	if msg.Conversation != nil {
		t.Fatalf("expected conversation to be converted to extended text")
	}
	if msg.GetExtendedTextMessage() == nil {
		t.Fatalf("expected extended text message to be set")
	}
	if got := msg.GetExtendedTextMessage().GetText(); got != "hello" {
		t.Fatalf("expected text hello, got %q", got)
	}
	if got := msg.GetExtendedTextMessage().GetContextInfo().GetExpiration(); got != 604800 {
		t.Fatalf("expected expiration 604800, got %d", got)
	}
	if got := msg.GetExtendedTextMessage().GetContextInfo().GetEphemeralSettingTimestamp(); got != 1710000000 {
		t.Fatalf("expected setting timestamp 1710000000, got %d", got)
	}
	if got := msg.GetExtendedTextMessage().GetContextInfo().GetDisappearingMode().GetTrigger(); got != waProto.DisappearingMode_CHAT_SETTING {
		t.Fatalf("expected disappearing mode trigger CHAT_SETTING, got %v", got)
	}
}
