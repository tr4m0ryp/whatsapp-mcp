package ratelimit

import (
	"errors"
	"testing"
	"time"
)

// stubStore answers the two questions the limiter asks, with per-call
// overrides so a test can make either one fail.
type stubStore struct {
	warm      map[string]bool
	coldToday int
	warmErr   error
	countErr  error
}

func (s *stubStore) HasInboundHistory(chatJID string) (bool, error) {
	if s.warmErr != nil {
		return false, s.warmErr
	}
	return s.warm[chatJID], nil
}

func (s *stubStore) CountColdConversationsSince(time.Time) (int, error) {
	if s.countErr != nil {
		return 0, s.countErr
	}
	return s.coldToday, nil
}

// at pins the limiter's clock so interval assertions are exact.
func at(l *Limiter, t time.Time) {
	l.now = func() time.Time { return t }
}

func TestWarmChatIsNeverLimited(t *testing.T) {
	store := &stubStore{warm: map[string]bool{"friend@s.whatsapp.net": true}, coldToday: 999}
	l := New(store, time.Hour, 1)
	base := time.Date(2026, 7, 23, 12, 0, 0, 0, time.Local)
	at(l, base)

	// Far past both limits, but the recipient wrote to us first — replying is
	// the case the limiter must never get in the way of.
	for i := 0; i < 5; i++ {
		if d := l.Check("friend@s.whatsapp.net"); !d.Allowed {
			t.Fatalf("warm send %d refused: %s", i, d.Reason)
		}
	}
}

func TestColdSendRespectsMinInterval(t *testing.T) {
	store := &stubStore{warm: map[string]bool{}}
	l := New(store, 30*time.Second, 50)
	base := time.Date(2026, 7, 23, 12, 0, 0, 0, time.Local)
	at(l, base)

	if d := l.Check("stranger@s.whatsapp.net"); !d.Allowed || !d.Cold {
		t.Fatalf("first cold send should be allowed and cold, got %+v", d)
	}
	l.RecordCold()

	at(l, base.Add(10*time.Second))
	d := l.Check("other@s.whatsapp.net")
	if d.Allowed {
		t.Fatal("second cold send inside the interval should be refused")
	}
	if want := 20 * time.Second; d.RetryAfter != want {
		t.Fatalf("RetryAfter = %s, want %s", d.RetryAfter, want)
	}

	at(l, base.Add(30*time.Second))
	if d := l.Check("other@s.whatsapp.net"); !d.Allowed {
		t.Fatalf("cold send after the interval should be allowed: %s", d.Reason)
	}
}

func TestFailedColdSendDoesNotConsumeInterval(t *testing.T) {
	store := &stubStore{warm: map[string]bool{}}
	l := New(store, 30*time.Second, 50)
	base := time.Date(2026, 7, 23, 12, 0, 0, 0, time.Local)
	at(l, base)

	// Check without RecordCold models a send that was attempted and failed.
	if d := l.Check("stranger@s.whatsapp.net"); !d.Allowed {
		t.Fatal("first cold send should be allowed")
	}
	if d := l.Check("stranger@s.whatsapp.net"); !d.Allowed {
		t.Fatal("a send that never went out must not consume the budget")
	}
}

func TestDailyCapRefusesAndRetriesTomorrow(t *testing.T) {
	store := &stubStore{warm: map[string]bool{}, coldToday: 50}
	l := New(store, time.Second, 50)
	base := time.Date(2026, 7, 23, 22, 0, 0, 0, time.Local)
	at(l, base)

	d := l.Check("stranger@s.whatsapp.net")
	if d.Allowed {
		t.Fatal("cold send at the daily cap should be refused")
	}
	if want := 2 * time.Hour; d.RetryAfter != want {
		t.Fatalf("RetryAfter = %s, want %s (midnight)", d.RetryAfter, want)
	}

	// One under the cap is fine again.
	store.coldToday = 49
	if d := l.Check("stranger@s.whatsapp.net"); !d.Allowed {
		t.Fatalf("cold send under the cap should be allowed: %s", d.Reason)
	}
}

func TestZeroCapDisablesColdSends(t *testing.T) {
	l := New(&stubStore{warm: map[string]bool{}}, time.Second, 0)
	at(l, time.Date(2026, 7, 23, 12, 0, 0, 0, time.Local))

	d := l.Check("stranger@s.whatsapp.net")
	if d.Allowed {
		t.Fatal("cold sends should be disabled when the cap is zero")
	}
	if d.RetryAfter != 0 {
		t.Fatalf("RetryAfter = %s, want 0 — waiting never clears a zero cap", d.RetryAfter)
	}
}

func TestStoreErrorsRefuseRatherThanAllow(t *testing.T) {
	// Not knowing whether a recipient is cold must not read as permission:
	// the failure this package prevents is sending too much.
	t.Run("history lookup", func(t *testing.T) {
		l := New(&stubStore{warmErr: errors.New("db gone")}, time.Second, 50)
		if d := l.Check("stranger@s.whatsapp.net"); d.Allowed {
			t.Fatal("a failed history lookup must refuse the send")
		}
	})
	t.Run("daily count", func(t *testing.T) {
		l := New(&stubStore{warm: map[string]bool{}, countErr: errors.New("db gone")}, time.Second, 50)
		if d := l.Check("stranger@s.whatsapp.net"); d.Allowed {
			t.Fatal("a failed daily count must refuse the send")
		}
	})
}

func TestNilLimiterAllowsEverything(t *testing.T) {
	var l *Limiter
	if d := l.Check("anyone@s.whatsapp.net"); !d.Allowed {
		t.Fatal("a nil limiter should not block")
	}
	l.RecordCold() // must not panic
}
