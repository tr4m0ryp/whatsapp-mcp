package wa

import (
	"fmt"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow/types/events"
)

// defaultStreamReclaimDelay is how long to wait before reclaiming a slot
// another session took, so a client that is merely slow to settle can finish
// first. Overridable per Handler for tests.
const defaultStreamReclaimDelay = 30 * time.Second

// maxStreamReplacements is how many times another WhatsApp Web session may
// take this device's slot before the bridge gives up.
//
// Only one web session can hold the slot at a time. Reclaiming it on every
// StreamReplaced means the bridge and a browser session take turns evicting
// each other indefinitely — and repeated session takeovers are exactly what a
// shared or stolen session looks like from the server's side. Reconnecting a
// couple of times covers the benign case (a stale tab that gets closed);
// beyond that the competing client is not going away and something has to
// yield. It is the bridge.
const maxStreamReplacements = 3

// RegisterEventHandlers wires the Handler into the whatsmeow event stream.
//
// Reconnection is deliberately NOT handled here. whatsmeow owns it: it retries
// with its own backoff and, critically, suppresses that retry for failures the
// server means as final. A second reconnect driver layered on top raced the
// library's and reconnected through rejections it had chosen to honour, which
// is how a temporary block escalates. Transient drops need no code; terminal
// conditions halt the process instead.
func (h *Handler) RegisterEventHandlers() {
	h.Client.AddEventHandler(h.EventHandler())
}

// EventHandler returns the event-routing closure that RegisterEventHandlers
// installs. Exposed so tests can drive events without a live client, and so
// the per-connection StreamReplaced counter has an obvious lifetime: it starts
// fresh with each handler, not with each event.
func (h *Handler) EventHandler() func(interface{}) {
	var streamReplacements atomic.Int32

	return func(evt interface{}) {
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

		case *events.Disconnected:
			// Transient. whatsmeow's own auto-reconnect handles it.
			h.Log.Warnf("⚠️  Disconnected from WhatsApp servers, awaiting automatic reconnect...")

		case *events.LoggedOut:
			// whatsmeow has already deleted the local session by the time this
			// fires. Continuing would take the pairing path on the next
			// connect and emit QR codes nobody is there to scan.
			h.Log.Errorf("❌ Device logged out (reason: %s)", v.Reason)
			h.halt(HaltLoggedOut, fmt.Sprintf("reason=%s on_connect=%v", v.Reason, v.OnConnect))

		case *events.TemporaryBan:
			h.Log.Errorf("❌ Account temporarily banned: code=%s expires_in=%s", v.Code, v.Expire)
			h.halt(HaltTemporaryBan, fmt.Sprintf("code=%s expire=%s", v.Code, v.Expire))

		case *events.ClientOutdated:
			h.Log.Errorf("❌ Client outdated — whatsmeow must be updated before reconnecting")
			h.halt(HaltClientOutdated, "WhatsApp rejected the client version (405)")

		case *events.ConnectFailure:
			// whatsmeow only dispatches this for failures it has decided not
			// to auto-reconnect through; transient 500/503s are handled
			// internally and never reach here.
			h.Log.Errorf("❌ Connection failure: %v (%s)", v.Reason, v.Message)
			h.halt(HaltConnectFailure, fmt.Sprintf("reason=%d/%v message=%q", int(v.Reason), v.Reason, v.Message))

		case *events.StreamReplaced:
			n := streamReplacements.Add(1)
			if n >= maxStreamReplacements {
				h.Log.Errorf("❌ Stream replaced %d times — another session keeps taking the slot", n)
				h.halt(HaltStreamReplaced, fmt.Sprintf("replaced %d times", n))
				return
			}
			// whatsmeow treats a replaced stream as a permanent disconnect and
			// suppresses its own reconnect, so reclaiming the slot is up to us.
			// Off the event goroutine and after a pause, so a competing client
			// that is merely slow to settle gets the chance to.
			delay := h.streamReclaimDelay()
			h.Log.Warnf("⚠️  Stream replaced by another session (%d/%d) — reconnecting in %s", n, maxStreamReplacements, delay)
			go func() {
				time.Sleep(delay)
				if err := h.reconnect(); err != nil {
					h.Log.Errorf("Failed to reclaim stream: %v", err)
				}
			}()

		case *events.StreamError:
			// Logged only. Recovery belongs to whatsmeow; a StreamError that
			// is actually terminal arrives again as ConnectFailure/LoggedOut.
			h.Log.Errorf("❌ Stream error: %v", v.Code)
		}
	}
}

// streamReclaimDelay resolves the configured pause before reclaiming a slot.
func (h *Handler) streamReclaimDelay() time.Duration {
	if h.StreamReclaimDelay > 0 {
		return h.StreamReclaimDelay
	}
	return defaultStreamReclaimDelay
}

// reconnect reclaims the connection, through Reconnect when set. The seam
// exists so tests can observe reclaim attempts without a live socket.
func (h *Handler) reconnect() error {
	if h.Reconnect != nil {
		return h.Reconnect()
	}
	return h.Client.Connect()
}

// halt records a terminal condition when a Halter is configured. Tests and
// callers that construct a bare Handler still work — they simply do not stop.
func (h *Handler) halt(reason HaltReason, detail string) {
	if h.Halter == nil {
		return
	}
	h.Halter.Halt(reason, detail)
}
