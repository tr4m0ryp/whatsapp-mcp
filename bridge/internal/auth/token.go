// Package auth provides bridge authentication and request validation.
//
// The REST listener binds to 127.0.0.1, but loopback is not a meaningful
// trust boundary on a developer workstation: any local process or browser
// tab (via DNS rebinding) can issue requests. We add two layers:
//
//  1. Bearer-token auth. A 256-bit token is generated at first start and
//     stored at store/.bridge-token (mode 0600). The MCP server reads it
//     either from WHATSAPP_BRIDGE_TOKEN or by reading that file. Every
//     /api/* request must carry "Authorization: Bearer <token>".
//
//  2. Host header allow-list. Even with auth, an attacker who tricks a
//     browser into resolving evil.example.com to 127.0.0.1 (DNS rebinding)
//     could send the loopback request from same-origin context. By
//     restricting Host to {127.0.0.1:<port>, localhost:<port>, [::1]:<port>}
//     we close that hole.
//
// Backwards compatibility: this is a breaking change. Existing deploys must
// either set WHATSAPP_BRIDGE_TOKEN to the printed token in the MCP server
// env, or read the token file. The bridge prints a loud one-time banner at
// startup when it generates a fresh token so users notice.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tokenFileMode is read/write for owner only — never group/other readable.
const tokenFileMode = 0o600

// tokenFilePath is the on-disk location of the bridge auth token, relative
// to the bridge's working directory. The MCP server reads this file as a
// fallback when WHATSAPP_BRIDGE_TOKEN is unset.
const tokenFilePath = "store/.bridge-token"

// tokenByteLen is the entropy size of generated tokens. 32 bytes (256 bits)
// is overkill for an HMAC-quality secret on a single host but trivial to
// generate and leaves zero margin for guessing attacks.
const tokenByteLen = 32

// LoadOrCreateToken returns the persisted token, generating one if the file
// does not exist yet. A WHATSAPP_BRIDGE_TOKEN env var, if set, always wins —
// useful for ephemeral containers where you want to inject the token from
// outside instead of mounting the file.
func LoadOrCreateToken() (token string, freshlyGenerated bool, err error) {
	if env := strings.TrimSpace(os.Getenv("WHATSAPP_BRIDGE_TOKEN")); env != "" {
		if len(env) < 16 {
			return "", false, errors.New("WHATSAPP_BRIDGE_TOKEN is too short (need at least 16 chars)")
		}
		return env, false, nil
	}

	if data, readErr := os.ReadFile(tokenFilePath); readErr == nil {
		existing := strings.TrimSpace(string(data))
		if existing != "" {
			return existing, false, nil
		}
		// File exists but is empty — fall through to regenerate.
	} else if !os.IsNotExist(readErr) {
		return "", false, fmt.Errorf("read %s: %w", tokenFilePath, readErr)
	}

	// Generate a new token.
	buf := make([]byte, tokenByteLen)
	if _, genErr := rand.Read(buf); genErr != nil {
		return "", false, fmt.Errorf("generate bridge token: %w", genErr)
	}
	newToken := hex.EncodeToString(buf)

	// Ensure parent directory exists. main.go already creates store/ before
	// this is called, but being defensive here keeps the helper testable.
	if mkErr := os.MkdirAll(filepath.Dir(tokenFilePath), 0o755); mkErr != nil {
		return "", false, fmt.Errorf("create token dir: %w", mkErr)
	}
	if writeErr := os.WriteFile(tokenFilePath, []byte(newToken+"\n"), tokenFileMode); writeErr != nil {
		return "", false, fmt.Errorf("write %s: %w", tokenFilePath, writeErr)
	}
	return newToken, true, nil
}

// PrintTokenBanner prints a high-visibility startup message when a fresh
// token has been written. This is the user's only chance to copy the token
// without having to cat the file, so it's intentionally noisy.
func PrintTokenBanner(token string, port int) {
	fmt.Println()
	fmt.Println("════════════════════════════════════════════════════════════════════")
	fmt.Println("  WHATSAPP BRIDGE AUTH TOKEN — first-time setup")
	fmt.Println("════════════════════════════════════════════════════════════════════")
	fmt.Printf("  Token:          %s\n", token)
	fmt.Printf("  Stored at:      %s (mode 0600)\n", tokenFilePath)
	fmt.Printf("  Bridge URL:     http://127.0.0.1:%d/api\n", port)
	fmt.Println()
	fmt.Println("  The MCP server must send this token on every request:")
	fmt.Println("    Authorization: Bearer <token>")
	fmt.Println()
	fmt.Println("  Configure the MCP server with one of:")
	fmt.Println("    export WHATSAPP_BRIDGE_TOKEN=<token>")
	fmt.Printf("    (or let it read %s automatically)\n", tokenFilePath)
	fmt.Println("════════════════════════════════════════════════════════════════════")
	fmt.Println()
}
