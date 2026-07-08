package wa

import (
	"time"

	"go.mau.fi/whatsmeow/types/events"
)

// RegisterEventHandlers wires the Handler into the whatsmeow event stream.
// Connection-loss events push into reconnectChan (non-blocking) so the
// reconnect loop in connect.go can react.
func (h *Handler) RegisterEventHandlers(reconnectChan chan<- bool) {
	signalReconnect := func() {
		select {
		case reconnectChan <- true:
		default:
			// Channel already has a reconnect signal
		}
	}

	h.Client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			// Process regular messages
			h.HandleMessage(v)

		case *events.HistorySync:
			// Process history sync events
			h.HandleHistorySync(v)

		case *events.GroupInfo:
			if v.Ephemeral != nil {
				expiration := uint32(0)
				if v.Ephemeral.IsEphemeral {
					expiration = v.Ephemeral.DisappearingTimer
				}
				if err := h.Store.UpdateChatEphemeralSettings(v.JID.String(), expiration, v.Timestamp.Unix()); err != nil {
					h.Log.Warnf("Failed to store group ephemeral settings for %s: %v", v.JID, err)
				}
			}

		case *events.CallOffer:
			// 1:1 incoming call. call_type defaults to "voice"; CallOffer
			// doesn't expose Media directly (it's buried in the binary Data
			// node). Group calls come through CallOfferNotice instead, which
			// DOES expose Media cleanly.
			h.HandleCallOffer(v.BasicCallMeta, "voice", false)

		case *events.CallOfferNotice:
			// Group calls. v.Media is "audio" or "video"; normalize to our
			// "voice"/"video" convention.
			callType := "voice"
			if v.Media == "video" {
				callType = "video"
			}
			isGroup := v.Type == "group" || !v.BasicCallMeta.GroupJID.IsEmpty()
			h.HandleCallOffer(v.BasicCallMeta, callType, isGroup)

		case *events.CallAccept:
			if err := h.Store.MarkCallAnswered(v.CallID, CallChatJID(v.BasicCallMeta)); err != nil {
				h.Log.Warnf("Failed to mark call answered: %v", err)
			} else {
				h.Log.Infof("Call answered: id=%s", v.CallID)
			}

		case *events.CallReject:
			if err := h.Store.MarkCallRejected(v.CallID, CallChatJID(v.BasicCallMeta)); err != nil {
				h.Log.Warnf("Failed to mark call rejected: %v", err)
			} else {
				h.Log.Infof("Call rejected: id=%s", v.CallID)
			}

		case *events.CallTerminate:
			if err := h.Store.MarkCallTerminated(v.CallID, CallChatJID(v.BasicCallMeta), v.Reason, v.Timestamp); err != nil {
				h.Log.Warnf("Failed to mark call terminated: %v", err)
			} else {
				h.Log.Infof("Call terminated: id=%s reason=%q", v.CallID, v.Reason)
			}

		case *events.Connected:
			h.Log.Infof("✓ Successfully connected to WhatsApp servers")

		case *events.LoggedOut:
			h.Log.Warnf("⚠️  Device logged out, please scan QR code to log in again")

		case *events.Disconnected:
			h.Log.Warnf("⚠️  Disconnected from WhatsApp servers, will attempt reconnection...")
			signalReconnect()

		case *events.ConnectFailure:
			h.Log.Errorf("❌ Connection failure: %v", v.Reason)
			signalReconnect()

		case *events.StreamError:
			h.Log.Errorf("❌ Stream error: %v", v.Code)
			signalReconnect()

		case *events.StreamReplaced:
			// Another WhatsApp Web session took our slot. whatsmeow treats this
			// as a "permanent" disconnect and suppresses the Disconnected event,
			// so we must handle it explicitly. Wait briefly to avoid ping-ponging
			// with the other client, then reconnect.
			h.Log.Warnf("⚠️  Stream replaced by another session — will reconnect after 30s")
			go func() {
				time.Sleep(30 * time.Second)
				signalReconnect()
			}()

		case *events.ClientOutdated:
			h.Log.Errorf("❌ Client outdated - please update whatsmeow library")
		}
	})
}
