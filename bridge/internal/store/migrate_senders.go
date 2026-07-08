package store

import (
	"fmt"
	"os"
	"strings"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// MigrateLegacyLIDSendersToPhones rewrites the `sender` column for any
// message whose stored value is a LID user-part for which whatsmeow has a
// known phone-number mapping. This is the row-level analogue of the
// chat-JID migration and is required because earlier builds resolved
// the chat JID but stored the raw LID user-part as the sender, leaving
// the database internally inconsistent (chat = phone, sender = LID).
//
// The migration is idempotent: a second run finds no remaining LID-shaped
// senders to rewrite. It is safe to run on every startup.
func (s *MessageStore) MigrateLegacyLIDSendersToPhones(whatsappDBPath string, logger waLog.Logger) error {
	if _, err := os.Stat(whatsappDBPath); err != nil {
		if os.IsNotExist(err) {
			logger.Infof("Skipping LID sender migration: %s not found", whatsappDBPath)
			return nil
		}
		return fmt.Errorf("failed to stat WhatsApp DB %s: %w", whatsappDBPath, err)
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to start LID sender migration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	alias := fmt.Sprintf("wa_sender_mig_%d", time.Now().UnixNano())
	escapedPath := strings.ReplaceAll(whatsappDBPath, "'", "''")
	if _, err := tx.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS %s;", escapedPath, alias)); err != nil {
		return fmt.Errorf("failed to attach WhatsApp DB for LID sender migration: %w", err)
	}

	var lidMapTableExists int
	if err := tx.QueryRow(fmt.Sprintf(
		"SELECT COUNT(1) FROM %s.sqlite_master WHERE type='table' AND name='whatsmeow_lid_map';",
		alias,
	)).Scan(&lidMapTableExists); err != nil {
		return fmt.Errorf("failed to inspect WhatsApp DB schema for LID sender migration: %w", err)
	}
	if lidMapTableExists == 0 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit no-op LID sender migration: %w", err)
		}
		logger.Infof("Skipping LID sender migration: whatsmeow_lid_map table not found")
		return nil
	}

	// The sender column stores just the user-part (no @server suffix), so we
	// match directly against whatsmeow_lid_map.lid. We pre-build a temp table
	// scoped to senders that actually appear in our messages, both to avoid
	// scanning the full LID map per row and to give us an accurate row count.
	if _, err := tx.Exec(fmt.Sprintf(`
		CREATE TEMP TABLE tmp_lid_sender_map AS
		SELECT DISTINCT lm.lid AS lid_user, lm.pn AS phone_user
		FROM %s.whatsmeow_lid_map lm
		WHERE lm.lid != '' AND lm.pn != ''
		  AND EXISTS (SELECT 1 FROM messages m WHERE m.sender = lm.lid);
	`, alias)); err != nil {
		return fmt.Errorf("failed to build temporary LID sender mapping table: %w", err)
	}

	var mappedSenders int
	if err := tx.QueryRow("SELECT COUNT(*) FROM tmp_lid_sender_map;").Scan(&mappedSenders); err != nil {
		return fmt.Errorf("failed to count mapped LID senders: %w", err)
	}

	if mappedSenders == 0 {
		if _, err := tx.Exec("DROP TABLE IF EXISTS tmp_lid_sender_map;"); err != nil {
			return fmt.Errorf("failed to clean temporary LID sender mapping table: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit no-op LID sender migration: %w", err)
		}
		logger.Infof("LID sender migration: nothing to migrate")
		return nil
	}

	updateResult, err := tx.Exec(`
		UPDATE messages
		SET sender = (
			SELECT phone_user FROM tmp_lid_sender_map WHERE lid_user = messages.sender
		)
		WHERE sender IN (SELECT lid_user FROM tmp_lid_sender_map);
	`)
	if err != nil {
		return fmt.Errorf("failed to rewrite legacy LID senders: %w", err)
	}
	updatedRows, _ := updateResult.RowsAffected()

	if _, err := tx.Exec("DROP TABLE IF EXISTS tmp_lid_sender_map;"); err != nil {
		return fmt.Errorf("failed to clean temporary LID sender mapping table: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit LID sender migration: %w", err)
	}

	logger.Infof(
		"LID sender migration complete: mapped_senders=%d updated_messages=%d",
		mappedSenders,
		updatedRows,
	)
	return nil
}
