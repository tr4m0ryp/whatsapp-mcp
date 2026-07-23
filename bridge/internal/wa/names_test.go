package wa_test

import (
	"database/sql"
	"testing"

	"go.mau.fi/whatsmeow/types"

	"whatsapp-mcp/bridge/internal/testutil"
	"whatsapp-mcp/bridge/internal/wa"
)

func TestGetChatName_LocalContactFallbackScopesToActiveAccount(t *testing.T) {
	activeSelf := types.JID{User: "15550000001", Server: types.DefaultUserServer}
	otherSelf := types.JID{User: "15550000002", Server: types.DefaultUserServer}

	client := testutil.NewClientWithSelf(&testutil.MockLIDStore{}, activeSelf)
	ms := testutil.NewMessageStore(t)
	logger := testutil.Logger()

	waDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open whatsmeow db: %v", err)
	}
	t.Cleanup(func() { _ = waDB.Close() })
	ms.WaDB = waDB

	if _, err := waDB.Exec(`
		CREATE TABLE whatsmeow_contacts (
			our_jid TEXT,
			their_jid TEXT,
			first_name TEXT,
			full_name TEXT,
			push_name TEXT,
			business_name TEXT,
			PRIMARY KEY (our_jid, their_jid)
		);
		INSERT INTO whatsmeow_contacts (our_jid, their_jid, first_name, full_name, push_name, business_name)
			VALUES (?, ?, 'Wrong', 'Wrong Account', '', '');
		INSERT INTO whatsmeow_contacts (our_jid, their_jid, first_name, full_name, push_name, business_name)
			VALUES (?, ?, 'Active First', '', 'Active Push', 'Active Business');
	`, otherSelf.String(), phonePN.String(), activeSelf.String(), phonePN.String()); err != nil {
		t.Fatalf("seed whatsmeow contacts: %v", err)
	}

	got := wa.GetChatName(client, ms, phonePN, phonePN.String(), nil, "Sender Fallback", true, logger)
	if got != "Active Push" {
		t.Fatalf("GetChatName() = %q, want active account contact name", got)
	}
}

func TestGetChatName_LocalContactFallbackMissingTableFallsBack(t *testing.T) {
	client := testutil.NewClientWithSelf(&testutil.MockLIDStore{}, selfPhone)
	ms := testutil.NewMessageStore(t)
	logger := testutil.Logger()

	waDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open whatsmeow db: %v", err)
	}
	t.Cleanup(func() { _ = waDB.Close() })
	ms.WaDB = waDB

	got := wa.GetChatName(client, ms, phonePN, phonePN.String(), nil, "Sender Fallback", true, logger)
	if got != "Sender Fallback" {
		t.Fatalf("GetChatName() = %q, want sender fallback", got)
	}
}
