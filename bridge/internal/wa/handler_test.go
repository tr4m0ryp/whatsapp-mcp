package wa_test

import (
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-mcp/bridge/internal/testutil"
)

// TestHandleMessage_BackfillsEphemeralFromContextInfo asserts the end-to-end
// backfill: an inbound regular message whose ContextInfo carries a
// non-zero EphemeralSettingTimestamp must update the chat's ephemeral
// settings row, so subsequent outgoing messages from the bridge respect the
// chat's disappearing-message timer.
func TestHandleMessage_BackfillsEphemeralFromContextInfo(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)

	msg := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     phonePN,
				Sender:   phonePN,
				IsFromMe: false,
			},
			ID:        "ephemeral-backfill-001",
			Timestamp: time.Now(),
		},
		Message: &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String("hi from a disappearing chat"),
				ContextInfo: &waProto.ContextInfo{
					Expiration:                proto.Uint32(604800),
					EphemeralSettingTimestamp: proto.Int64(1710000000),
				},
			},
		},
	}

	newHandler(client, ms).HandleMessage(msg)

	settings, err := ms.GetChatEphemeralSettings(phonePN.String())
	if err != nil {
		t.Fatalf("get ephemeral settings: %v", err)
	}
	if settings.Expiration != 604800 || settings.SettingTimestamp != 1710000000 {
		t.Fatalf("expected HandleMessage to backfill (604800, 1710000000); got (%d, %d)",
			settings.Expiration, settings.SettingTimestamp)
	}
}

// --- Integration tests: HandleMessage stores under correct JID ---

func TestHandleMessage_IncomingLIDMessage_StoredUnderPhoneJID(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)

	msg := buildTextMessage(
		phoneLID,       // chat: arrives as LID
		phoneLID,       // sender: LID
		phonePN,        // senderAlt: phone JID (provided by whatsmeow)
		types.EmptyJID, // recipientAlt: not set for incoming
		false,          // isFromMe: incoming
		"Hola, qué tal?",
	)

	newHandler(client, ms).HandleMessage(msg)

	// Message MUST be stored under the phone-based JID.
	if count := testutil.QueryMessageCount(ms, phonePN.String()); count != 1 {
		t.Errorf("expected 1 message under phone JID %s, got %d", phonePN, count)
	}

	// No chat entry should exist for the LID JID.
	if _, found := testutil.QueryChat(ms, phoneLID.String()); found {
		t.Error("LID chat entry should not exist in database")
	}

	// No message should be stored under the LID JID.
	if count := testutil.QueryMessageCount(ms, phoneLID.String()); count != 0 {
		t.Errorf("expected 0 messages under LID JID %s, got %d", phoneLID, count)
	}
}

func TestHandleMessage_OutgoingLIDMessage_StoredUnderPhoneJID(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)

	msg := buildTextMessage(
		phoneLID,       // chat: LID
		phoneLID,       // sender: self (LID)
		types.EmptyJID, // senderAlt: not set for outgoing
		phonePN,        // recipientAlt: phone JID
		true,           // isFromMe: outgoing
		"Todo bien!",
	)

	newHandler(client, ms).HandleMessage(msg)

	if count := testutil.QueryMessageCount(ms, phonePN.String()); count != 1 {
		t.Errorf("expected 1 message under phone JID %s, got %d", phonePN, count)
	}

	if count := testutil.QueryMessageCount(ms, phoneLID.String()); count != 0 {
		t.Errorf("expected 0 messages under LID JID %s, got %d", phoneLID, count)
	}
}

func TestHandleMessage_LIDWithStoreFallback_StoredUnderPhoneJID(t *testing.T) {
	lidStore := &testutil.MockLIDStore{
		PNByLID: map[types.JID]types.JID{phoneLID: phonePN},
	}
	client := testutil.NewClient(lidStore)
	ms := testutil.NewMessageStore(t)

	// No SenderAlt/RecipientAlt -- must resolve via LID store.
	msg := buildTextMessage(
		phoneLID,       // chat: LID
		phoneLID,       // sender: LID
		types.EmptyJID, // senderAlt: empty (simulates missing alt)
		types.EmptyJID, // recipientAlt: empty
		false,          // isFromMe: incoming
		"Message without alt JIDs",
	)

	newHandler(client, ms).HandleMessage(msg)

	if count := testutil.QueryMessageCount(ms, phonePN.String()); count != 1 {
		t.Errorf("expected 1 message under phone JID %s, got %d", phonePN, count)
	}

	if count := testutil.QueryMessageCount(ms, phoneLID.String()); count != 0 {
		t.Errorf("expected 0 messages under LID JID %s, got %d", phoneLID, count)
	}
}

func TestHandleMessage_PhoneJID_Unaffected(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)

	msg := buildTextMessage(
		phonePN,        // chat: already phone-based
		phonePN,        // sender: phone-based
		types.EmptyJID, // senderAlt: empty
		types.EmptyJID, // recipientAlt: empty
		false,          // isFromMe: incoming
		"Normal message",
	)

	newHandler(client, ms).HandleMessage(msg)

	if count := testutil.QueryMessageCount(ms, phonePN.String()); count != 1 {
		t.Errorf("expected 1 message under phone JID %s, got %d", phonePN, count)
	}
}

// --- Sender column resolution ---
//
// These tests guard against the regression where the bridge stored the
// LID user-part (or, for outgoing messages, the recipient's phone) in the
// sender column even after the chat-JID was resolved to a phone JID.

// TestHandleMessage_OutgoingFromSelf_SenderIsOwnPhone asserts that an
// outgoing message from a LID-typed self does not get the recipient's
// phone written into the sender column. Before the fix, ResolveLIDChat
// reused recipientAlt for the sender, mis-attributing self messages.
func TestHandleMessage_OutgoingFromSelf_SenderIsOwnPhone(t *testing.T) {
	client := testutil.NewClientWithSelf(&testutil.MockLIDStore{}, selfPhone)
	ms := testutil.NewMessageStore(t)

	msg := buildTextMessage(
		phoneLID,       // chat: peer LID
		selfLID,        // sender: own LID
		types.EmptyJID, // senderAlt: empty for outgoing
		phonePN,        // recipientAlt: peer phone (NOT self phone)
		true,           // outgoing
		"hi",
	)

	newHandler(client, ms).HandleMessage(msg)

	got := testutil.QuerySender(ms, phonePN.String())
	if got != selfPhone.User {
		t.Errorf("outgoing sender = %q, want own phone user %q (recipient phone is %q, must not appear)",
			got, selfPhone.User, phonePN.User)
	}
}

// TestHandleMessage_IncomingLID_SenderResolvedFromAlt asserts that an
// incoming LID-only sender with a non-empty SenderAlt is rewritten to the
// peer's phone user-part, not stored as the raw LID number.
func TestHandleMessage_IncomingLID_SenderResolvedFromAlt(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)

	msg := buildTextMessage(
		phoneLID,       // chat: LID
		phoneLID,       // sender: peer LID
		phonePN,        // senderAlt: peer phone
		types.EmptyJID, // recipientAlt: unused for incoming
		false,          // incoming
		"hola",
	)

	newHandler(client, ms).HandleMessage(msg)

	got := testutil.QuerySender(ms, phonePN.String())
	if got != phonePN.User {
		t.Errorf("incoming sender = %q, want peer phone user %q", got, phonePN.User)
	}
}

// TestHandleMessage_IncomingLID_SenderResolvedFromStore covers the
// history-sync-style case: SenderAlt is empty but the LID store has a
// PN mapping for the peer LID, so the sender column should still end up
// as the phone user-part.
func TestHandleMessage_IncomingLID_SenderResolvedFromStore(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{
		PNByLID: map[types.JID]types.JID{phoneLID: phonePN},
	})
	ms := testutil.NewMessageStore(t)

	msg := buildTextMessage(
		phoneLID,       // chat: LID
		phoneLID,       // sender: peer LID
		types.EmptyJID, // senderAlt: empty (post-fix, fallback to LID store)
		types.EmptyJID, // recipientAlt: empty
		false,          // incoming
		"hello",
	)

	newHandler(client, ms).HandleMessage(msg)

	got := testutil.QuerySender(ms, phonePN.String())
	if got != phonePN.User {
		t.Errorf("incoming sender = %q, want peer phone user %q (LID store fallback)",
			got, phonePN.User)
	}
}

// TestHandleMessage_LIDWithoutMapping_SenderFallsBackToLID asserts the
// graceful-degradation path: with no SenderAlt and no LID store mapping,
// the bridge stores the raw LID user-part rather than failing or writing
// an unrelated value.
func TestHandleMessage_LIDWithoutMapping_SenderFallsBackToLID(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)

	msg := buildTextMessage(
		phoneLID,       // chat: LID
		phoneLID,       // sender: peer LID
		types.EmptyJID, // senderAlt: empty
		types.EmptyJID, // recipientAlt: empty
		false,          // incoming
		"orphan",
	)

	newHandler(client, ms).HandleMessage(msg)

	// Chat JID has no mapping either, so the message ends up under the LID chat.
	got := testutil.QuerySender(ms, phoneLID.String())
	if got != phoneLID.User {
		t.Errorf("orphan-LID sender = %q, want raw LID user %q (graceful fallback)",
			got, phoneLID.User)
	}
}

// TestHandleMessage_GroupParticipantLID_ResolvedViaStore covers the
// highest-volume path that triggers the LID-sender bug: a group message
// where the participant JID is LID-only and the per-message SenderAlt is
// empty. Resolution must come from the LID store.
func TestHandleMessage_GroupParticipantLID_ResolvedViaStore(t *testing.T) {
	groupJID := types.JID{User: "254110094043-1619359480", Server: types.GroupServer}
	participantLID := types.JID{User: "261391827087520", Server: types.HiddenUserServer}
	participantPhone := types.JID{User: "31612345678", Server: types.DefaultUserServer}

	client := testutil.NewClient(&testutil.MockLIDStore{
		PNByLID: map[types.JID]types.JID{participantLID: participantPhone},
	})
	ms := testutil.NewMessageStore(t)

	// Pre-seed the group chat row so GetChatName short-circuits on the
	// existing-name path and doesn't try to issue a GetGroupInfo IQ
	// against the fake client.
	if err := ms.StoreChat(groupJID.String(), "Test Group", time.Now()); err != nil {
		t.Fatalf("seed group chat: %v", err)
	}

	msg := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     groupJID,
				Sender:   participantLID,
				IsFromMe: false,
				IsGroup:  true,
			},
			ID:        "test-group-001",
			Timestamp: time.Now(),
		},
		Message: &waProto.Message{
			Conversation: proto.String("group hello"),
		},
	}

	newHandler(client, ms).HandleMessage(msg)

	got := testutil.QuerySender(ms, groupJID.String())
	if got != participantPhone.User {
		t.Errorf("group participant sender = %q, want phone user %q", got, participantPhone.User)
	}
}

// --- Revoke ("delete for everyone") tests ---

func TestHandleMessage_RevokeMarksTargetDeleted(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)
	chatJID := phonePN.String()
	targetID := "3A1234567890ABCDEF"

	if _, err := ms.DB.Exec(
		`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		targetID, chatJID, phonePN.User, "secret", time.Unix(1710000000, 0), false,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	revokedAt := time.Unix(1710000010, 0)
	newHandler(client, ms).HandleMessage(revokeEvent(targetID, revokedAt))

	got, valid := readDeletedAt(t, ms, chatJID, targetID)
	if !valid {
		t.Fatalf("expected REVOKE to mark target row as deleted")
	}
	if !got.Equal(revokedAt) {
		t.Fatalf("expected deleted_at=%v, got %v", revokedAt, got)
	}

	var content string
	if err := ms.DB.QueryRow("SELECT content FROM messages WHERE id = ? AND chat_jid = ?",
		targetID, chatJID).Scan(&content); err != nil {
		t.Fatalf("read content: %v", err)
	}
	if content != "secret" {
		t.Fatalf("content must be preserved on revoke, got %q", content)
	}
}

func TestHandleMessage_RevokeIsNoopForUnknownTarget(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)

	// No seeded row — bridge was offline when the original arrived, or it
	// was deleted before this code path shipped. The handler must not
	// error and must not invent a row.
	newHandler(client, ms).HandleMessage(revokeEvent("NEVER_SEEN", time.Unix(1710000010, 0)))

	var rowCount int
	if err := ms.DB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&rowCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("expected no rows in messages, got %d", rowCount)
	}
}

func TestHandleMessage_DuplicateRevokeKeepsEarliestDeletedAt(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)
	chatJID := phonePN.String()
	targetID := "3A1234567890ABCDEF"

	if _, err := ms.DB.Exec(
		`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		targetID, chatJID, phonePN.User, "x", time.Unix(1710000000, 0), false,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	earlier := time.Unix(1710000010, 0)
	later := time.Unix(1710000020, 0)

	h := newHandler(client, ms)
	h.HandleMessage(revokeEvent(targetID, earlier))
	h.HandleMessage(revokeEvent(targetID, later))

	got, valid := readDeletedAt(t, ms, chatJID, targetID)
	if !valid || !got.Equal(earlier) {
		t.Fatalf("expected earlier deleted_at=%v to be preserved across a duplicate revoke, got %v", earlier, got)
	}
}

func TestHandleMessage_ReplayedOriginalPreservesDeletedAt(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)
	chatJID := phonePN.String()
	targetID := "3A1234567890ABCDEF"

	if _, err := ms.DB.Exec(
		`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		targetID, chatJID, phonePN.User, "secret", time.Unix(1710000000, 0), false,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	revokedAt := time.Unix(1710000010, 0)
	h := newHandler(client, ms)
	h.HandleMessage(revokeEvent(targetID, revokedAt))

	replayedOriginal := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: phonePN, Sender: phonePN, IsFromMe: false},
			ID:            targetID,
			Timestamp:     time.Unix(1710000020, 0),
		},
		Message: &waProto.Message{Conversation: proto.String("replayed original")},
	}
	h.HandleMessage(replayedOriginal)

	got, valid := readDeletedAt(t, ms, chatJID, targetID)
	if !valid || !got.Equal(revokedAt) {
		t.Fatalf("expected replayed original to preserve deleted_at=%v, got %v (valid=%v)", revokedAt, got, valid)
	}
}

func TestHandleMessage_RegularMessageDoesNotMarkDeleted(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)
	chatJID := phonePN.String()
	seededID := "PRE_EXISTING_MESSAGE"

	if _, err := ms.DB.Exec(
		`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		seededID, chatJID, phonePN.User, "still here", time.Unix(1710000000, 0), false,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A regular text message arriving in the same chat must not touch
	// deleted_at on any existing row, and must not mark itself deleted.
	regular := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: phonePN, Sender: phonePN, IsFromMe: false},
			ID:            "REGULAR_MSG",
			Timestamp:     time.Unix(1710000010, 0),
		},
		Message: &waProto.Message{Conversation: proto.String("just a normal hello")},
	}
	newHandler(client, ms).HandleMessage(regular)

	if _, valid := readDeletedAt(t, ms, chatJID, seededID); valid {
		t.Fatalf("regular message must not flip deleted_at on the pre-existing row")
	}
	if _, valid := readDeletedAt(t, ms, chatJID, "REGULAR_MSG"); valid {
		t.Fatalf("regular message must not flip its own deleted_at")
	}
}

// --- Quoted-reply tests ---

// TestHandleMessage_QuotedReply_IDPersisted verifies that an inbound
// ExtendedTextMessage reply that carries a ContextInfo.StanzaID has its
// quoted_message_id column populated correctly.
func TestHandleMessage_QuotedReply_IDPersisted(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)
	chatJID := phonePN.String()
	targetID := "3AORIGINAL1234567"
	replyID := "3AREPLY0000000001"

	msg := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     phonePN,
				Sender:   phonePN,
				IsFromMe: false,
			},
			ID:        replyID,
			Timestamp: time.Now(),
		},
		Message: &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String("Great point!"),
				ContextInfo: &waProto.ContextInfo{
					StanzaID:      proto.String(targetID),
					Participant:   proto.String(phonePN.String()),
					QuotedMessage: &waProto.Message{Conversation: proto.String("original text")},
				},
			},
		},
	}

	newHandler(client, ms).HandleMessage(msg)

	quotedID, valid := queryQuotedMessageID(ms, chatJID, replyID)
	if !valid {
		t.Fatalf("expected quoted_message_id to be set, got NULL")
	}
	if quotedID != targetID {
		t.Errorf("quoted_message_id = %q, want %q", quotedID, targetID)
	}
}

// TestHandleMessage_PlainMessage_QuotedIDIsNull verifies that a plain
// Conversation message (no ContextInfo) has a NULL quoted_message_id.
func TestHandleMessage_PlainMessage_QuotedIDIsNull(t *testing.T) {
	client := testutil.NewClient(&testutil.MockLIDStore{})
	ms := testutil.NewMessageStore(t)
	chatJID := phonePN.String()
	msgID := "3APLAIN0000000001"

	msg := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     phonePN,
				Sender:   phonePN,
				IsFromMe: false,
			},
			ID:        msgID,
			Timestamp: time.Now(),
		},
		Message: &waProto.Message{
			Conversation: proto.String("just a plain message"),
		},
	}

	newHandler(client, ms).HandleMessage(msg)

	_, valid := queryQuotedMessageID(ms, chatJID, msgID)
	if valid {
		t.Fatalf("plain message must have NULL quoted_message_id")
	}
}
