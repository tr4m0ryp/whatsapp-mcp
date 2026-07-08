package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"go.mau.fi/whatsmeow/types"
)

// ReactRequest is the request body for the /api/react endpoint.
type ReactRequest struct {
	Recipient string  `json:"recipient"`  // chat JID
	MessageID string  `json:"message_id"` // ID of the message being reacted to
	FromMe    bool    `json:"from_me"`    // whether the reacted-to message was sent by us
	SenderJID string  `json:"sender_jid"` // full JID of the reacted-to message's sender
	Emoji     *string `json:"emoji"`      // reaction emoji; empty string removes the reaction
}

// handleReact sends (or removes) an emoji reaction.
func (s *Server) handleReact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ReactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Recipient == "" || req.MessageID == "" || req.Emoji == nil {
		http.Error(w, "recipient, message_id, and emoji are required", http.StatusBadRequest)
		return
	}
	chatJID, err := types.ParseJID(req.Recipient)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid recipient JID: %v", err), http.StatusBadRequest)
		return
	}
	var senderJID types.JID
	switch {
	case req.FromMe:
		if s.Client.Store.ID == nil {
			http.Error(w, "Not logged in", http.StatusServiceUnavailable)
			return
		}
		senderJID = *s.Client.Store.ID
	case req.SenderJID != "":
		if senderJID, err = types.ParseJID(req.SenderJID); err != nil {
			http.Error(w, fmt.Sprintf("Invalid sender_jid: %v", err), http.StatusBadRequest)
			return
		}
		if senderJID.User == "" || senderJID.Server == "" {
			http.Error(w, "Invalid sender_jid", http.StatusBadRequest)
			return
		}
	default:
		if chatJID.Server == types.GroupServer {
			http.Error(w, "sender_jid is required for group reactions when from_me is false", http.StatusBadRequest)
			return
		}
		senderJID = chatJID
	}
	msg := s.Client.BuildReaction(chatJID, senderJID, req.MessageID, *req.Emoji)
	w.Header().Set("Content-Type", "application/json")
	if _, err := s.Client.SendMessage(context.Background(), chatJID, msg); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
