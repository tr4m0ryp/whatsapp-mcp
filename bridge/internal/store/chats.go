package store

import (
	"database/sql"
	"time"
)

// ChatEphemeralSettings is a chat's disappearing-message state.
type ChatEphemeralSettings struct {
	Expiration       uint32
	SettingTimestamp int64
}

// StoreChat stores a chat in the database. An empty `name` preserves any
// existing resolved contact/group name on the row — outbound-message
// persistence doesn't have a friendly name available at send time and must
// not clobber names set by inbound handling or history sync.
// last_message_time is merged monotonically so out-of-order delivery
// (history sync, backfill) can't move it backwards.
func (s *MessageStore) StoreChat(jid, name string, lastMessageTime time.Time) error {
	_, err := s.DB.Exec(
		`INSERT INTO chats (jid, name, last_message_time)
		VALUES (?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			name = CASE WHEN excluded.name = '' THEN chats.name ELSE excluded.name END,
			last_message_time = CASE
				WHEN chats.last_message_time IS NULL THEN excluded.last_message_time
				WHEN excluded.last_message_time IS NULL THEN chats.last_message_time
				WHEN excluded.last_message_time > chats.last_message_time THEN excluded.last_message_time
				ELSE chats.last_message_time
			END`,
		jid, name, lastMessageTime,
	)
	return err
}

// UpdateChatEphemeralSettings records the chat's disappearing-message timer.
// Writes are gated on settingTimestamp so that low-information events don't
// clobber authoritative ones:
//
//   - settingTimestamp == 0: skip entirely. Sparse history-sync chunks and
//     plain (non-ephemeral) messages deliver records with no ephemeral fields,
//     and we must not interpret that absence as "the user turned it off".
//   - settingTimestamp older than the stored one: skip. Out-of-order delivery
//     (replays, late history-sync chunks, old messages flowing in) would
//     otherwise downgrade newer state to older state.
func (s *MessageStore) UpdateChatEphemeralSettings(jid string, expiration uint32, settingTimestamp int64) error {
	if settingTimestamp == 0 {
		return nil
	}
	// INSERT only the ephemeral columns; leave name/last_message_time NULL
	// so a `GroupInfo` event firing before any StoreChat call doesn't
	// fabricate placeholder metadata (raw JID as name, year-0001 timestamp)
	// that would leak into list_chats output.
	_, err := s.DB.Exec(
		`INSERT INTO chats (jid, ephemeral_expiration, ephemeral_setting_timestamp)
		VALUES (?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			ephemeral_expiration = excluded.ephemeral_expiration,
			ephemeral_setting_timestamp = excluded.ephemeral_setting_timestamp
		WHERE excluded.ephemeral_setting_timestamp >= chats.ephemeral_setting_timestamp`,
		jid, expiration, settingTimestamp,
	)
	return err
}

// GetChatEphemeralSettings returns the stored disappearing-message state for
// a chat, or sql.ErrNoRows when the chat is unknown.
func (s *MessageStore) GetChatEphemeralSettings(jid string) (ChatEphemeralSettings, error) {
	var settings ChatEphemeralSettings
	err := s.DB.QueryRow(
		"SELECT ephemeral_expiration, ephemeral_setting_timestamp FROM chats WHERE jid = ?",
		jid,
	).Scan(&settings.Expiration, &settings.SettingTimestamp)
	if err != nil {
		return ChatEphemeralSettings{}, err
	}
	return settings, nil
}

// GetChats returns all chats keyed by JID with their last-message times.
func (s *MessageStore) GetChats() (map[string]time.Time, error) {
	rows, err := s.DB.Query("SELECT jid, last_message_time FROM chats ORDER BY last_message_time DESC")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	chats := make(map[string]time.Time)
	for rows.Next() {
		var jid string
		// last_message_time can be NULL — UpdateChatEphemeralSettings can
		// create a chat row from a GroupInfo / ephemeral-setting event
		// before any message has landed for that chat.
		var lastMessageTime sql.NullTime
		err := rows.Scan(&jid, &lastMessageTime)
		if err != nil {
			return nil, err
		}
		if lastMessageTime.Valid {
			chats[jid] = lastMessageTime.Time
		} else {
			chats[jid] = time.Time{}
		}
	}

	return chats, nil
}

// ChatName returns the stored name for a chat, or empty when the chat is
// unknown or unnamed.
func (s *MessageStore) ChatName(jid string) string {
	var name string
	_ = s.DB.QueryRow("SELECT name FROM chats WHERE jid = ?", jid).Scan(&name)
	return name
}
