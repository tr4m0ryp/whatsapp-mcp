// Package wa contains everything that talks to WhatsApp through whatsmeow:
// JID/LID resolution, message content extraction, sending, media download,
// live event handling, history sync, and the connection lifecycle.
package wa

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// ResolveLIDChat resolves a LID-based chat JID to its phone-based equivalent
// so that incoming and outgoing messages are stored under the same chat entry.
// The senderAlt/recipientAlt fields carry the phone JID on live messages;
// for history sync these will be empty and the function falls back to the
// whatsmeow LID store (populated during live message handling).
func ResolveLIDChat(client *whatsmeow.Client, chat, senderAlt, recipientAlt types.JID, isFromMe bool) types.JID {
	if chat.Server != types.HiddenUserServer {
		return chat
	}

	// For incoming DMs the phone JID is in SenderAlt;
	// for outgoing DMs it is in RecipientAlt.
	var alt types.JID
	if !isFromMe && !senderAlt.IsEmpty() && senderAlt.Server == types.DefaultUserServer {
		alt = senderAlt.ToNonAD()
	} else if isFromMe && !recipientAlt.IsEmpty() && recipientAlt.Server == types.DefaultUserServer {
		alt = recipientAlt.ToNonAD()
	}

	if !alt.IsEmpty() {
		fmt.Printf("Resolved LID chat %s -> %s (from message alt)\n", chat, alt)
		return alt
	}

	// Fallback: query the whatsmeow LID-PN mapping store.
	pn, err := client.Store.LIDs.GetPNForLID(context.Background(), chat)
	if err == nil && !pn.IsEmpty() {
		fmt.Printf("Resolved LID chat %s -> %s (from LID store)\n", chat, pn.ToNonAD())
		return pn.ToNonAD()
	}

	fmt.Printf("Warning: could not resolve LID chat %s to phone JID\n", chat)
	return chat
}

// ResolveUserJID resolves a single user JID (sender or participant) to its
// phone-based equivalent. Unlike ResolveLIDChat it takes a single hint alt
// JID (either SenderAlt for the peer in a DM or the user's own phone JID
// for outgoing messages) so it can never accidentally substitute the
// recipient's identity for the sender's. Falls back to the whatsmeow
// LID-PN store, then returns the original JID if no mapping is known.
func ResolveUserJID(client *whatsmeow.Client, j, alt types.JID) types.JID {
	j = j.ToNonAD()
	if j.Server != types.HiddenUserServer {
		return j
	}
	if !alt.IsEmpty() && alt.Server == types.DefaultUserServer {
		return alt.ToNonAD()
	}
	if client != nil && client.Store != nil && client.Store.LIDs != nil {
		if pn, err := client.Store.LIDs.GetPNForLID(context.Background(), j); err == nil && !pn.IsEmpty() {
			return pn.ToNonAD()
		}
	}
	return j
}

// SenderAltForMessage returns the best phone-JID hint for the sender of a
// message: SenderAlt for incoming, the user's own phone JID for outgoing.
// Falls through to EmptyJID if no hint is available, in which case
// ResolveUserJID will fall back to the LID store.
func SenderAltForMessage(client *whatsmeow.Client, info types.MessageInfo) types.JID {
	if info.IsFromMe {
		if client != nil && client.Store != nil && client.Store.ID != nil {
			return client.Store.ID.ToNonAD()
		}
		return types.EmptyJID
	}
	if !info.SenderAlt.IsEmpty() && info.SenderAlt.Server == types.DefaultUserServer {
		return info.SenderAlt.ToNonAD()
	}
	return types.EmptyJID
}
