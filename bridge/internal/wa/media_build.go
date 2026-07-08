package wa

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"

	"whatsapp-mcp/bridge/internal/ogg"
)

// classifyMediaPath maps a file extension to (whatsmeow upload type, MIME
// type, persist-side category). Single source of truth for the upload path
// (which needs the whatsmeow.MediaType + MIME) and the SQLite persist path
// (which stores the short category string).
func classifyMediaPath(mediaPath string) (whatsmeow.MediaType, string, string) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(mediaPath), "."))
	switch ext {
	case "jpg", "jpeg":
		return whatsmeow.MediaImage, "image/jpeg", "image"
	case "png":
		return whatsmeow.MediaImage, "image/png", "image"
	case "gif":
		return whatsmeow.MediaImage, "image/gif", "image"
	case "webp":
		return whatsmeow.MediaImage, "image/webp", "image"
	case "ogg":
		return whatsmeow.MediaAudio, "audio/ogg; codecs=opus", "audio"
	case "mp4":
		return whatsmeow.MediaVideo, "video/mp4", "video"
	case "avi":
		return whatsmeow.MediaVideo, "video/avi", "video"
	case "mov":
		return whatsmeow.MediaVideo, "video/quicktime", "video"
	default:
		if m := mime.TypeByExtension("." + ext); m != "" {
			return whatsmeow.MediaDocument, m, "document"
		}
		return whatsmeow.MediaDocument, "application/octet-stream", "document"
	}
}

// buildMediaMessage uploads the file at mediaPath and returns a message
// carrying the appropriate media sub-message with `message` as its caption.
func buildMediaMessage(client *whatsmeow.Client, mediaPath, message string) (*waProto.Message, error) {
	mediaData, err := os.ReadFile(mediaPath)
	if err != nil {
		return nil, fmt.Errorf("error reading media file: %v", err)
	}

	mediaType, mimeType, _ := classifyMediaPath(mediaPath)

	resp, err := client.Upload(context.Background(), mediaData, mediaType)
	if err != nil {
		return nil, fmt.Errorf("error uploading media: %v", err)
	}

	fmt.Println("Media uploaded", resp)

	msg := &waProto.Message{}
	switch mediaType {
	case whatsmeow.MediaImage:
		msg.ImageMessage = &waProto.ImageMessage{
			Caption:       proto.String(message),
			Mimetype:      proto.String(mimeType),
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    &resp.FileLength,
		}
	case whatsmeow.MediaAudio:
		// Handle ogg audio files
		var seconds uint32 = 30 // Default fallback
		var waveform []byte = nil

		// Try to analyze the ogg file
		if strings.Contains(mimeType, "ogg") {
			analyzedSeconds, analyzedWaveform, err := ogg.AnalyzeOpus(mediaData)
			if err == nil {
				seconds = analyzedSeconds
				waveform = analyzedWaveform
			} else {
				return nil, fmt.Errorf("failed to analyze Ogg Opus file: %v", err)
			}
		} else {
			fmt.Printf("Not an Ogg Opus file: %s\n", mimeType)
		}

		msg.AudioMessage = &waProto.AudioMessage{
			Mimetype:      proto.String(mimeType),
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    &resp.FileLength,
			Seconds:       proto.Uint32(seconds),
			PTT:           proto.Bool(true),
			Waveform:      waveform,
		}
	case whatsmeow.MediaVideo:
		msg.VideoMessage = &waProto.VideoMessage{
			Caption:       proto.String(message),
			Mimetype:      proto.String(mimeType),
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    &resp.FileLength,
		}
	case whatsmeow.MediaDocument:
		msg.DocumentMessage = &waProto.DocumentMessage{
			Title:         proto.String(filepath.Base(mediaPath)),
			FileName:      proto.String(filepath.Base(mediaPath)),
			Caption:       proto.String(message),
			Mimetype:      proto.String(mimeType),
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    &resp.FileLength,
		}
	}
	return msg, nil
}
