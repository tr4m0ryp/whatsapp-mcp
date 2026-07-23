// Package config centralizes environment parsing for the bridge. All values
// are resolved eagerly at startup so an invalid setting fails fast instead of
// surfacing mid-session.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
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

// AllowPairing reports whether the bridge may run the QR pairing flow when no
// session is stored. Defaults to false: pairing only works with a human
// present to scan, and an unattended process that pairs on demand ends up
// requesting registration codes in a loop after any logout.
func AllowPairing() bool {
	return EnvBool("WHATSAPP_ALLOW_PAIRING", false)
}

// DeviceName is the name this bridge registers as a linked device. It shows in
// Linked Devices on the phone and is sent in the pairing handshake, where
// whatsmeow's default identifies the library by name. Defaults to the host's
// name, falling back to a generic label when that is unavailable.
func DeviceName() string {
	if v := strings.TrimSpace(os.Getenv("WHATSAPP_DEVICE_NAME")); v != "" {
		return v
	}
	if host, err := os.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host)
	}
	return "WhatsApp Bridge"
}

// EnvDuration reads a duration given in whole seconds, clamped to >= 0.
func EnvDuration(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs < 0 {
		return def
	}
	return time.Duration(secs) * time.Second
}

// EnvInt reads a non-negative integer env var with a default.
func EnvInt(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return def
	}
	return v
}

// ColdMinInterval is the minimum gap between two messages that each open a new
// conversation. Replies into existing chats are not subject to it.
func ColdMinInterval() time.Duration {
	return EnvDuration("WHATSAPP_COLD_MIN_INTERVAL_SEC", 30*time.Second)
}

// ColdDailyCap is the maximum number of new conversations that may be started
// in one calendar day. Zero disables cold sends entirely.
func ColdDailyCap() int {
	return EnvInt("WHATSAPP_COLD_DAILY_CAP", 50)
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
