package wa_test

import (
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"

	"whatsapp-mcp/bridge/internal/testutil"
	"whatsapp-mcp/bridge/internal/wa"
)

// newEventHandler builds a Handler wired to a temp-dir Halter, and returns
// the routing closure, the halter, and a counter of reclaim attempts.
func newEventHandler(t *testing.T) (func(interface{}), *wa.Halter, *atomic.Int32) {
	t.Helper()
	halter := wa.NewHalter()
	halter.Dir = t.TempDir()

	var reclaims atomic.Int32
	h := &wa.Handler{
		Client:             testutil.NewClient(&testutil.MockLIDStore{}),
		Store:              testutil.NewMessageStore(t),
		Log:                testutil.Logger(),
		Halter:             halter,
		StreamReclaimDelay: time.Millisecond,
		Reconnect: func() error {
			reclaims.Add(1)
			return nil
		},
	}
	return h.EventHandler(), halter, &reclaims
}

func assertHalted(t *testing.T, h *wa.Halter, want wa.HaltReason) {
	t.Helper()
	select {
	case <-h.Halted():
	case <-time.After(time.Second):
		t.Fatalf("expected halt with reason %q, none occurred", want)
	}
	if got, _ := h.Reason(); got != want {
		t.Fatalf("halt reason = %q, want %q", got, want)
	}
}

func assertNotHalted(t *testing.T, h *wa.Halter) {
	t.Helper()
	select {
	case <-h.Halted():
		reason, detail := h.Reason()
		t.Fatalf("unexpected halt: %s (%s)", reason, detail)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTerminalEventsHalt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event interface{}
		want  wa.HaltReason
	}{
		{"logged out", &events.LoggedOut{Reason: events.ConnectFailureLoggedOut}, wa.HaltLoggedOut},
		{"temporary ban", &events.TemporaryBan{Expire: 6 * time.Hour}, wa.HaltTemporaryBan},
		{"client outdated", &events.ClientOutdated{}, wa.HaltClientOutdated},
		{"connect failure", &events.ConnectFailure{Reason: events.ConnectFailureBadUserAgent}, wa.HaltConnectFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handle, halter, _ := newEventHandler(t)
			handle(tc.event)
			assertHalted(t, halter, tc.want)
		})
	}
}

func TestLoggedOutDoesNotAttemptReconnect(t *testing.T) {
	// The old failure mode: a logged-out session was retried, which took the
	// pairing branch and emitted registration codes nobody could scan.
	handle, halter, reclaims := newEventHandler(t)

	handle(&events.LoggedOut{Reason: events.ConnectFailureLoggedOut})
	assertHalted(t, halter, wa.HaltLoggedOut)

	time.Sleep(20 * time.Millisecond)
	if n := reclaims.Load(); n != 0 {
		t.Fatalf("reconnect attempted %d times after logout, want 0", n)
	}
}

func TestTransientEventsDoNotHalt(t *testing.T) {
	// Reconnection through ordinary drops belongs to whatsmeow. These events
	// must neither stop the bridge nor drive a second reconnect loop.
	for _, tc := range []struct {
		name  string
		event interface{}
	}{
		{"disconnected", &events.Disconnected{}},
		{"stream error", &events.StreamError{Code: "515"}},
		{"connected", &events.Connected{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handle, halter, reclaims := newEventHandler(t)
			handle(tc.event)
			assertNotHalted(t, halter)
			if n := reclaims.Load(); n != 0 {
				t.Fatalf("reconnect attempted %d times for a transient event, want 0", n)
			}
		})
	}
}

func TestStreamReplacedReclaimsThenHalts(t *testing.T) {
	handle, halter, reclaims := newEventHandler(t)

	// The first two takeovers are worth contesting — a stale tab may close.
	handle(&events.StreamReplaced{})
	assertNotHalted(t, halter)
	handle(&events.StreamReplaced{})
	assertNotHalted(t, halter)

	if n := reclaims.Load(); n != 2 {
		t.Fatalf("reclaim attempts = %d, want 2", n)
	}

	// The third says the competing client is not going away.
	handle(&events.StreamReplaced{})
	assertHalted(t, halter, wa.HaltStreamReplaced)

	time.Sleep(20 * time.Millisecond)
	if n := reclaims.Load(); n != 2 {
		t.Fatalf("reclaim attempts = %d after halting, want 2", n)
	}
}
