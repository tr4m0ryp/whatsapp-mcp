package wa_test

import (
	"testing"

	"go.mau.fi/whatsmeow/types"

	"whatsapp-mcp/bridge/internal/wa"
)

// TestCallChatJID_Precedence pins down the precedence rules in CallChatJID:
//
//  1. GroupJID wins (group calls always key on the group)
//  2. CallCreator wins over From (Accept events arrive with From=accepter's
//     JID, which is "us" if the user picked up on their phone)
//  3. From is the last-resort fallback
//
// Without rule 2, Accept UPDATEs miss the row stored at Offer time and the
// state machine falls through to "missed" when the user answered elsewhere.
func TestCallChatJID_Precedence(t *testing.T) {
	groupJID := types.JID{User: "120363012345678901", Server: types.GroupServer}
	creatorJID := types.JID{User: "11234567890", Server: types.DefaultUserServer}
	fromJID := types.JID{User: "19998887777", Server: types.DefaultUserServer}

	cases := []struct {
		name string
		meta types.BasicCallMeta
		want string
	}{
		{
			name: "group JID wins when present",
			meta: types.BasicCallMeta{
				GroupJID:    groupJID,
				CallCreator: creatorJID,
				From:        fromJID,
			},
			want: groupJID.String(),
		},
		{
			name: "creator wins over From for 1:1 (Accept-from-other-device case)",
			meta: types.BasicCallMeta{
				CallCreator: creatorJID,
				From:        fromJID,
			},
			want: creatorJID.ToNonAD().String(),
		},
		{
			name: "From is fallback when creator is empty",
			meta: types.BasicCallMeta{
				From: fromJID,
			},
			want: fromJID.ToNonAD().String(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wa.CallChatJID(tc.meta)
			if got != tc.want {
				t.Errorf("CallChatJID() = %q, want %q", got, tc.want)
			}
		})
	}
}
