package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"whatsapp-mcp/bridge/internal/wa"
)

// DownloadMediaRequest represents the request body for the download media API.
type DownloadMediaRequest struct {
	MessageID string `json:"message_id"`
	ChatJID   string `json:"chat_jid"`
}

// DownloadMediaResponse represents the response for the download media API.
type DownloadMediaResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Filename string `json:"filename,omitempty"`
	Path     string `json:"path,omitempty"`
}

// handleDownload downloads media from a stored message.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	// Only allow POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if connected
	if !s.Client.IsConnected() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(DownloadMediaResponse{
			Success: false,
			Message: "WhatsApp client is not connected. Please wait for reconnection.",
		})
		return
	}

	// Parse the request body
	var req DownloadMediaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// Validate request
	if req.MessageID == "" || req.ChatJID == "" {
		http.Error(w, "Message ID and Chat JID are required", http.StatusBadRequest)
		return
	}

	// Log download request for debugging
	fmt.Printf("📥 Download request: message_id=%s chat_jid=%s\n", req.MessageID, req.ChatJID)

	// Download the media
	success, mediaType, filename, path, err := wa.DownloadMedia(s.Client, s.Store, req.MessageID, req.ChatJID)

	w.Header().Set("Content-Type", "application/json")

	// Handle download result
	if !success || err != nil {
		errMsg := "Unknown error"
		if err != nil {
			errMsg = err.Error()
		}

		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(DownloadMediaResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to download media: %s", errMsg),
		})
		return
	}

	// Send successful response
	_ = json.NewEncoder(w).Encode(DownloadMediaResponse{
		Success:  true,
		Message:  fmt.Sprintf("Successfully downloaded %s media", mediaType),
		Filename: filename,
		Path:     path,
	})
}
