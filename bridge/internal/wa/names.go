package wa

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"whatsapp-mcp/bridge/internal/store"
)

// GetChatName determines the appropriate name for a chat based on JID and other info.
//
// allowNetwork controls whether an unknown group name may be resolved by
// asking WhatsApp. History sync passes false: it delivers conversations in
// bulk, so one query per unnamed group becomes a burst of metadata requests —
// issued from whatsmeow's serialized event goroutine, which stalls every other
// event behind it. The placeholder name it falls back to is replaced the first
// time a live message arrives in that group.
func GetChatName(client *whatsmeow.Client, messageStore *store.MessageStore, jid types.JID, chatJID string, conversation interface{}, sender string, allowNetwork bool, logger waLog.Logger) string {
	// First, check if chat already exists in database with a name.
	// A stored placeholder does not count as a name when we are allowed to
	// look one up: history sync writes placeholders for groups precisely
	// because it must not make network calls, and this is where they get
	// upgraded to the real thing.
	if existingName := messageStore.ChatName(chatJID); existingName != "" {
		if !(allowNetwork && existingName == groupPlaceholderName(jid)) {
			logger.Infof("Using existing chat name for %s: %s", chatJID, existingName)
			return existingName
		}
	}

	// Need to determine chat name
	var name string

	if jid.Server == "g.us" {
		// This is a group chat
		logger.Infof("Getting name for group: %s", chatJID)

		// Use conversation data if provided (from history sync)
		if conversation != nil {
			// Extract name from conversation if available
			// This uses type assertions to handle different possible types
			var displayName, convName *string
			// Try to extract the fields we care about regardless of the exact type
			v := reflect.ValueOf(conversation)
			if v.Kind() == reflect.Pointer && !v.IsNil() {
				v = v.Elem()

				// Try to find DisplayName field
				if displayNameField := v.FieldByName("DisplayName"); displayNameField.IsValid() && displayNameField.Kind() == reflect.Pointer && !displayNameField.IsNil() {
					dn := displayNameField.Elem().String()
					displayName = &dn
				}

				// Try to find Name field
				if nameField := v.FieldByName("Name"); nameField.IsValid() && nameField.Kind() == reflect.Pointer && !nameField.IsNil() {
					n := nameField.Elem().String()
					convName = &n
				}
			}

			// Use the name we found
			if displayName != nil && *displayName != "" {
				name = *displayName
			} else if convName != nil && *convName != "" {
				name = *convName
			}
		}

		// If we didn't get a name, try group info
		if name == "" && allowNetwork {
			groupInfo, err := client.GetGroupInfo(context.Background(), jid)
			if err == nil && groupInfo.Name != "" {
				name = groupInfo.Name
			}
		}
		if name == "" {
			// Fallback name for groups
			name = groupPlaceholderName(jid)
		}

		logger.Infof("Using group name: %s", name)
	} else {
		// This is an individual contact
		logger.Infof("Getting name for contact: %s", chatJID)

		// Use contact info (full name)
		contact, err := client.Store.Contacts.GetContact(context.Background(), jid)
		if err == nil && contact.FullName != "" {
			name = contact.FullName
		} else {
			name = lookupLocalContactName(client, messageStore, chatJID, logger)

			if name == "" {
				if sender != "" {
					name = sender
				} else {
					name = jid.User
				}
			}
		}

		logger.Infof("Using contact name: %s", name)
	}

	return name
}

// groupPlaceholderName is the stand-in stored for a group whose real name is
// not known yet. Recognisable on sight so a later, network-allowed lookup can
// tell it apart from a name the user actually sees.
func groupPlaceholderName(jid types.JID) string {
	return fmt.Sprintf("Group %s", jid.User)
}

func lookupLocalContactName(client *whatsmeow.Client, messageStore *store.MessageStore, chatJID string, logger waLog.Logger) string {
	if client == nil || client.Store == nil || client.Store.ID == nil || messageStore == nil || messageStore.WaDB == nil {
		return ""
	}

	var localName string
	err := messageStore.WaDB.QueryRow(
		`SELECT COALESCE(
			NULLIF(full_name, ''),
			NULLIF(push_name, ''),
			NULLIF(first_name, ''),
			NULLIF(business_name, ''),
			''
		) FROM whatsmeow_contacts WHERE our_jid = ? AND their_jid = ?`,
		client.Store.ID.String(),
		chatJID,
	).Scan(&localName)
	if err == nil {
		if localName != "" {
			logger.Infof("Using local contact name for %s: %s", chatJID, localName)
		}
		return localName
	}
	if err != sql.ErrNoRows && !strings.Contains(err.Error(), "no such table: whatsmeow_contacts") {
		logger.Warnf("Failed to query local contact name for %s: %v", chatJID, err)
	}
	return ""
}
