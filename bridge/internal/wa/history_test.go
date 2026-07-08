package wa_test

import (
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-mcp/bridge/internal/testutil"
)

// TestHandleHistorySync_LIDParticipant_ResolvedViaStore exercises the
// history-sync code path. Because history-sync rows do not carry SenderAlt,
// resolution must succeed via the LID store fallback that
// ResolveUserJID consults. The stored sender column must be the phone
// user-part, not the raw LID number copied verbatim from Key.Participant.
func TestHandleHistorySync_LIDParticipant_ResolvedViaStore(t *testing.T) {
	chatJID := phonePN.String() // history-sync conversation already keyed by phone
	participantLID := types.JID{User: "445566778899", Server: types.HiddenUserServer}
	participantPhone := types.JID{User: "11234567890", Server: types.DefaultUserServer}

	client := testutil.NewClientWithSelf(&testutil.MockLIDStore{
		PNByLID: map[types.JID]types.JID{participantLID: participantPhone},
	}, selfPhone)
	ms := testutil.NewMessageStore(t)

	historySync := &events.HistorySync{
		Data: &waProto.HistorySync{
			SyncType: waProto.HistorySync_RECENT.Enum(),
			Conversations: []*waProto.Conversation{
				{
					ID: proto.String(chatJID),
					Messages: []*waProto.HistorySyncMsg{
						{
							Message: &waProto.WebMessageInfo{
								Key: &waCommon.MessageKey{
									ID:          proto.String("hist-msg-001"),
									FromMe:      proto.Bool(false),
									Participant: proto.String(participantLID.String()),
								},
								MessageTimestamp: proto.Uint64(uint64(time.Now().Unix())),
								Message: &waProto.Message{
									Conversation: proto.String("history payload"),
								},
							},
						},
					},
				},
			},
		},
	}

	newHandler(client, ms).HandleHistorySync(historySync)

	got := testutil.QuerySender(ms, chatJID)
	if got != participantPhone.User {
		t.Errorf("history-sync sender = %q, want resolved phone user %q (raw LID was %q)",
			got, participantPhone.User, participantLID.User)
	}
}
