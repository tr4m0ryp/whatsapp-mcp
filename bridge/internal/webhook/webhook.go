// Package webhook forwards inbound WhatsApp events to an operator-configured
// HTTP endpoint (WEBHOOK_URL).
package webhook

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// maxMediaBase64Bytes is the maximum file size that will be base64-encoded and
// included in a webhook payload. Files larger than this limit are skipped to
// avoid excessive memory use and oversized HTTP requests.
const maxMediaBase64Bytes = 10 * 1024 * 1024 // 10 MB

// client is used for all outbound webhook POSTs. The 30-second timeout
// prevents a slow or unreachable endpoint from blocking message handling
// indefinitely. Redirects are never followed: WEBHOOK_URL is a single
// operator-configured endpoint, not a browsable URL, and following a 3xx
// would forward X-Bridge-Token to whatever host the redirect names — Go only
// strips Authorization/Cookie on cross-origin redirects, not custom headers.
var client = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// DefaultURL is used when WEBHOOK_URL is not set. It is a var (not a const)
// so tests can point it at a local test server. It must never receive the
// bridge token: unlike an operator-configured WEBHOOK_URL, nothing has
// vetted this address, so any other local process that happens to bind this
// port could otherwise capture the token just by being reachable.
var DefaultURL = "http://localhost:8769/whatsapp/webhook"

// Sender posts webhook payloads, authenticated with the shared bridge token.
//
// Token is attached as an "X-Bridge-Token" header to every outbound POST —
// the same token the bridge requires on inbound /api/* requests. When empty
// (no token configured yet) the header is omitted so deployments that
// predate the token rollout keep working. A dedicated header is used
// (rather than Authorization) so it never collides with a receiver's own
// Authorization-based auth — e.g. HTTP Basic auth that net/http derives
// automatically from credentials embedded in WEBHOOK_URL.
type Sender struct {
	Token string
}

// Payload represents the data sent to the webhook.
type Payload struct {
	EventType       string `json:"eventType,omitempty"`
	Sender          string `json:"sender"`
	Content         string `json:"content"`
	ChatJID         string `json:"chatJID"`
	IsFromMe        bool   `json:"isFromMe"`
	QuotedMessageID string `json:"quotedMessageId,omitempty"`
	QuotedSender    string `json:"quotedSender,omitempty"`
	QuotedContent   string `json:"quotedContent,omitempty"`
	// Media fields - populated when the message contains an image attachment
	MessageID     string `json:"messageId,omitempty"`
	MediaType     string `json:"mediaType,omitempty"`
	MimeType      string `json:"mimeType,omitempty"`
	MediaFilename string `json:"mediaFilename,omitempty"`
	MediaBase64   string `json:"mediaBase64,omitempty"`
	// Reaction fields - populated when EventType is "reaction".
	ReactionToMessageID string  `json:"reactionToMessageId,omitempty"`
	ReactionEmoji       *string `json:"reactionEmoji,omitempty"`
	ReactionRemoved     *bool   `json:"reactionRemoved,omitempty"`
}

// send marshals and POSTs a Payload to the configured webhook URL.
func (s *Sender) send(payload Payload) {
	webhookURL := os.Getenv("WEBHOOK_URL")
	explicitlyConfigured := webhookURL != ""
	if !explicitlyConfigured {
		webhookURL = DefaultURL
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("Error marshaling webhook payload: %v\n", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Printf("Error building webhook request: %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// Authenticate to the receiver's fail-closed inbound webhook route with
	// the shared bridge token, via a dedicated header so it can never clobber
	// a receiver's own Authorization-based auth (see Sender doc comment).
	// Only attach it when BOTH a token is configured AND WEBHOOK_URL was
	// explicitly set by the operator — the bridge token also authorizes
	// /api/* calls like sending messages, and the implicit local default is
	// not a destination anyone vetted, so it must never receive it.
	if s.Token != "" && explicitlyConfigured {
		req.Header.Set("X-Bridge-Token", s.Token)
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error sending webhook: %v\n", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == 200 {
		fmt.Printf("✓ Webhook sent for message from %s\n", payload.Sender)
	} else {
		fmt.Printf("⚠ Webhook failed with status %d\n", resp.StatusCode)
	}
}

// SendText sends a text-only message to the webhook endpoint.
func (s *Sender) SendText(sender, content, chatJID string, isFromMe bool, quotedMessageID, quotedSender, quotedContent string) {
	s.send(Payload{
		Sender:          sender,
		Content:         content,
		ChatJID:         chatJID,
		IsFromMe:        isFromMe,
		QuotedMessageID: quotedMessageID,
		QuotedSender:    quotedSender,
		QuotedContent:   quotedContent,
	})
}

// SendWithMedia sends a message carrying an attachment, describing the media
// rather than embedding it.
//
// The payload used to include the file base64-encoded, which required the
// bridge to download every attachment on arrival just in case a consumer
// wanted it. Nothing is downloaded now, so a consumer that wants the bytes
// fetches them with the message ID and chat JID below — through the MCP
// download_media tool, or /api/download directly.
func (s *Sender) SendWithMedia(
	sender, content, chatJID string,
	isFromMe bool,
	quotedMessageID, quotedSender, quotedContent string,
	messageID, mediaType, mediaFilename string,
	fileLength uint64,
) {
	s.send(Payload{
		Sender:          sender,
		Content:         content,
		ChatJID:         chatJID,
		IsFromMe:        isFromMe,
		QuotedMessageID: quotedMessageID,
		QuotedSender:    quotedSender,
		QuotedContent:   quotedContent,
		MessageID:       messageID,
		MediaType:       mediaType,
		MediaFilename:   mediaFilename,
		MediaFileLength: fileLength,
	})
}

// SendReaction sends a typed reaction event to the webhook endpoint.
func (s *Sender) SendReaction(sender, chatJID string, isFromMe bool, messageID, reactionToMessageID, emoji string) {
	removed := emoji == ""
	s.send(Payload{
		EventType:           "reaction",
		Sender:              sender,
		Content:             emoji,
		ChatJID:             chatJID,
		IsFromMe:            isFromMe,
		MessageID:           messageID,
		MediaType:           "reaction",
		ReactionToMessageID: reactionToMessageID,
		ReactionEmoji:       &emoji,
		ReactionRemoved:     &removed,
	})
}
