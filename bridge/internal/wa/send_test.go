package wa_test

import (
	"testing"

	"go.mau.fi/whatsmeow/types"

	"whatsapp-mcp/bridge/internal/testutil"
	"whatsapp-mcp/bridge/internal/wa"
)

func TestResolveRecipientJIDResolvesPhoneToCachedLID(t *testing.T) {
	phoneJID := types.JID{User: "15551234567", Server: types.DefaultUserServer}
	lidJID := types.JID{User: "123456789012345", Server: types.HiddenUserServer}
	client := testutil.NewClient(&testutil.MockLIDStore{
		LIDByPN: map[types.JID]types.JID{phoneJID: lidJID},
	})

	got, err := wa.ResolveRecipientJID(client, phoneJID.User)
	if err != nil {
		t.Fatalf("ResolveRecipientJID returned error: %v", err)
	}

	if got != lidJID {
		t.Fatalf("expected cached LID %s, got %s", lidJID, got)
	}
}
