package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

func testLogger() waLog.Logger {
	return waLog.Stdout("Test", "WARN", true)
}

func newTestMessageStore(t *testing.T) *MessageStore {
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
	return &MessageStore{DB: db}
}

// queryChat returns the chat name, or found=false if not present.
func queryChat(ms *MessageStore, jid string) (name string, found bool) {
	err := ms.DB.QueryRow("SELECT name FROM chats WHERE jid = ?", jid).Scan(&name)
	return name, err == nil
}

// queryChatLastMessageTime returns the last_message_time for a chat JID.
func queryChatLastMessageTime(ms *MessageStore, jid string) (lastMessageTime string, found bool) {
	err := ms.DB.QueryRow("SELECT last_message_time FROM chats WHERE jid = ?", jid).Scan(&lastMessageTime)
	return lastMessageTime, err == nil
}

// queryMessageCount returns the number of messages stored under a chat JID.
func queryMessageCount(ms *MessageStore, chatJID string) int {
	var count int
	_ = ms.DB.QueryRow("SELECT COUNT(*) FROM messages WHERE chat_jid = ?", chatJID).Scan(&count)
	return count
}

func TestOpenWhatsmeowContactsDB_MissingPathDoesNotCreateDB(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "whatsapp.db")

	db, err := openWhatsmeowContactsDB(missingPath)
	if err != nil {
		t.Fatalf("openWhatsmeowContactsDB returned error: %v", err)
	}
	if db != nil {
		t.Fatalf("expected missing DB to return nil handle")
	}
	if _, err := os.Stat(missingPath); !os.IsNotExist(err) {
		t.Fatalf("expected missing DB path to stay absent, stat error: %v", err)
	}
}

func TestOpenWhatsmeowContactsDB_ReadOnly(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "whatsapp.db")
	seedDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if _, err := seedDB.Exec("CREATE TABLE marker (id INTEGER)"); err != nil {
		t.Fatalf("create marker table: %v", err)
	}
	if err := seedDB.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	db, err := openWhatsmeowContactsDB(dbPath)
	if err != nil {
		t.Fatalf("openWhatsmeowContactsDB returned error: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec("INSERT INTO marker (id) VALUES (1)"); err == nil {
		t.Fatalf("expected read-only whatsmeow DB handle to reject writes")
	}
}

func TestStoreChatPreservesEphemeralSettings(t *testing.T) {
	ms := newTestMessageStore(t)

	chatJID := "15551234567@s.whatsapp.net"
	if err := ms.UpdateChatEphemeralSettings(chatJID, 604800, 1710000000); err != nil {
		t.Fatalf("failed to seed ephemeral settings: %v", err)
	}

	if err := ms.StoreChat(chatJID, "Alice", time.Unix(1710000100, 0)); err != nil {
		t.Fatalf("failed to store chat: %v", err)
	}

	settings, err := ms.GetChatEphemeralSettings(chatJID)
	if err != nil {
		t.Fatalf("failed to load ephemeral settings: %v", err)
	}
	if settings.Expiration != 604800 {
		t.Fatalf("expected expiration 604800, got %d", settings.Expiration)
	}
	if settings.SettingTimestamp != 1710000000 {
		t.Fatalf("expected setting timestamp 1710000000, got %d", settings.SettingTimestamp)
	}
}

// TestUpdateChatEphemeralSettings_IgnoresZeroTimestamp pins down the sparse-chunk
// guard: a write with settingTimestamp == 0 carries no information about when
// the user toggled the chat's ephemeral state, so it must not clobber a
// previously-captured non-zero value. WhatsApp's history sync delivers
// Conversation records in many chunks; sparse later chunks omit the ephemeral
// fields and would otherwise reset the row to (0, 0).
func TestUpdateChatEphemeralSettings_IgnoresZeroTimestamp(t *testing.T) {
	ms := newTestMessageStore(t)

	chatJID := "15551234567@s.whatsapp.net"
	if err := ms.UpdateChatEphemeralSettings(chatJID, 604800, 1710000000); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := ms.UpdateChatEphemeralSettings(chatJID, 0, 0); err != nil {
		t.Fatalf("zero-ts write: %v", err)
	}

	settings, err := ms.GetChatEphemeralSettings(chatJID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if settings.Expiration != 604800 || settings.SettingTimestamp != 1710000000 {
		t.Fatalf("expected sparse zero-ts write to be ignored; got (%d, %d)",
			settings.Expiration, settings.SettingTimestamp)
	}
}

// TestUpdateChatEphemeralSettings_IgnoresOlderTimestamp pins down the
// monotonic-update rule: a write whose settingTimestamp is older than the
// stored one is stale (out-of-order delivery) and must not overwrite. This
// lets HandleMessage safely write ephemeral-from-ContextInfo on every inbound
// message without worrying about replays / late history-sync chunks.
func TestUpdateChatEphemeralSettings_IgnoresOlderTimestamp(t *testing.T) {
	ms := newTestMessageStore(t)

	chatJID := "15551234567@s.whatsapp.net"
	if err := ms.UpdateChatEphemeralSettings(chatJID, 604800, 1710000000); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// An older event (ts=1700000000) should not overwrite, even though both
	// expiration and ts are non-zero.
	if err := ms.UpdateChatEphemeralSettings(chatJID, 86400, 1700000000); err != nil {
		t.Fatalf("older-ts write: %v", err)
	}

	settings, err := ms.GetChatEphemeralSettings(chatJID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if settings.Expiration != 604800 || settings.SettingTimestamp != 1710000000 {
		t.Fatalf("expected older-ts write to be ignored; got (%d, %d)",
			settings.Expiration, settings.SettingTimestamp)
	}
}

func TestNewMessageStoreCreatesMessagesChatJIDIndex(t *testing.T) {
	// NewMessageStore writes to a relative "store/" directory, so run in a
	// temporary working directory that is cleaned up automatically.
	t.Chdir(t.TempDir())

	ms, err := NewMessageStore()
	if err != nil {
		t.Fatalf("NewMessageStore() failed: %v", err)
	}
	defer func() { _ = ms.Close() }()

	var count int
	if err := ms.DB.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_messages_chat_jid';`,
	).Scan(&count); err != nil {
		t.Fatalf("failed to query index metadata: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected idx_messages_chat_jid to exist, found %d", count)
	}
}
