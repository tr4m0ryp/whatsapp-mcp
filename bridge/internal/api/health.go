package api

import (
	"encoding/json"
	"net/http"
	"time"
)

// handleHealth reports bridge liveness and WhatsApp connection state.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := map[string]interface{}{
		"status":    "ok",
		"connected": s.Client.IsConnected(),
		"timestamp": time.Now().Unix(),
	}
	if !s.Client.IsConnected() {
		status["status"] = "disconnected"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(status)
}
