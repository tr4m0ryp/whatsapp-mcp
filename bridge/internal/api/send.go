package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

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
//
// RetryAfterSeconds is set only on a rate-limit refusal. It is surfaced as a
// field rather than just the header so an MCP caller — which sees a JSON body,
// not HTTP metadata — can read how long to wait and say so instead of retrying
// blindly into the same limit.
type SendMessageResponse struct {
	Success           bool `json:"success"`
	Message           string `json:"message"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
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

	// Meter conversations we are starting ourselves. Replies into existing
	// chats are unmetered — the limit exists to slow down cold contact, which
	// is what recipients report as spam, not to throttle ordinary
	// conversation. The chat is resolved the same way wa.SendMessage persists
	// it, so the lookup matches the rows that will be written.
	decision := s.SendLimiter.Check(wa.StorageChatJID(s.Client, req.Recipient))
	if !decision.Allowed {
		retryAfter := int(decision.RetryAfter.Round(time.Second) / time.Second)
		fmt.Printf("← /api/send rate-limited recipient=%q reason=%q retry_after=%ds\n",
			req.Recipient, decision.Reason, retryAfter)
		w.Header().Set("Content-Type", "application/json")
		if retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(SendMessageResponse{
			Success:           false,
			Message:           "Rate limited: " + decision.Reason,
			RetryAfterSeconds: retryAfter,
		})
		return
	}

	// Send the message
	success, message := wa.SendMessage(s.Client, s.Store, req.Recipient, req.Message, resolvedMediaPath, req.QuotedMessageID, req.QuotedSenderJID, req.QuotedContent)
	fmt.Printf("← /api/send success=%v status=%q\n", success, message)

	// Only a delivered cold message consumes the interval budget; a failed
	// send should not lock out the retry.
	if success && decision.Cold {
		s.SendLimiter.RecordCold()
	}

	w.Header().Set("Content-Type", "application/json")

	if !success {
		w.WriteHeader(http.StatusInternalServerError)
	}

	_ = json.NewEncoder(w).Encode(SendMessageResponse{
		Success: success,
		Message: message,
	})
}
