// Package store owns the bridge's SQLite message archive (messages.db):
// schema, chat/message/call persistence, and the legacy-LID migrations.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Message represents a chat message for our client.
type Message struct {
	Time      time.Time
	Sender    string
	Content   string
	IsFromMe  bool
	MediaType string
	Filename  string
}

// MessageStore is the database handler for storing message history.
//
// DB is the bridge's own archive; WaDB is whatsmeow's database, opened
// read-only for contact-name resolution fallback. Both are exported so
// tests can seed and inspect rows directly.
type MessageStore struct {
	DB   *sql.DB
	WaDB *sql.DB
}

// NewMessageStore initializes the message store, creating the store
// directory, the schema, and the read-only whatsmeow handle.
func NewMessageStore() (*MessageStore, error) {
	// Create directory for database if it doesn't exist
	if err := os.MkdirAll("store", 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %v", err)
	}

	// Open SQLite database for messages
	db, err := sql.Open("sqlite3", "file:store/messages.db?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("failed to open message database: %v", err)
	}

	// Create tables if they don't exist
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			jid TEXT PRIMARY KEY,
			name TEXT,
			last_message_time TIMESTAMP,
			ephemeral_expiration INTEGER NOT NULL DEFAULT 0,
			ephemeral_setting_timestamp INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS messages (
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
			PRIMARY KEY (id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid)
		);

		CREATE TABLE IF NOT EXISTS calls (
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

		CREATE INDEX IF NOT EXISTS idx_calls_chat ON calls(chat_jid);
		CREATE INDEX IF NOT EXISTS idx_calls_timestamp ON calls(timestamp);
		CREATE INDEX IF NOT EXISTS idx_messages_chat_jid ON messages(chat_jid);
	`)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create tables: %v", err)
	}

	// Open whatsmeow's database read-only for contact name resolution fallback.
	// Missing DBs are expected on first run and should not create a new file.
	waDB, err := openWhatsmeowContactsDB(whatsmeowDBPath)
	if err != nil {
		fmt.Printf("Warning: could not open whatsmeow database for contact resolution: %v\n", err)
	}

	if err := ensureSchema(db); err != nil {
		_ = db.Close()
		if waDB != nil {
			_ = waDB.Close()
		}
		return nil, err
	}

	return &MessageStore{DB: db, WaDB: waDB}, nil
}

const whatsmeowDBPath = "store/whatsapp.db"

func openWhatsmeowContactsDB(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro", path))
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func ensureSchema(db *sql.DB) error {
	if err := ensureColumn(db, "chats", "ephemeral_expiration", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("failed to ensure chats.ephemeral_expiration column: %w", err)
	}
	if err := ensureColumn(db, "chats", "ephemeral_setting_timestamp", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("failed to ensure chats.ephemeral_setting_timestamp column: %w", err)
	}
	if err := ensureColumn(db, "messages", "deleted_at", "TIMESTAMP"); err != nil {
		return fmt.Errorf("failed to ensure messages.deleted_at column: %w", err)
	}
	if err := ensureColumn(db, "messages", "quoted_message_id", "TEXT"); err != nil {
		return fmt.Errorf("failed to ensure messages.quoted_message_id column: %w", err)
	}
	return nil
}

func ensureColumn(db *sql.DB, tableName, columnName, columnSpec string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return err
	}

	exists := false
	for rows.Next() {
		var cid int
		var name string
		var colType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == columnName {
			exists = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	// Close before ALTER: SQLite holds a read lock while rows are open,
	// which would make the schema change fail with "database is locked".
	if err := rows.Close(); err != nil {
		return err
	}
	if exists {
		return nil
	}

	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", tableName, columnName, columnSpec))
	return err
}

// Close the database connections.
func (s *MessageStore) Close() error {
	var waErr error
	if s.WaDB != nil {
		waErr = s.WaDB.Close()
	}
	if err := s.DB.Close(); err != nil {
		return err
	}
	return waErr
}
