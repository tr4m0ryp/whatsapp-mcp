package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"whatsapp-mcp/bridge/internal/media"
	"whatsapp-mcp/bridge/internal/wa"
)

// SendMessageRequest represents the request body for the send message API.
type SendMessageRequest struct {
	Recipient       string `json:"recipient"`
	Message         string `json:"message"`
	MediaPath       string `json:"media_path,omitempty"`
	QuotedMessageID string `json:"quoted_message_id,omitempty"`
	QuotedSenderJID string `json:"quoted_sender_jid,omitempty"`
	QuotedContent   string `json:"quoted_content,omitempty"`
}

// SendMessageResponse represents the response for the send message API.
type SendMessageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// handleSend sends a message (optionally with media or as a quoted reply).
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	// Only allow POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	fmt.Printf("→ /api/send from=%q user_agent=%q\n", r.RemoteAddr, r.UserAgent())

	// Parse the request body
	var req SendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// Validate request
	if req.Recipient == "" {
		http.Error(w, "Recipient is required", http.StatusBadRequest)
		return
	}

	if req.Message == "" && req.MediaPath == "" {
		http.Error(w, "Message or media path is required", http.StatusBadRequest)
		return
	}

	// Validate and canonicalize media_path against the configured roots
	// before reading. This prevents the bridge from being used as a
	// generic file-read primitive (e.g. media_path=/Users/x/.ssh/id_rsa).
	resolvedMediaPath := req.MediaPath
	if req.MediaPath != "" {
		canonical, mpErr := media.ValidatePath(req.MediaPath, s.MediaRoots)
		if mpErr != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(SendMessageResponse{
				Success: false,
				Message: fmt.Sprintf("media_path rejected: %v", mpErr),
			})
			return
		}
		resolvedMediaPath = canonical
	}

	// Avoid logging req.Message verbatim — it's user content and may
	// contain secrets the user pasted into a chat.
	fmt.Printf("→ /api/send recipient=%q message_len=%d has_media=%v\n",
		req.Recipient, len(req.Message), resolvedMediaPath != "")

	// Send the message
	success, message := wa.SendMessage(s.Client, s.Store, req.Recipient, req.Message, resolvedMediaPath, req.QuotedMessageID, req.QuotedSenderJID, req.QuotedContent)
	fmt.Printf("← /api/send success=%v status=%q\n", success, message)
	w.Header().Set("Content-Type", "application/json")

	if !success {
		w.WriteHeader(http.StatusInternalServerError)
	}

	_ = json.NewEncoder(w).Encode(SendMessageResponse{
		Success: success,
		Message: message,
	})
}
