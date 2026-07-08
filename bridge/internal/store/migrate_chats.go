package store

import (
	"fmt"
	"os"
	"strings"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// MigrateLegacyLIDChatsToPhoneJIDs rewrites message/chat rows stored under
// legacy @lid chat JIDs into phone-based @s.whatsapp.net chat JIDs using the
// whatsmeow LID map in whatsapp.db.
func (s *MessageStore) MigrateLegacyLIDChatsToPhoneJIDs(whatsappDBPath string, logger waLog.Logger) error {
	if _, err := os.Stat(whatsappDBPath); err != nil {
		if os.IsNotExist(err) {
			logger.Infof("Skipping LID chat migration: %s not found", whatsappDBPath)
			return nil
		}
		return fmt.Errorf("failed to stat WhatsApp DB %s: %w", whatsappDBPath, err)
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to start LID chat migration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	alias := fmt.Sprintf("wa_mig_%d", time.Now().UnixNano())
	escapedPath := strings.ReplaceAll(whatsappDBPath, "'", "''")
	if _, err := tx.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS %s;", escapedPath, alias)); err != nil {
		return fmt.Errorf("failed to attach WhatsApp DB for LID chat migration: %w", err)
	}

	var lidMapTableExists int
	if err := tx.QueryRow(fmt.Sprintf(
		"SELECT COUNT(1) FROM %s.sqlite_master WHERE type='table' AND name='whatsmeow_lid_map';",
		alias,
	)).Scan(&lidMapTableExists); err != nil {
		return fmt.Errorf("failed to inspect WhatsApp DB schema for LID migration: %w", err)
	}
	if lidMapTableExists == 0 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit no-op LID chat migration: %w", err)
		}
		logger.Infof("Skipping LID chat migration: whatsmeow_lid_map table not found")
		return nil
	}

	if _, err := tx.Exec(fmt.Sprintf(`
		CREATE TEMP TABLE tmp_lid_to_phone AS
		SELECT DISTINCT
			lm.lid || '@lid' AS lid_jid,
			lm.pn || '@s.whatsapp.net' AS phone_jid
		FROM %s.whatsmeow_lid_map lm
		WHERE lm.lid != '' AND lm.pn != ''
		  AND (
		  	EXISTS (SELECT 1 FROM chats c WHERE c.jid = lm.lid || '@lid')
		  	OR EXISTS (SELECT 1 FROM messages m WHERE m.chat_jid = lm.lid || '@lid')
		  );
	`, alias)); err != nil {
		return fmt.Errorf("failed to build temporary LID mapping table: %w", err)
	}

	var mappedChats int
	if err := tx.QueryRow("SELECT COUNT(*) FROM tmp_lid_to_phone;").Scan(&mappedChats); err != nil {
		return fmt.Errorf("failed to count mapped LID chats: %w", err)
	}

	if mappedChats == 0 {
		if _, err := tx.Exec("DROP TABLE IF EXISTS tmp_lid_to_phone;"); err != nil {
			return fmt.Errorf("failed to clean temporary LID mapping table: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit no-op LID chat migration: %w", err)
		}
		logger.Infof("LID chat migration: nothing to migrate")
		return nil
	}

	if _, err := tx.Exec(`
		CREATE TEMP TABLE tmp_lid_chat_candidates AS
		SELECT
			m.phone_jid AS phone_jid,
			m.lid_jid AS lid_jid,
			NULLIF(TRIM(c.name), '') AS source_name,
			COALESCE(
				c.last_message_time,
				(
					SELECT MAX(msg.timestamp)
					FROM messages msg
					WHERE msg.chat_jid = m.lid_jid
				)
			) AS source_last_message_time
		FROM tmp_lid_to_phone m
		LEFT JOIN chats c ON c.jid = m.lid_jid;
	`); err != nil {
		return fmt.Errorf("failed to build temporary chat candidate table: %w", err)
	}

	if _, err := tx.Exec(`
		CREATE TEMP TABLE tmp_lid_chat_meta AS
		SELECT
			c.phone_jid AS phone_jid,
			COALESCE(
				(
					SELECT c2.source_name
					FROM tmp_lid_chat_candidates c2
					WHERE c2.phone_jid = c.phone_jid
						AND c2.source_name IS NOT NULL
					ORDER BY
						CASE WHEN c2.source_last_message_time IS NULL THEN 1 ELSE 0 END,
						c2.source_last_message_time DESC,
						c2.lid_jid ASC
					LIMIT 1
				),
				substr(c.phone_jid, 1, instr(c.phone_jid, '@') - 1)
			) AS source_name,
			MAX(c.source_last_message_time) AS source_last_message_time
		FROM tmp_lid_chat_candidates c
		GROUP BY c.phone_jid;
	`); err != nil {
		return fmt.Errorf("failed to build temporary chat metadata table: %w", err)
	}

	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO chats (jid, name, last_message_time)
		SELECT phone_jid, source_name, source_last_message_time
		FROM tmp_lid_chat_meta;
	`); err != nil {
		return fmt.Errorf("failed to upsert destination chat rows: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE chats
		SET
			name = CASE
				WHEN (name IS NULL OR TRIM(name) = '') THEN (
					SELECT m.source_name
					FROM tmp_lid_chat_meta m
					WHERE m.phone_jid = chats.jid
				)
				ELSE name
			END,
			last_message_time = CASE
				WHEN (
					SELECT m.source_last_message_time
					FROM tmp_lid_chat_meta m
					WHERE m.phone_jid = chats.jid
				) IS NULL THEN last_message_time
				WHEN last_message_time IS NULL THEN (
					SELECT m.source_last_message_time
					FROM tmp_lid_chat_meta m
					WHERE m.phone_jid = chats.jid
				)
				WHEN (
					SELECT m.source_last_message_time
					FROM tmp_lid_chat_meta m
					WHERE m.phone_jid = chats.jid
				) > last_message_time THEN (
					SELECT m.source_last_message_time
					FROM tmp_lid_chat_meta m
					WHERE m.phone_jid = chats.jid
				)
				ELSE last_message_time
			END
		WHERE jid IN (SELECT phone_jid FROM tmp_lid_chat_meta);
	`); err != nil {
		return fmt.Errorf("failed to merge destination chat metadata: %w", err)
	}

	insertResult, err := tx.Exec(`
		INSERT OR IGNORE INTO messages (
			id, chat_jid, sender, content, timestamp, is_from_me,
			media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length
		)
		SELECT
			msg.id,
			m.phone_jid,
			msg.sender,
			msg.content,
			msg.timestamp,
			msg.is_from_me,
			msg.media_type,
			msg.filename,
			msg.url,
			msg.media_key,
			msg.file_sha256,
			msg.file_enc_sha256,
			msg.file_length
		FROM messages msg
		JOIN tmp_lid_to_phone m ON m.lid_jid = msg.chat_jid;
	`)
	if err != nil {
		return fmt.Errorf("failed to copy legacy LID messages into phone chats: %w", err)
	}

	insertedMessages, _ := insertResult.RowsAffected()

	deleteMessagesResult, err := tx.Exec(`
		DELETE FROM messages
		WHERE chat_jid IN (SELECT lid_jid FROM tmp_lid_to_phone);
	`)
	if err != nil {
		return fmt.Errorf("failed to delete migrated LID messages: %w", err)
	}
	deletedMessages, _ := deleteMessagesResult.RowsAffected()

	deleteChatsResult, err := tx.Exec(`
		DELETE FROM chats
		WHERE jid IN (SELECT lid_jid FROM tmp_lid_to_phone);
	`)
	if err != nil {
		return fmt.Errorf("failed to delete migrated LID chats: %w", err)
	}
	deletedChats, _ := deleteChatsResult.RowsAffected()

	if _, err := tx.Exec("DROP TABLE IF EXISTS tmp_lid_to_phone;"); err != nil {
		return fmt.Errorf("failed to clean temporary LID mapping table: %w", err)
	}
	if _, err := tx.Exec("DROP TABLE IF EXISTS tmp_lid_chat_meta;"); err != nil {
		return fmt.Errorf("failed to clean temporary chat metadata table: %w", err)
	}
	if _, err := tx.Exec("DROP TABLE IF EXISTS tmp_lid_chat_candidates;"); err != nil {
		return fmt.Errorf("failed to clean temporary chat candidate table: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit LID chat migration: %w", err)
	}

	logger.Infof(
		"LID chat migration complete: mapped_chats=%d inserted_messages=%d deleted_lid_messages=%d deleted_lid_chats=%d",
		mappedChats,
		insertedMessages,
		deletedMessages,
		deletedChats,
	)
	return nil
}
