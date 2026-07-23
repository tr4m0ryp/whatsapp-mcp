package store

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// newColdStore returns a MessageStore over an in-memory DB with the schema
// the cold-send queries touch. Local to this file so it does not depend on
// testutil, which imports this package.
func newColdStore(t *testing.T) *MessageStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE messages (
			id TEXT,
			chat_jid TEXT,
			sender TEXT,
			content TEXT,
			timestamp TIMESTAMP,
			is_from_me BOOLEAN,
			media_type TEXT,
			filename TEXT,
			url TEXT,
			media_key BLOB,
			file_sha256 BLOB,
			file_enc_sha256 BLOB,
			file_length INTEGER,
			deleted_at TIMESTAMP,
			quoted_message_id TEXT,
			PRIMARY KEY (id, chat_jid)
		);
	`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &MessageStore{DB: db}
}

func insert(t *testing.T, s *MessageStore, id, chatJID string, fromMe bool, ts time.Time) {
	t.Helper()
	if _, err := s.DB.Exec(
		`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
		 VALUES (?, ?, 'x', 'hello', ?, ?)`,
		id, chatJID, ts, fromMe,
	); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func TestHasInboundHistory(t *testing.T) {
	s := newColdStore(t)
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.Local)

	insert(t, s, "m1", "friend@s.whatsapp.net", false, now)
	insert(t, s, "m2", "stranger@s.whatsapp.net", true, now)

	for _, tc := range []struct {
		chat string
		want bool
	}{
		{"friend@s.whatsapp.net", true},   // they wrote to us
		{"stranger@s.whatsapp.net", false}, // only we have written
		{"unknown@s.whatsapp.net", false},  // no rows at all
	} {
		got, err := s.HasInboundHistory(tc.chat)
		if err != nil {
			t.Fatalf("HasInboundHistory(%s): %v", tc.chat, err)
		}
		if got != tc.want {
			t.Errorf("HasInboundHistory(%s) = %v, want %v", tc.chat, got, tc.want)
		}
	}
}

func TestCountColdConversationsSince(t *testing.T) {
	s := newColdStore(t)
	today := time.Date(2026, 7, 23, 9, 0, 0, 0, time.Local)
	midnight := time.Date(2026, 7, 23, 0, 0, 0, 0, time.Local)
	yesterday := today.AddDate(0, 0, -1)

	// Two conversations we opened today.
	insert(t, s, "a1", "coldA@s.whatsapp.net", true, today)
	insert(t, s, "b1", "coldB@s.whatsapp.net", true, today.Add(time.Hour))

	// One we opened today that has since been replied to — still ours, and
	// a reply must not refund the budget it cost to reach them.
	insert(t, s, "c1", "coldC@s.whatsapp.net", true, today)
	insert(t, s, "c2", "coldC@s.whatsapp.net", false, today.Add(time.Minute))

	// They opened this one today: not a cold send of ours.
	insert(t, s, "d1", "inbound@s.whatsapp.net", false, today)
	insert(t, s, "d2", "inbound@s.whatsapp.net", true, today.Add(time.Minute))

	// We opened this one yesterday and are still talking today — the
	// conversation is not new, so it belongs to yesterday's budget.
	insert(t, s, "e1", "older@s.whatsapp.net", true, yesterday)
	insert(t, s, "e2", "older@s.whatsapp.net", true, today)

	got, err := s.CountColdConversationsSince(midnight)
	if err != nil {
		t.Fatalf("CountColdConversationsSince: %v", err)
	}
	if want := 3; got != want {
		t.Fatalf("CountColdConversationsSince = %d, want %d", got, want)
	}
}

func TestCountColdConversationsResetsAcrossDayBoundary(t *testing.T) {
	s := newColdStore(t)
	yesterday := time.Date(2026, 7, 22, 15, 0, 0, 0, time.Local)
	todayMidnight := time.Date(2026, 7, 23, 0, 0, 0, 0, time.Local)

	for _, id := range []string{"x1", "x2", "x3"} {
		insert(t, s, id, id+"@s.whatsapp.net", true, yesterday)
	}

	got, err := s.CountColdConversationsSince(todayMidnight)
	if err != nil {
		t.Fatalf("CountColdConversationsSince: %v", err)
	}
	if got != 0 {
		t.Fatalf("yesterday's cold sends leaked into today: got %d, want 0", got)
	}
}
