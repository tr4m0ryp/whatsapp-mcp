package store

import "time"

// Call storage methods.
//
// WhatsApp calls arrive as a sequence of events: Offer/OfferNotice → Accept →
// Terminate (or Reject → Terminate). We model each call as a single row keyed
// by (call_id, chat_jid), upserted as events arrive. The `result` column
// tracks the call's final state as the event sequence plays out.
//
// State machine:
//   Offer/OfferNotice → result = "in_progress"
//   Accept            → result = "answered"
//   Reject            → result = "rejected"
//   Terminate         → if result == "in_progress" → "missed"
//                       if result == "answered"    → "ended"
//                       otherwise preserve existing (rejected stays rejected)

// StoreCallOffer inserts a new call row when an offer event arrives. Uses
// INSERT OR IGNORE so duplicate offer events (rare but possible) don't clobber
// a call already in a later lifecycle state.
func (s *MessageStore) StoreCallOffer(callID, chatJID, fromJID string, timestamp time.Time, isFromMe bool, callType string, isGroup bool) error {
	_, err := s.DB.Exec(
		`INSERT OR IGNORE INTO calls
		 (call_id, chat_jid, from_jid, timestamp, is_from_me, call_type, is_group, result)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'in_progress')`,
		callID, chatJID, fromJID, timestamp, isFromMe, callType, isGroup,
	)
	return err
}

// MarkCallAnswered records that the offer was accepted.
func (s *MessageStore) MarkCallAnswered(callID, chatJID string) error {
	_, err := s.DB.Exec(
		`UPDATE calls SET result = 'answered'
		 WHERE call_id = ? AND chat_jid = ? AND result = 'in_progress'`,
		callID, chatJID,
	)
	return err
}

// MarkCallRejected records that the call was explicitly rejected.
func (s *MessageStore) MarkCallRejected(callID, chatJID string) error {
	_, err := s.DB.Exec(
		`UPDATE calls SET result = 'rejected'
		 WHERE call_id = ? AND chat_jid = ? AND result = 'in_progress'`,
		callID, chatJID,
	)
	return err
}

// MarkCallTerminated records the end of a call, computing duration from the
// offer timestamp. Infers final result when the call was still in_progress
// (meaning no accept was seen → the call was missed).
func (s *MessageStore) MarkCallTerminated(callID, chatJID, reason string, endedAt time.Time) error {
	// ROUND before CAST: julianday() arithmetic produces a float and CAST truncates
	// toward zero, so a 90-second call would otherwise record as 89.
	_, err := s.DB.Exec(
		`UPDATE calls SET
			ended_at = ?,
			duration_sec = CAST(ROUND((julianday(?) - julianday(timestamp)) * 86400) AS INTEGER),
			reason = ?,
			result = CASE result
				WHEN 'in_progress' THEN 'missed'
				WHEN 'answered'    THEN 'ended'
				ELSE result
			END
		 WHERE call_id = ? AND chat_jid = ?`,
		endedAt, endedAt, reason, callID, chatJID,
	)
	return err
}
