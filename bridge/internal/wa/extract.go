package wa

import (
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
)

// ExtractTextContent extracts text content from a message.
func ExtractTextContent(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}

	// Try to get text content
	if text := msg.GetConversation(); text != "" {
		return text
	} else if extendedText := msg.GetExtendedTextMessage(); extendedText != nil {
		return extendedText.GetText()
	}

	// Captions on media messages — surface them as searchable content
	// alongside the media itself. Audio messages don't carry captions.
	if img := msg.GetImageMessage(); img != nil {
		return img.GetCaption()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return vid.GetCaption()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		return doc.GetCaption()
	}

	// WhatsApp Business templates arrive hydrated — body lives in
	// HydratedTemplate.HydratedContentText. Without this branch every
	// template-sent message (e.g. WABA notifications) returns "" and the
	// row is silently skipped at the storage gate.
	if tpl := msg.GetTemplateMessage(); tpl != nil {
		if h := tpl.GetHydratedTemplate(); h != nil {
			if t := h.GetHydratedContentText(); t != "" {
				return t
			}
		}
	}
	if btn := msg.GetButtonsMessage(); btn != nil {
		if t := btn.GetContentText(); t != "" {
			return t
		}
		if t := btn.GetText(); t != "" {
			return t
		}
	}
	if ia := msg.GetInteractiveMessage(); ia != nil {
		if body := ia.GetBody(); body != nil {
			if t := body.GetText(); t != "" {
				return t
			}
		}
	}
	if lst := msg.GetListMessage(); lst != nil {
		if t := lst.GetDescription(); t != "" {
			return t
		}
	}
	if br := msg.GetButtonsResponseMessage(); br != nil {
		if t := br.GetSelectedDisplayText(); t != "" {
			return t
		}
	}
	if tbr := msg.GetTemplateButtonReplyMessage(); tbr != nil {
		if t := tbr.GetSelectedDisplayText(); t != "" {
			return t
		}
	}

	return ""
}

// ExtractQuotedMessageInfo extracts quoted message info from ContextInfo.
func ExtractQuotedMessageInfo(msg *waProto.Message) (quotedMessageID string, quotedSender string, quotedContent string) {
	if msg == nil {
		return "", "", ""
	}

	var contextInfo *waProto.ContextInfo

	// Check all message types that can have ContextInfo
	if extText := msg.GetExtendedTextMessage(); extText != nil {
		contextInfo = extText.GetContextInfo()
	} else if img := msg.GetImageMessage(); img != nil {
		contextInfo = img.GetContextInfo()
	} else if vid := msg.GetVideoMessage(); vid != nil {
		contextInfo = vid.GetContextInfo()
	} else if doc := msg.GetDocumentMessage(); doc != nil {
		contextInfo = doc.GetContextInfo()
	} else if aud := msg.GetAudioMessage(); aud != nil {
		contextInfo = aud.GetContextInfo()
	}

	if contextInfo == nil {
		return "", "", ""
	}

	// Extract quoted message ID (StanzaID)
	if contextInfo.StanzaID != nil {
		quotedMessageID = *contextInfo.StanzaID
	}

	// Extract quoted sender (Participant)
	if contextInfo.Participant != nil {
		quotedSender = *contextInfo.Participant
	}

	// Extract quoted message content
	if quotedMsg := contextInfo.QuotedMessage; quotedMsg != nil {
		quotedContent = ExtractTextContent(quotedMsg)
	}

	return quotedMessageID, quotedSender, quotedContent
}

// ExtractMediaInfo extracts media info from a message. Filenames embed the
// message ID so that two messages arriving in the same second do not collide
// on a single file.
func ExtractMediaInfo(msg *waProto.Message, msgTimestamp time.Time, msgID string) (mediaType string, filename string, url string, mediaKey []byte, fileSHA256 []byte, fileEncSHA256 []byte, fileLength uint64) {
	if msg == nil {
		return "", "", "", nil, nil, nil, 0
	}

	// Use message timestamp for filename, fallback to current time if zero
	ts := msgTimestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	tsStr := ts.Format("20060102_150405")
	suffix := tsStr
	if msgID != "" {
		suffix = tsStr + "_" + msgID
	}

	// Check for image message
	if img := msg.GetImageMessage(); img != nil {
		return "image", "image_" + suffix + ".jpg",
			img.GetURL(), img.GetMediaKey(), img.GetFileSHA256(), img.GetFileEncSHA256(), img.GetFileLength()
	}

	// Check for video message
	if vid := msg.GetVideoMessage(); vid != nil {
		return "video", "video_" + suffix + ".mp4",
			vid.GetURL(), vid.GetMediaKey(), vid.GetFileSHA256(), vid.GetFileEncSHA256(), vid.GetFileLength()
	}

	// Check for audio message
	if aud := msg.GetAudioMessage(); aud != nil {
		return "audio", "audio_" + suffix + ".ogg",
			aud.GetURL(), aud.GetMediaKey(), aud.GetFileSHA256(), aud.GetFileEncSHA256(), aud.GetFileLength()
	}

	// Check for document message
	if doc := msg.GetDocumentMessage(); doc != nil {
		filename := doc.GetFileName()
		if filename == "" {
			filename = "document_" + suffix
		}
		return "document", filename,
			doc.GetURL(), doc.GetMediaKey(), doc.GetFileSHA256(), doc.GetFileEncSHA256(), doc.GetFileLength()
	}

	// Sticker message: WebP image, no caption, same URL+MediaKey+SHA shape as other media.
	// On the wire stickers surface as type="media" with an <enc mediatype="sticker"> payload, e.g.:
	//   <message id="..." type="media">
	//     <enc mediatype="sticker" type="msg" v="2"><!-- 660 bytes --></enc>
	//   </message>
	if stk := msg.GetStickerMessage(); stk != nil {
		return "sticker", "sticker_" + suffix + ".webp",
			stk.GetURL(), stk.GetMediaKey(), stk.GetFileSHA256(), stk.GetFileEncSHA256(), stk.GetFileLength()
	}

	return "", "", "", nil, nil, nil, 0
}
