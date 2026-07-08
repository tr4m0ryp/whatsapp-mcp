package wa

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"

	"whatsapp-mcp/bridge/internal/store"
)

// MediaDownloader implements the whatsmeow.DownloadableMessage interface.
type MediaDownloader struct {
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileLength    uint64
	FileSHA256    []byte
	FileEncSHA256 []byte
	MediaType     whatsmeow.MediaType
}

// GetDirectPath implements the DownloadableMessage interface.
func (d *MediaDownloader) GetDirectPath() string {
	return d.DirectPath
}

// GetURL implements the DownloadableMessage interface.
func (d *MediaDownloader) GetURL() string {
	return d.URL
}

// GetMediaKey implements the DownloadableMessage interface.
func (d *MediaDownloader) GetMediaKey() []byte {
	return d.MediaKey
}

// GetFileLength implements the DownloadableMessage interface.
func (d *MediaDownloader) GetFileLength() uint64 {
	return d.FileLength
}

// GetFileSHA256 implements the DownloadableMessage interface.
func (d *MediaDownloader) GetFileSHA256() []byte {
	return d.FileSHA256
}

// GetFileEncSHA256 implements the DownloadableMessage interface.
func (d *MediaDownloader) GetFileEncSHA256() []byte {
	return d.FileEncSHA256
}

// GetMediaType implements the DownloadableMessage interface.
func (d *MediaDownloader) GetMediaType() whatsmeow.MediaType {
	return d.MediaType
}

// DownloadMedia downloads media from a message into the chat's store
// directory. Returns (success, mediaType, filename, absolutePath, error).
func DownloadMedia(client *whatsmeow.Client, messageStore *store.MessageStore, messageID, chatJID string) (bool, string, string, string, error) {
	// Get media info AND timestamp from the database
	info, err := messageStore.GetMediaInfo(messageID, chatJID)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to find message: %v", err)
	}

	// Check if this is a media message
	if info.MediaType == "" {
		return false, "", "", "", fmt.Errorf("not a media message")
	}

	// Rebuild filename from (timestamp, messageID) — must match ExtractMediaInfo.
	// The message ID disambiguates two messages that arrive in the same second.
	var ext string
	switch info.MediaType {
	case "image":
		ext = ".jpg"
	case "video":
		ext = ".mp4"
	case "audio":
		ext = ".ogg"
	case "sticker":
		ext = ".webp"
	case "document":
		ext = ""
	default:
		ext = ""
	}
	filename := fmt.Sprintf("%s_%s_%s%s", info.MediaType, info.Timestamp.Format("20060102_150405"), messageID, ext)

	// First, check if we already have this file
	chatDir := fmt.Sprintf("store/%s", strings.ReplaceAll(chatJID, ":", "_"))

	// Create directory for the chat if it doesn't exist
	if err := os.MkdirAll(chatDir, 0755); err != nil {
		return false, "", "", "", fmt.Errorf("failed to create chat directory: %v", err)
	}

	// Generate a local path for the file
	localPath := fmt.Sprintf("%s/%s", chatDir, filename)

	// Get absolute path
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to get absolute path: %v", err)
	}

	// Check if file already exists
	if _, err := os.Stat(localPath); err == nil {
		// File exists, return it
		fmt.Printf("📁 File already exists: %s\n", absPath)
		return true, info.MediaType, filename, absPath, nil
	}

	// If we don't have all the media info we need, we can't download
	if info.URL == "" || len(info.MediaKey) == 0 || len(info.FileSHA256) == 0 || len(info.FileEncSHA256) == 0 || info.FileLength == 0 {
		return false, "", "", "", fmt.Errorf("incomplete media information for download")
	}

	fmt.Printf("Attempting to download media for message %s in chat %s...\n", messageID, chatJID)

	// Extract direct path from URL
	directPath := extractDirectPathFromURL(info.URL)

	// Create a downloader that implements DownloadableMessage
	var waMediaType whatsmeow.MediaType
	switch info.MediaType {
	case "image":
		waMediaType = whatsmeow.MediaImage
	case "video":
		waMediaType = whatsmeow.MediaVideo
	case "audio":
		waMediaType = whatsmeow.MediaAudio
	case "document":
		waMediaType = whatsmeow.MediaDocument
	case "sticker":
		// whatsmeow derives sticker decryption keys from the image HKDF info string
		// (see download.go: classToMediaType maps "StickerMessage" -> MediaImage).
		waMediaType = whatsmeow.MediaImage
	default:
		return false, "", "", "", fmt.Errorf("unsupported media type: %s", info.MediaType)
	}

	downloader := &MediaDownloader{
		URL:           info.URL,
		DirectPath:    directPath,
		MediaKey:      info.MediaKey,
		FileLength:    info.FileLength,
		FileSHA256:    info.FileSHA256,
		FileEncSHA256: info.FileEncSHA256,
		MediaType:     waMediaType,
	}

	// Download the media using whatsmeow client
	mediaData, err := client.Download(context.Background(), downloader)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to download media: %v", err)
	}

	// Save the downloaded media to file
	if err := os.WriteFile(localPath, mediaData, 0644); err != nil {
		return false, "", "", "", fmt.Errorf("failed to save media file: %v", err)
	}

	fmt.Printf("Successfully downloaded %s media to %s (%d bytes)\n", info.MediaType, absPath, len(mediaData))
	return true, info.MediaType, filename, absPath, nil
}

// extractDirectPathFromURL extracts the direct path from a WhatsApp media URL.
func extractDirectPathFromURL(url string) string {
	// The direct path is typically in the URL, we need to extract it
	// Example URL: https://mmg.whatsapp.net/v/t62.7118-24/13812002_698058036224062_3424455886509161511_n.enc?ccb=11-4&oh=...

	// Find the path part after the domain
	parts := strings.SplitN(url, ".net/", 2)
	if len(parts) < 2 {
		return url // Return original URL if parsing fails
	}

	// Keep the query string: it carries the CDN auth tokens (oh=/oe=).
	// whatsmeow's Download rebuilds the URL as host + directPath + "&hash=..."
	// and the CDN returns 403 if the auth params are missing.
	return "/" + parts[1]
}
