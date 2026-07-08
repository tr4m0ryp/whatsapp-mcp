// Package testutil provides shared fakes and fixtures for the bridge's test
// suites: an in-memory LID store, whatsmeow client stubs, and a schema-seeded
// in-memory MessageStore.
package testutil

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"whatsapp-mcp/bridge/internal/store"
)

// MockLIDStore implements wmstore.LIDStore with a simple in-memory map.
type MockLIDStore struct {
	wmstore.NoopStore
	LIDByPN map[types.JID]types.JID
	PNByLID map[types.JID]types.JID
}

// GetLIDForPN returns the mapped LID or EmptyJID.
func (m *MockLIDStore) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	if lid, ok := m.LIDByPN[pn]; ok {
		return lid, nil
	}
	return types.EmptyJID, nil
}

// GetPNForLID returns the mapped phone JID or EmptyJID.
func (m *MockLIDStore) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	if pn, ok := m.PNByLID[lid]; ok {
		return pn, nil
	}
	return types.EmptyJID, nil
}

// NewClient builds a whatsmeow client stub backed by the given LID store.
func NewClient(lidStore wmstore.LIDStore) *whatsmeow.Client {
	noop := &wmstore.NoopStore{}
	return &whatsmeow.Client{
		Store: &wmstore.Device{
			LIDs:     lidStore,
			Contacts: noop,
		},
	}
}

// NewClientWithSelf builds a test client with the user's own phone JID set
// on Store.ID, which the production code uses as the sender-alt hint for
// outgoing messages. Tests that exercise sender resolution for outgoing
// messages must use this constructor.
func NewClientWithSelf(lidStore wmstore.LIDStore, selfPhone types.JID) *whatsmeow.Client {
	c := NewClient(lidStore)
	pn := selfPhone.ToNonAD()
	c.Store.ID = &pn
	return c
}

// NewMessageStore returns an in-memory MessageStore with the full schema.
func NewMessageStore(t *testing.T) *store.MessageStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE chats (
			jid TEXT PRIMARY KEY,
			name TEXT,
			last_message_time TIMESTAMP,
			ephemeral_expiration INTEGER NOT NULL DEFAULT 0,
			ephemeral_setting_timestamp INTEGER NOT NULL DEFAULT 0
		);
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
			PRIMARY KEY (id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid)
		);
		CREATE TABLE calls (
			call_id TEXT,
			chat_jid TEXT,
			from_jid TEXT,
			timestamp TIMESTAMP,
			is_from_me BOOLEAN,
			call_type TEXT,
			is_group BOOLEAN,
			result TEXT,
			duration_sec INTEGER,
			ended_at TIMESTAMP,
			reason TEXT,
			PRIMARY KEY (call_id, chat_jid)
		);
	`)
	if err != nil {
		t.Fatalf("failed to create tables: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &store.MessageStore{DB: db}
}

// Logger returns a quiet logger for tests.
func Logger() waLog.Logger {
	return waLog.Stdout("Test", "WARN", true)
}

// QuerySender returns the sender column for the first message stored under a
// chat JID, or empty string if none.
func QuerySender(ms *store.MessageStore, chatJID string) string {
	var s string
	_ = ms.DB.QueryRow("SELECT sender FROM messages WHERE chat_jid = ? LIMIT 1", chatJID).Scan(&s)
	return s
}

// QueryChat returns the chat name, with found=false if the row is missing.
func QueryChat(ms *store.MessageStore, jid string) (name string, found bool) {
	err := ms.DB.QueryRow("SELECT name FROM chats WHERE jid = ?", jid).Scan(&name)
	return name, err == nil
}

// QueryChatLastMessageTime returns the last_message_time for a chat JID.
func QueryChatLastMessageTime(ms *store.MessageStore, jid string) (lastMessageTime string, found bool) {
	err := ms.DB.QueryRow("SELECT last_message_time FROM chats WHERE jid = ?", jid).Scan(&lastMessageTime)
	return lastMessageTime, err == nil
}

// QueryMessageCount returns the number of messages stored under a chat JID.
func QueryMessageCount(ms *store.MessageStore, chatJID string) int {
	var count int
	_ = ms.DB.QueryRow("SELECT COUNT(*) FROM messages WHERE chat_jid = ?", chatJID).Scan(&count)
	return count
}
