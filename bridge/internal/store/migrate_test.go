package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyLIDSendersToPhones_RewritesAndIsIdempotent(t *testing.T) {
	ms := newTestMessageStore(t)
	logger := testLogger()

	tmpDir := t.TempDir()
	whatsappDBPath := filepath.Join(tmpDir, "whatsapp.db")

	waDB, err := sql.Open("sqlite3", whatsappDBPath)
	if err != nil {
		t.Fatalf("failed to create whatsapp db: %v", err)
	}
	defer func() { _ = waDB.Close() }()

	if _, err := waDB.Exec(`
		CREATE TABLE whatsmeow_lid_map (
			lid TEXT PRIMARY KEY,
			pn TEXT NOT NULL
		);
		INSERT INTO whatsmeow_lid_map (lid, pn) VALUES ('111', '222');
		INSERT INTO whatsmeow_lid_map (lid, pn) VALUES ('333', '444');
	`); err != nil {
		t.Fatalf("failed to prepare lid map db: %v", err)
	}

	chatPhone := "222@s.whatsapp.net"
	groupChat := "group@g.us"

	if _, err := ms.DB.Exec(`
		INSERT INTO chats (jid, name, last_message_time) VALUES
			(?, 'Peer', '2026-03-01T10:00:00Z'),
			(?, 'Group', '2026-03-01T11:00:00Z');

		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length) VALUES
			('m1', ?, '111', 'incoming dm pre-fix',  '2026-03-01T10:00:00Z', 0, '', '', '', NULL, NULL, NULL, 0),
			('m2', ?, '222', 'incoming dm post-fix', '2026-03-01T10:01:00Z', 0, '', '', '', NULL, NULL, NULL, 0),
			('g1', ?, '333', 'group msg pre-fix',    '2026-03-01T11:00:00Z', 0, '', '', '', NULL, NULL, NULL, 0),
			('g2', ?, '999', 'group msg unmapped',   '2026-03-01T11:01:00Z', 0, '', '', '', NULL, NULL, NULL, 0);
	`, chatPhone, groupChat, chatPhone, chatPhone, groupChat, groupChat); err != nil {
		t.Fatalf("failed to seed message store: %v", err)
	}

	if err := ms.MigrateLegacyLIDSendersToPhones(whatsappDBPath, logger); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	type row struct {
		id, sender string
	}
	var got []row
	rows, err := ms.DB.Query("SELECT id, sender FROM messages ORDER BY id")
	if err != nil {
		t.Fatalf("failed to read messages: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.sender); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}

	want := map[string]string{
		"m1": "222", // rewritten via lid map
		"m2": "222", // already phone, untouched
		"g1": "444", // rewritten via lid map
		"g2": "999", // unmapped LID stays as-is (graceful fallback)
	}
	for _, r := range got {
		if w, ok := want[r.id]; !ok || r.sender != w {
			t.Errorf("message %s: sender = %q, want %q", r.id, r.sender, w)
		}
	}

	if err := ms.MigrateLegacyLIDSendersToPhones(whatsappDBPath, logger); err != nil {
		t.Fatalf("second run should be no-op, got error: %v", err)
	}
}

func TestMigrateLegacyLIDSendersToPhones_MissingWhatsAppDBIsNoOp(t *testing.T) {
	ms := newTestMessageStore(t)
	logger := testLogger()

	missingPath := filepath.Join(t.TempDir(), "missing-whatsapp.db")
	if err := ms.MigrateLegacyLIDSendersToPhones(missingPath, logger); err != nil {
		t.Fatalf("expected missing whatsapp db to be a no-op, got error: %v", err)
	}
}

func TestMigrateLegacyLIDChatsToPhoneJIDs_MigratesAndIsIdempotent(t *testing.T) {
	ms := newTestMessageStore(t)
	logger := testLogger()

	tmpDir := t.TempDir()
	whatsappDBPath := filepath.Join(tmpDir, "whatsapp.db")

	waDB, err := sql.Open("sqlite3", whatsappDBPath)
	if err != nil {
		t.Fatalf("failed to create whatsapp db: %v", err)
	}
	defer func() { _ = waDB.Close() }()

	if _, err := waDB.Exec(`
		CREATE TABLE whatsmeow_lid_map (
			lid TEXT PRIMARY KEY,
			pn TEXT NOT NULL
		);
		INSERT INTO whatsmeow_lid_map (lid, pn) VALUES ('111', '222');
	`); err != nil {
		t.Fatalf("failed to prepare lid map db: %v", err)
	}

	lidJID := "111@lid"
	phoneJID := "222@s.whatsapp.net"

	_, err = ms.DB.Exec(`
		INSERT INTO chats (jid, name, last_message_time) VALUES
			(?, 'Legacy LID Name', '2026-03-01T10:00:00Z'),
			(?, '', '2026-03-01T09:00:00Z');

		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length) VALUES
			('dup', ?, 'alice', 'lid duplicate', '2026-03-01T10:00:00Z', 0, '', '', '', NULL, NULL, NULL, 0),
			('only-lid', ?, 'alice', 'lid only', '2026-03-01T10:01:00Z', 0, '', '', '', NULL, NULL, NULL, 0),
			('dup', ?, 'alice', 'phone duplicate', '2026-03-01T10:00:00Z', 0, '', '', '', NULL, NULL, NULL, 0),
			('only-phone', ?, 'alice', 'phone only', '2026-03-01T10:02:00Z', 0, '', '', '', NULL, NULL, NULL, 0);
	`, lidJID, phoneJID, lidJID, lidJID, phoneJID, phoneJID)
	if err != nil {
		t.Fatalf("failed to seed message store: %v", err)
	}

	if err := ms.MigrateLegacyLIDChatsToPhoneJIDs(whatsappDBPath, logger); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	if lidCount := queryMessageCount(ms, lidJID); lidCount != 0 {
		t.Fatalf("expected 0 messages under migrated LID chat, got %d", lidCount)
	}
	if phoneCount := queryMessageCount(ms, phoneJID); phoneCount != 3 {
		t.Fatalf("expected 3 messages under phone chat after dedupe, got %d", phoneCount)
	}

	if _, found := queryChat(ms, lidJID); found {
		t.Fatalf("expected migrated LID chat row to be removed")
	}

	phoneName, found := queryChat(ms, phoneJID)
	if !found {
		t.Fatalf("expected phone chat row to exist after migration")
	}
	if phoneName != "Legacy LID Name" {
		t.Fatalf("expected phone chat name to be hydrated from LID chat, got %q", phoneName)
	}

	phoneTime, timeFound := queryChatLastMessageTime(ms, phoneJID)
	if !timeFound {
		t.Fatalf("expected phone chat to have last_message_time after migration")
	}
	if phoneTime != "2026-03-01T10:00:00Z" {
		t.Fatalf("expected phone chat last_message_time to be the latest (from LID chat), got %q", phoneTime)
	}

	if err := ms.MigrateLegacyLIDChatsToPhoneJIDs(whatsappDBPath, logger); err != nil {
		t.Fatalf("second migration run should be a no-op, got error: %v", err)
	}
	if phoneCount := queryMessageCount(ms, phoneJID); phoneCount != 3 {
		t.Fatalf("expected idempotent result with 3 phone messages, got %d", phoneCount)
	}
}

func TestMigrateLegacyLIDChatsToPhoneJIDs_MissingWhatsAppDBIsNoOp(t *testing.T) {
	ms := newTestMessageStore(t)
	logger := testLogger()

	missingPath := filepath.Join(t.TempDir(), "missing-whatsapp.db")
	if err := ms.MigrateLegacyLIDChatsToPhoneJIDs(missingPath, logger); err != nil {
		t.Fatalf("expected missing whatsapp db to be treated as no-op, got error: %v", err)
	}
}

func TestMigrateLegacyLIDChatsToPhoneJIDs_AggregatesByPhoneJIDDeterministically(t *testing.T) {
	ms := newTestMessageStore(t)
	logger := testLogger()

	tmpDir := t.TempDir()
	whatsappDBPath := filepath.Join(tmpDir, "whatsapp.db")

	waDB, err := sql.Open("sqlite3", whatsappDBPath)
	if err != nil {
		t.Fatalf("failed to create whatsapp db: %v", err)
	}
	defer func() { _ = waDB.Close() }()

	if _, err := waDB.Exec(`
		CREATE TABLE whatsmeow_lid_map (
			lid TEXT PRIMARY KEY,
			pn TEXT NOT NULL
		);
		INSERT INTO whatsmeow_lid_map (lid, pn) VALUES ('111', '222');
		INSERT INTO whatsmeow_lid_map (lid, pn) VALUES ('333', '222');
	`); err != nil {
		t.Fatalf("failed to prepare lid map db: %v", err)
	}

	lidA := "111@lid"
	lidB := "333@lid"
	phoneJID := "222@s.whatsapp.net"

	_, err = ms.DB.Exec(`
		INSERT INTO chats (jid, name, last_message_time) VALUES
			(?, 'Older Name', '2026-03-01T10:00:00Z'),
			(?, 'Newest Name', '2026-03-01T11:00:00Z');

		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length) VALUES
			('a1', ?, 'alice', 'from lid A', '2026-03-01T10:00:00Z', 0, '', '', '', NULL, NULL, NULL, 0),
			('b1', ?, 'bob', 'from lid B', '2026-03-01T11:00:00Z', 0, '', '', '', NULL, NULL, NULL, 0);
	`, lidA, lidB, lidA, lidB)
	if err != nil {
		t.Fatalf("failed to seed message store: %v", err)
	}

	if err := ms.MigrateLegacyLIDChatsToPhoneJIDs(whatsappDBPath, logger); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	if count := queryMessageCount(ms, lidA); count != 0 {
		t.Fatalf("expected no messages under first LID after migration, got %d", count)
	}
	if count := queryMessageCount(ms, lidB); count != 0 {
		t.Fatalf("expected no messages under second LID after migration, got %d", count)
	}
	if count := queryMessageCount(ms, phoneJID); count != 2 {
		t.Fatalf("expected 2 messages under phone JID after migration, got %d", count)
	}

	name, found := queryChat(ms, phoneJID)
	if !found {
		t.Fatalf("expected merged phone chat row to exist")
	}
	if name != "Newest Name" {
		t.Fatalf("expected deterministic name selection from latest source chat, got %q", name)
	}

	var lastMessage string
	if err := ms.DB.QueryRow("SELECT last_message_time FROM chats WHERE jid = ?", phoneJID).Scan(&lastMessage); err != nil {
		t.Fatalf("failed to read merged last_message_time: %v", err)
	}
	if lastMessage != "2026-03-01T11:00:00Z" {
		t.Fatalf("expected merged last_message_time to be max source value, got %s", lastMessage)
	}
}
