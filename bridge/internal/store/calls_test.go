package store

import (
	"database/sql"
	"testing"
	"time"
)

// queryCallResult returns the (result, duration_sec, reason) for a call row,
// or empties if no row exists.
func queryCallResult(ms *MessageStore, callID, chatJID string) (result string, duration sql.NullInt64, reason sql.NullString, found bool) {
	err := ms.DB.QueryRow(
		"SELECT result, duration_sec, reason FROM calls WHERE call_id = ? AND chat_jid = ?",
		callID, chatJID,
	).Scan(&result, &duration, &reason)
	return result, duration, reason, err == nil
}

// TestCallStateMachine_AllTransitions exercises every documented transition of
// the call lifecycle state machine and pins down the non-obvious invariants:
//
//   - Offer → Accept → Terminate          ⇒ "ended" (with computed duration)
//   - Offer → Terminate (no Accept)       ⇒ "missed"
//   - Offer → Reject → Terminate          ⇒ "rejected" is preserved
//     (Terminate's CASE branch must NOT downgrade rejected to ended/missed)
//   - Duplicate Offer events do not clobber a call already in a later state
//   - MarkCallAnswered/Rejected only fire when row is still in_progress
func TestCallStateMachine_AllTransitions(t *testing.T) {
	type step struct {
		name string
		do   func(ms *MessageStore) error
	}

	t0 := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	t30 := t0.Add(30 * time.Second)
	t90 := t0.Add(90 * time.Second)

	cases := []struct {
		name         string
		callID       string
		chatJID      string
		steps        []step
		wantResult   string
		wantDuration int64 // 0 = expect NULL
		wantReason   string
	}{
		{
			name:    "Offer→Accept→Terminate yields ended with duration",
			callID:  "call-answered",
			chatJID: "creator@s.whatsapp.net",
			steps: []step{
				{"offer", func(ms *MessageStore) error {
					return ms.StoreCallOffer("call-answered", "creator@s.whatsapp.net", "creator@s.whatsapp.net", t0, false, "voice", false)
				}},
				{"accept", func(ms *MessageStore) error {
					return ms.MarkCallAnswered("call-answered", "creator@s.whatsapp.net")
				}},
				{"terminate", func(ms *MessageStore) error {
					return ms.MarkCallTerminated("call-answered", "creator@s.whatsapp.net", "normal", t90)
				}},
			},
			wantResult:   "ended",
			wantDuration: 90,
			wantReason:   "normal",
		},
		{
			name:    "Offer→Terminate with no Accept yields missed",
			callID:  "call-missed",
			chatJID: "creator@s.whatsapp.net",
			steps: []step{
				{"offer", func(ms *MessageStore) error {
					return ms.StoreCallOffer("call-missed", "creator@s.whatsapp.net", "creator@s.whatsapp.net", t0, false, "voice", false)
				}},
				{"terminate", func(ms *MessageStore) error {
					return ms.MarkCallTerminated("call-missed", "creator@s.whatsapp.net", "timeout", t30)
				}},
			},
			wantResult:   "missed",
			wantDuration: 30,
			wantReason:   "timeout",
		},
		{
			name:    "Offer→Reject→Terminate preserves rejected",
			callID:  "call-rejected",
			chatJID: "creator@s.whatsapp.net",
			steps: []step{
				{"offer", func(ms *MessageStore) error {
					return ms.StoreCallOffer("call-rejected", "creator@s.whatsapp.net", "creator@s.whatsapp.net", t0, false, "voice", false)
				}},
				{"reject", func(ms *MessageStore) error {
					return ms.MarkCallRejected("call-rejected", "creator@s.whatsapp.net")
				}},
				{"terminate", func(ms *MessageStore) error {
					return ms.MarkCallTerminated("call-rejected", "creator@s.whatsapp.net", "rejected_by_user", t30)
				}},
			},
			wantResult:   "rejected",
			wantDuration: 30,
			wantReason:   "rejected_by_user",
		},
		{
			name:    "Duplicate Offer does not clobber later state",
			callID:  "call-dup-offer",
			chatJID: "creator@s.whatsapp.net",
			steps: []step{
				{"offer", func(ms *MessageStore) error {
					return ms.StoreCallOffer("call-dup-offer", "creator@s.whatsapp.net", "creator@s.whatsapp.net", t0, false, "voice", false)
				}},
				{"accept", func(ms *MessageStore) error {
					return ms.MarkCallAnswered("call-dup-offer", "creator@s.whatsapp.net")
				}},
				{"duplicate offer (should be ignored)", func(ms *MessageStore) error {
					return ms.StoreCallOffer("call-dup-offer", "creator@s.whatsapp.net", "creator@s.whatsapp.net", t0, false, "voice", false)
				}},
				{"terminate", func(ms *MessageStore) error {
					return ms.MarkCallTerminated("call-dup-offer", "creator@s.whatsapp.net", "normal", t90)
				}},
			},
			wantResult:   "ended",
			wantDuration: 90,
			wantReason:   "normal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := newTestMessageStore(t)
			for _, s := range tc.steps {
				if err := s.do(ms); err != nil {
					t.Fatalf("step %q failed: %v", s.name, err)
				}
			}

			result, duration, reason, found := queryCallResult(ms, tc.callID, tc.chatJID)
			if !found {
				t.Fatalf("expected row for call_id=%s chat_jid=%s, got none", tc.callID, tc.chatJID)
			}
			if result != tc.wantResult {
				t.Errorf("result: got %q, want %q", result, tc.wantResult)
			}
			if !duration.Valid || duration.Int64 != tc.wantDuration {
				t.Errorf("duration_sec: got %v, want %d", duration, tc.wantDuration)
			}
			if !reason.Valid || reason.String != tc.wantReason {
				t.Errorf("reason: got %v, want %q", reason, tc.wantReason)
			}
		})
	}
}

// TestCallStateMachine_AcceptAndRejectAreNoOpAfterTerminate verifies that
// late-arriving Accept/Reject events (post-Terminate) do not corrupt a
// finalized row. The WHERE result='in_progress' guard is what enforces this.
func TestCallStateMachine_AcceptAndRejectAreNoOpAfterTerminate(t *testing.T) {
	ms := newTestMessageStore(t)
	t0 := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)

	if err := ms.StoreCallOffer("call-late", "creator@s.whatsapp.net", "creator@s.whatsapp.net", t0, false, "voice", false); err != nil {
		t.Fatalf("offer: %v", err)
	}
	if err := ms.MarkCallTerminated("call-late", "creator@s.whatsapp.net", "timeout", t0.Add(30*time.Second)); err != nil {
		t.Fatalf("terminate: %v", err)
	}

	// These should be no-ops because the row is already 'missed', not 'in_progress'.
	_ = ms.MarkCallAnswered("call-late", "creator@s.whatsapp.net")
	_ = ms.MarkCallRejected("call-late", "creator@s.whatsapp.net")

	result, _, _, _ := queryCallResult(ms, "call-late", "creator@s.whatsapp.net")
	if result != "missed" {
		t.Errorf("expected missed to be preserved, got %q", result)
	}
}
