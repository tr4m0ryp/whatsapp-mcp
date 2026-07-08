package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"go.mau.fi/whatsmeow/types"
)

// handleTyping toggles the typing (chat presence) indicator for a chat.
func (s *Server) handleTyping(w http.ResponseWriter, r *http.Request) {
	// Only allow POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse the request body
	var req struct {
		Recipient string `json:"recipient"`
		IsTyping  bool   `json:"is_typing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// Validate request
	if req.Recipient == "" {
		http.Error(w, "Recipient is required", http.StatusBadRequest)
		return
	}

	// Create JID for recipient
	var recipientJID types.JID
	var err error

	// Check if recipient is a JID
	if strings.Contains(req.Recipient, "@") {
		recipientJID, err = types.ParseJID(req.Recipient)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": fmt.Sprintf("Error parsing JID: %v", err),
			})
			return
		}
	} else {
		// Create JID from phone number
		recipientJID = types.JID{
			User:   req.Recipient,
			Server: "s.whatsapp.net",
		}
	}

	// Determine the chat presence state
	var state types.ChatPresence
	if req.IsTyping {
		state = types.ChatPresenceComposing
	} else {
		state = types.ChatPresencePaused
	}

	// Send the chat presence update
	err = s.Client.SendChatPresence(context.Background(), recipientJID, state, types.ChatPresenceMediaText)

	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("Failed to send typing indicator: %v", err),
		})
	} else {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Typing indicator set to %v", req.IsTyping),
		})
	}
}
