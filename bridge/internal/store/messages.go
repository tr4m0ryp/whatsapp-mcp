package store

import (
	"time"
)

// StoreMessage stores a message in the database.
func (s *MessageStore) StoreMessage(id, chatJID, sender, content string, timestamp time.Time, isFromMe bool,
	mediaType, filename, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64,
	quotedMessageID string) error {
	// Only store if there's actual content or media
	if content == "" && mediaType == "" {
		return nil
	}

	// Store empty quoted_message_id as SQL NULL so the column is null for
	// plain messages (no ContextInfo). This makes the ON CONFLICT merge
	// straightforward: COALESCE prefers the new non-null value over a
	// kept null, and ignores an incoming null so it cannot clobber a
	// previously-stored ID.
	var qmid interface{}
	if quotedMessageID != "" {
		qmid = quotedMessageID
	}

	_, err := s.DB.Exec(
		`INSERT INTO messages
		(id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length, quoted_message_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id, chat_jid) DO UPDATE SET
			sender = excluded.sender,
			content = excluded.content,
			timestamp = excluded.timestamp,
			is_from_me = excluded.is_from_me,
			media_type = excluded.media_type,
			filename = excluded.filename,
			url = excluded.url,
			media_key = excluded.media_key,
			file_sha256 = excluded.file_sha256,
			file_enc_sha256 = excluded.file_enc_sha256,
			file_length = excluded.file_length,
			quoted_message_id = COALESCE(excluded.quoted_message_id, messages.quoted_message_id)`,
		id, chatJID, sender, content, timestamp, isFromMe, mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength, qmid,
	)
	return err
}

// MarkMessageDeleted records a "delete for everyone" event by stamping
// deleted_at on the target row. Content is preserved on purpose — the
// local DB is an archive, and the value is in knowing the message was
// retracted, not in erasing what was said.
//
// First-revoke-wins: once deleted_at is set, a later REVOKE does not
// overwrite it. Calling this for a message that does not exist (e.g.
// the bridge missed the original) is a silent no-op, not an error.
func (s *MessageStore) MarkMessageDeleted(messageID, chatJID string, deletedAt time.Time) error {
	_, err := s.DB.Exec(
		`UPDATE messages SET deleted_at = ?
		 WHERE id = ? AND chat_jid = ? AND deleted_at IS NULL`,
		deletedAt, messageID, chatJID,
	)
	return err
}

// HasInboundHistory reports whether a chat contains any message the other
// side sent. It is the test for "has this person ever written to us", which
// separates a reply from a cold contact on the send path.
func (s *MessageStore) HasInboundHistory(chatJID string) (bool, error) {
	var exists int
	err := s.DB.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM messages WHERE chat_jid = ? AND is_from_me = 0)`,
		chatJID,
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}

// CountColdConversationsSince counts chats whose earliest archived message is
// outbound and lands at or after since — i.e. conversations we started.
//
// Derived from the archive rather than tracked in its own counter so the
// number survives restarts without extra state to keep in sync. The bridge
// crashing or being redeployed must not hand the sender a fresh daily budget.
//
// A conversation counts once it is opened, whether or not the recipient
// replied — a reply does not refund the budget it cost to reach them.
func (s *MessageStore) CountColdConversationsSince(since time.Time) (int, error) {
	var count int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM (
			SELECT chat_jid,
			       MIN(timestamp) AS first_ts
			  FROM messages
			 GROUP BY chat_jid
			HAVING first_ts >= ?
			   AND MAX(CASE WHEN timestamp = first_ts AND is_from_me = 1 THEN 1 ELSE 0 END) = 1
		)`,
		since,
	).Scan(&count)
	return count, err
}

// GetMessages returns the most recent messages from a chat.
func (s *MessageStore) GetMessages(chatJID string, limit int) ([]Message, error) {
	rows, err := s.DB.Query(
		"SELECT sender, content, timestamp, is_from_me, media_type, filename FROM messages WHERE chat_jid = ? ORDER BY timestamp DESC LIMIT ?",
		chatJID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var messages []Message
	for rows.Next() {
		var msg Message
		var timestamp time.Time
		err := rows.Scan(&msg.Sender, &msg.Content, &timestamp, &msg.IsFromMe, &msg.MediaType, &msg.Filename)
		if err != nil {
			return nil, err
		}
		msg.Time = timestamp
		messages = append(messages, msg)
	}

	return messages, nil
}

// MediaInfo is everything needed to decrypt and re-download a media
// attachment from WhatsApp's CDN.
type MediaInfo struct {
	MediaType     string
	Filename      string
	URL           string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
	FileLength    uint64
	Timestamp     time.Time
}

// StoreMediaInfo stores additional media info in the database.
func (s *MessageStore) StoreMediaInfo(id, chatJID, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) error {
	_, err := s.DB.Exec(
		"UPDATE messages SET url = ?, media_key = ?, file_sha256 = ?, file_enc_sha256 = ?, file_length = ? WHERE id = ? AND chat_jid = ?",
		url, mediaKey, fileSHA256, fileEncSHA256, fileLength, id, chatJID,
	)
	return err
}

// GetMediaInfo returns the media columns for a message, including the
// message timestamp (needed to rebuild the deterministic download filename).
func (s *MessageStore) GetMediaInfo(id, chatJID string) (MediaInfo, error) {
	var info MediaInfo
	err := s.DB.QueryRow(
		"SELECT media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length, timestamp FROM messages WHERE id = ? AND chat_jid = ?",
		id, chatJID,
	).Scan(&info.MediaType, &info.Filename, &info.URL, &info.MediaKey, &info.FileSHA256, &info.FileEncSHA256, &info.FileLength, &info.Timestamp)
	return info, err
}
