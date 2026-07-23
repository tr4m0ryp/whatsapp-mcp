// Package api exposes the WhatsApp client over a loopback REST API.
//
// Auth: every handler is wrapped in auth.WithAuth, which enforces both a
// bearer-token check and a Host-header allow-list (loopback only). See the
// auth package for the rationale.
//
// Outbound media: media_path in /api/send is validated against the
// configured media roots before wa.SendMessage ever sees it. See the media
// package.
package api

import (
	"fmt"
	"net/http"
	"time"

	"go.mau.fi/whatsmeow"

	"whatsapp-mcp/bridge/internal/auth"
	"whatsapp-mcp/bridge/internal/store"
)

// Server holds the dependencies shared by all REST handlers.
type Server struct {
	Client     *whatsmeow.Client
	Store      *store.MessageStore
	Port       int
	Token      string
	MediaRoots []string
	// SendLimiter meters messages that open new conversations. A nil limiter
	// allows everything, which keeps tests that only exercise routing simple.
	SendLimiter *ratelimit.Limiter
}

// NewMux builds the authenticated route table.
func (s *Server) NewMux() *http.ServeMux {
	allowedHosts := auth.BuildAllowedHosts(s.Port)
	wrap := func(h http.HandlerFunc) http.HandlerFunc {
		return auth.WithAuth(s.Token, allowedHosts, h)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", wrap(s.handleHealth))
	mux.HandleFunc("/api/send", wrap(s.handleSend))
	mux.HandleFunc("/api/react", wrap(s.handleReact))
	mux.HandleFunc("/api/download", wrap(s.handleDownload))
	mux.HandleFunc("/api/typing", wrap(s.handleTyping))
	return mux
}

// Start runs the REST API server in a goroutine so it doesn't block.
func (s *Server) Start() {
	handler := s.NewMux()

	// Start the server with proper timeouts. Bind to loopback so the bridge is
	// not reachable from the LAN; MCP clients talk to it over localhost.
	serverAddr := fmt.Sprintf("127.0.0.1:%d", s.Port)
	fmt.Printf("Starting REST API server on %s...\n", serverAddr)

	// Create server with timeouts for stability
	server := &http.Server{
		Addr:         serverAddr,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second, // Longer for media downloads
		IdleTimeout:  120 * time.Second,
		Handler:      handler,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("REST API server error: %v\n", err)
		}
	}()
}
