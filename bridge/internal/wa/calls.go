package wa

import (
	"go.mau.fi/whatsmeow/types"
)

// CallChatJID resolves the chat JID that a call belongs to. For group calls
// this is the group JID; for 1:1 calls it's the call creator's JID — which
// stays stable across the entire lifecycle (Offer → Accept → Terminate).
//
// meta.From is NOT reliable as the chat key: for Accept events that fire
// when the user picks up on their phone, meta.From is the *accepting*
// device's JID (our own), not the other party's. Using From caused Accept
// UPDATEs to miss the row stored at Offer time, so the state machine fell
// through to "missed" when the user answered elsewhere.
//
// meta.CallCreator is populated from the stanza's call-creator attribute,
// which WhatsApp keeps consistent for every event in the call.
func CallChatJID(meta types.BasicCallMeta) string {
	if !meta.GroupJID.IsEmpty() {
		return meta.GroupJID.String()
	}
	if !meta.CallCreator.IsEmpty() {
		return meta.CallCreator.ToNonAD().String()
	}
	return meta.From.ToNonAD().String()
}

// HandleCallOffer stores a new call row. The isFromMe path is defensive —
// in practice WhatsApp's primary device handles outbound calls without
// notifying linked devices, so events observed here are always inbound and
// isFromMe stays false. We keep the branch anyway in case behavior changes.
func (h *Handler) HandleCallOffer(meta types.BasicCallMeta, callType string, isGroup bool) {
	chatJID := CallChatJID(meta)

	fromJID := ""
	switch {
	case !meta.CallCreator.IsEmpty():
		fromJID = meta.CallCreator.ToNonAD().String()
	case !meta.From.IsEmpty():
		fromJID = meta.From.ToNonAD().String()
	}

	isFromMe := h.Client.Store.ID != nil && fromJID == h.Client.Store.ID.ToNonAD().String()

	if err := h.Store.StoreCallOffer(meta.CallID, chatJID, fromJID, meta.Timestamp, isFromMe, callType, isGroup); err != nil {
		h.Log.Warnf("Failed to store call offer: %v", err)
		return
	}

	kind := "Call"
	if isGroup {
		kind = "Group call"
	}
	direction := "incoming"
	if isFromMe {
		direction = "outgoing"
	}
	h.Log.Infof("%s %s: id=%s type=%s from=%s chat=%s",
		kind, direction, meta.CallID, callType, fromJID, chatJID)
}
