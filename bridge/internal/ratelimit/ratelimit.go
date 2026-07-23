// Package ratelimit meters outbound messages that open new conversations.
//
// The distinction it enforces is between replying and cold-contacting. A reply
// into a chat someone else started is unremarkable at any rate: the recipient
// asked for it. A message to a number that has never written to us is a cold
// contact, and cold contacts are what account bans are actually made of —
// recipients tapping Block or Report on messages they did not ask for, faster
// than any protocol-level signal.
//
// So replies pass through untouched, and cold sends are spaced and capped.
// Neither limit makes cold outreach safe; they make it slow enough that a
// mistake costs one message instead of a hundred.
package ratelimit

import (
	"fmt"
	"sync"
	"time"
)

// Store is the subset of the message archive the limiter needs. Narrow on
// purpose so tests can supply a stub without a database.
type Store interface {
	// HasInboundHistory reports whether the chat contains any message the
	// other side sent.
	HasInboundHistory(chatJID string) (bool, error)
	// CountColdConversationsSince counts chats whose earliest archived
	// message is outbound and falls at or after the given time.
	CountColdConversationsSince(since time.Time) (int, error)
}

// Decision is the outcome of a limit check.
type Decision struct {
	Allowed bool
	// Reason is a human-readable explanation, empty when allowed.
	Reason string
	// RetryAfter is how long the caller should wait, zero when allowed or
	// when waiting will not help (the daily cap resets at midnight, which
	// RetryAfter does express, but a zero cap never clears).
	RetryAfter time.Duration
}

// Limiter meters cold sends. The zero value is not usable; call New.
type Limiter struct {
	store       Store
	minInterval time.Duration
	dailyCap    int

	mu           sync.Mutex
	lastColdSend time.Time
	// now is swappable for tests.
	now func() time.Time
}

// New returns a Limiter enforcing a minimum gap between cold sends and a cap
// on how many new conversations may be started per calendar day.
func New(store Store, minInterval time.Duration, dailyCap int) *Limiter {
	return &Limiter{
		store:       store,
		minInterval: minInterval,
		dailyCap:    dailyCap,
		now:         time.Now,
	}
}

// Check reports whether a message to chatJID may be sent now.
//
// A store error is not treated as permission. If we cannot tell whether a
// recipient is cold, the safe reading is that they are — the failure mode this
// package exists to prevent is sending too much, not too little.
func (l *Limiter) Check(chatJID string) Decision {
	if l == nil {
		return Decision{Allowed: true}
	}

	warm, err := l.store.HasInboundHistory(chatJID)
	if err != nil {
		return Decision{
			Allowed: false,
			Reason:  fmt.Sprintf("cannot determine conversation history for %s: %v", chatJID, err),
		}
	}
	if warm {
		return Decision{Allowed: true}
	}

	now := l.now()

	if l.dailyCap == 0 {
		return Decision{
			Allowed:    false,
			Reason:     "cold sends are disabled (WHATSAPP_COLD_DAILY_CAP=0)",
			RetryAfter: 0,
		}
	}

	started, err := l.store.CountColdConversationsSince(startOfDay(now))
	if err != nil {
		return Decision{
			Allowed: false,
			Reason:  fmt.Sprintf("cannot count today's new conversations: %v", err),
		}
	}
	if started >= l.dailyCap {
		return Decision{
			Allowed: false,
			Reason: fmt.Sprintf("daily cap reached: %d/%d new conversations started today",
				started, l.dailyCap),
			RetryAfter: startOfDay(now).AddDate(0, 0, 1).Sub(now),
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.lastColdSend.IsZero() {
		if elapsed := now.Sub(l.lastColdSend); elapsed < l.minInterval {
			return Decision{
				Allowed: false,
				Reason: fmt.Sprintf("minimum %s between new conversations; %s elapsed",
					l.minInterval, elapsed.Round(time.Second)),
				RetryAfter: l.minInterval - elapsed,
			}
		}
	}

	return Decision{Allowed: true}
}

// RecordCold marks that a cold send just went out, starting the interval
// clock. Called only after a send succeeds, so a failed send does not consume
// the caller's budget.
func (l *Limiter) RecordCold(chatJID string) {
	if l == nil {
		return
	}
	warm, err := l.store.HasInboundHistory(chatJID)
	if err == nil && warm {
		return
	}
	l.mu.Lock()
	l.lastColdSend = l.now()
	l.mu.Unlock()
}

// startOfDay truncates to local midnight. Local rather than UTC because the
// cap is a human-scale budget and should turn over when the operator's day
// does, not at an arbitrary hour.
func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
