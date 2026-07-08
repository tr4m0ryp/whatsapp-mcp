package wa_test

import (
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"

	"whatsapp-mcp/bridge/internal/wa"
)

func TestExtractTextContent_SurfacesMediaCaptions(t *testing.T) {
	cases := []struct {
		name string
		msg  *waProto.Message
		want string
	}{
		{
			name: "Conversation",
			msg:  &waProto.Message{Conversation: proto.String("hola")},
			want: "hola",
		},
		{
			name: "ExtendedTextMessage",
			msg: &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String("quoted reply")},
			},
			want: "quoted reply",
		},
		{
			name: "ImageMessage with caption",
			msg: &waProto.Message{
				ImageMessage: &waProto.ImageMessage{Caption: proto.String("sunset on the beach")},
			},
			want: "sunset on the beach",
		},
		{
			name: "VideoMessage with caption",
			msg: &waProto.Message{
				VideoMessage: &waProto.VideoMessage{Caption: proto.String("the kids playing")},
			},
			want: "the kids playing",
		},
		{
			name: "DocumentMessage with caption",
			msg: &waProto.Message{
				DocumentMessage: &waProto.DocumentMessage{Caption: proto.String("invoice attached")},
			},
			want: "invoice attached",
		},
		{
			name: "TemplateMessage with hydrated content text",
			msg: &waProto.Message{
				TemplateMessage: &waProto.TemplateMessage{
					HydratedTemplate: &waProto.TemplateMessage_HydratedFourRowTemplate{
						HydratedContentText: proto.String("template body"),
					},
				},
			},
			want: "template body",
		},
		{
			name: "ButtonsMessage with content text",
			msg: &waProto.Message{
				ButtonsMessage: &waProto.ButtonsMessage{ContentText: proto.String("buttons body")},
			},
			want: "buttons body",
		},
		{
			name: "ButtonsMessage with header text",
			msg: &waProto.Message{
				ButtonsMessage: &waProto.ButtonsMessage{
					Header: &waProto.ButtonsMessage_Text{Text: "buttons fallback"},
				},
			},
			want: "buttons fallback",
		},
		{
			name: "InteractiveMessage with body text",
			msg: &waProto.Message{
				InteractiveMessage: &waProto.InteractiveMessage{
					Body: &waProto.InteractiveMessage_Body{Text: proto.String("interactive body")},
				},
			},
			want: "interactive body",
		},
		{
			name: "ListMessage with description",
			msg: &waProto.Message{
				ListMessage: &waProto.ListMessage{Description: proto.String("choose an option")},
			},
			want: "choose an option",
		},
		{
			name: "ButtonsResponseMessage with selected display text",
			msg: &waProto.Message{
				ButtonsResponseMessage: &waProto.ButtonsResponseMessage{
					Response: &waProto.ButtonsResponseMessage_SelectedDisplayText{
						SelectedDisplayText: "selected display",
					},
				},
			},
			want: "selected display",
		},
		{
			name: "TemplateButtonReplyMessage with selected display text",
			msg: &waProto.Message{
				TemplateButtonReplyMessage: &waProto.TemplateButtonReplyMessage{
					SelectedDisplayText: proto.String("template selection"),
				},
			},
			want: "template selection",
		},
		{
			name: "ImageMessage without caption returns empty",
			msg:  &waProto.Message{ImageMessage: &waProto.ImageMessage{}},
			want: "",
		},
		{
			name: "Nil message returns empty",
			msg:  nil,
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wa.ExtractTextContent(tc.msg)
			if got != tc.want {
				t.Errorf("ExtractTextContent() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractMediaInfo_Sticker(t *testing.T) {
	ts := time.Unix(1710000000, 0).UTC()
	msgID := "TEST_STICKER_ID"

	url := "https://mmg.whatsapp.net/v/t62.7161-24/sticker.enc"
	mediaKey := []byte{0x01, 0x02, 0x03}
	sha := []byte{0xaa, 0xbb}
	encSha := []byte{0xcc, 0xdd}
	var length uint64 = 660

	msg := &waProto.Message{
		StickerMessage: &waProto.StickerMessage{
			URL:           proto.String(url),
			MediaKey:      mediaKey,
			FileSHA256:    sha,
			FileEncSHA256: encSha,
			FileLength:    proto.Uint64(length),
		},
	}

	gotType, gotFile, gotURL, gotKey, gotSHA, gotEncSHA, gotLen := wa.ExtractMediaInfo(msg, ts, msgID)

	if gotType != "sticker" {
		t.Errorf("mediaType = %q, want %q", gotType, "sticker")
	}
	wantFile := "sticker_" + ts.Format("20060102_150405") + "_" + msgID + ".webp"
	if gotFile != wantFile {
		t.Errorf("filename = %q, want %q", gotFile, wantFile)
	}
	if gotURL != url {
		t.Errorf("url = %q, want %q", gotURL, url)
	}
	if string(gotKey) != string(mediaKey) {
		t.Errorf("mediaKey = %x, want %x", gotKey, mediaKey)
	}
	if string(gotSHA) != string(sha) {
		t.Errorf("fileSHA256 = %x, want %x", gotSHA, sha)
	}
	if string(gotEncSHA) != string(encSha) {
		t.Errorf("fileEncSHA256 = %x, want %x", gotEncSHA, encSha)
	}
	if gotLen != length {
		t.Errorf("fileLength = %d, want %d", gotLen, length)
	}
}

func TestExtractMediaInfo_NoMediaReturnsEmpty(t *testing.T) {
	msg := &waProto.Message{Conversation: proto.String("plain text, not media")}
	gotType, gotFile, gotURL, gotKey, _, _, gotLen := wa.ExtractMediaInfo(msg, time.Unix(1710000000, 0), "X")
	if gotType != "" || gotFile != "" || gotURL != "" || gotKey != nil || gotLen != 0 {
		t.Errorf("non-media should return empty: type=%q file=%q url=%q keyLen=%d len=%d",
			gotType, gotFile, gotURL, len(gotKey), gotLen)
	}
}

// TestExtractQuotedMessageInfo_ExtendedText verifies the helper that the
// bridge uses to parse quoted-reply ContextInfo from inbound messages.
func TestExtractQuotedMessageInfo_ExtendedText(t *testing.T) {
	stanzaID := "3ATARGET0000001"
	participant := "15551234567@s.whatsapp.net"
	quotedText := "the original message"

	msg := &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String("my reply"),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(stanzaID),
				Participant:   proto.String(participant),
				QuotedMessage: &waProto.Message{Conversation: proto.String(quotedText)},
			},
		},
	}

	gotID, gotSender, gotContent := wa.ExtractQuotedMessageInfo(msg)
	if gotID != stanzaID {
		t.Errorf("stanzaID = %q, want %q", gotID, stanzaID)
	}
	if gotSender != participant {
		t.Errorf("participant = %q, want %q", gotSender, participant)
	}
	if gotContent != quotedText {
		t.Errorf("content = %q, want %q", gotContent, quotedText)
	}
}

// TestExtractQuotedMessageInfo_NoContextInfo verifies graceful handling when
// the message has no ContextInfo (plain Conversation, ReactionMessage, etc.).
func TestExtractQuotedMessageInfo_NoContextInfo(t *testing.T) {
	cases := []struct {
		name string
		msg  *waProto.Message
	}{
		{"plain conversation", &waProto.Message{Conversation: proto.String("hello")}},
		{"nil message", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, sender, content := wa.ExtractQuotedMessageInfo(tc.msg)
			if id != "" || sender != "" || content != "" {
				t.Errorf("expected all empty, got (%q, %q, %q)", id, sender, content)
			}
		})
	}
}
