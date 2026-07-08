// Package config centralizes environment parsing for the bridge. All values
// are resolved eagerly at startup so an invalid setting fails fast instead of
// surfacing mid-session.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// StoreDir is the working-directory-relative location of all bridge state:
// the whatsmeow session DB, the message archive, downloaded media, and the
// auth token file.
const StoreDir = "store"

// WhatsmeowDBPath is whatsmeow's session/app-state SQLite database.
const WhatsmeowDBPath = StoreDir + "/whatsapp.db"

// MessagesDBPath is the bridge's own message archive.
const MessagesDBPath = StoreDir + "/messages.db"

// DefaultPort is used when WHATSAPP_BRIDGE_PORT is unset.
const DefaultPort = 8080

// EnvBool reads a boolean env var with a default.
// Accepts: 1/true/yes/on and 0/false/no/off (case-insensitive).
func EnvBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// ForwardSelf reports whether messages sent by the user's own account should
// be forwarded to the webhook. Defaults to true; override with
// FORWARD_SELF=false.
func ForwardSelf() bool {
	return EnvBool("FORWARD_SELF", true)
}

// Port resolves the REST API port from WHATSAPP_BRIDGE_PORT. Pure env
// parsing with no dependency on the WhatsApp connection, so callers can fail
// fast on an invalid port before running a QR-pairing flow.
func Port() (int, error) {
	p := os.Getenv("WHATSAPP_BRIDGE_PORT")
	if p == "" {
		return DefaultPort, nil
	}
	v, err := strconv.Atoi(p)
	if err != nil || v < 1 || v > 65535 {
		return 0, fmt.Errorf("invalid WHATSAPP_BRIDGE_PORT=%q, must be 1-65535", p)
	}
	return v, nil
}
