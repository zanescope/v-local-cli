package store

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
)

type GroupMember struct {
	Username       string `json:"username"`
	Nickname       string `json:"nickname,omitempty"`
	Remark         string `json:"remark,omitempty"`
	ContactDisplay string `json:"contact_display,omitempty"`
	GroupNickname  string `json:"group_nickname,omitempty"`
	Display        string `json:"display"`
	IsOwner        bool   `json:"is_owner,omitempty"`
	ObservedOnly   bool   `json:"observed_only,omitempty"`
}

type MemberReport struct {
	Chat     string         `json:"chat"`
	Items    []GroupMember  `json:"items"`
	Coverage map[string]any `json:"member_source_coverage"`
}

type protoGroupMember struct {
	Username string
	Display  string
}

func parseChatRoomMemberList(payload []byte) []protoGroupMember {
	seen := map[string]bool{}
	var result []protoGroupMember
	for offset := 0; offset < len(payload); {
		field, wire, value, next, ok := nextProtoField(payload, offset)
		if !ok {
			break
		}
		offset = next
		if field != 1 || wire != 2 {
			continue
		}
		member := protoGroupMember{}
		for memberOffset := 0; memberOffset < len(value); {
			memberField, memberWire, memberValue, memberNext, memberOK := nextProtoField(value, memberOffset)
			if !memberOK {
				break
			}
			memberOffset = memberNext
			if memberWire != 2 || !validProtoText(memberValue) {
				continue
			}
			switch memberField {
			case 1:
				member.Username = strings.TrimSpace(string(memberValue))
			case 2:
				member.Display = strings.TrimSpace(string(memberValue))
			}
		}
		if member.Username != "" && !seen[member.Username] {
			seen[member.Username] = true
			result = append(result, member)
		}
	}
	return result
}

func chatRoomRecord(database *sql.DB, chat string) (int64, string, []byte) {
	if !tableExists(database, "chat_room") {
		return 0, "", nil
	}
	available := columns(database, "chat_room")
	nameColumn := ""
	for _, candidate := range []string{"username", "chat_room_name", "name"} {
		if available[candidate] {
			nameColumn = candidate
			break
		}
	}
	if nameColumn == "" {
		return 0, "", nil
	}
	selected := selectedColumns(available, "id", "owner", "ext_buffer")
	if len(selected) == 0 {
		return 0, "", nil
	}
	query := "SELECT " + strings.Join(selected, ",") + " FROM " + quoteIdentifier("chat_room") + " WHERE " + quoteIdentifier(nameColumn) + "=? LIMIT 1"
	row := database.QueryRow(query, chat)
	values := make([]any, len(selected))
	targets := make([]any, len(selected))
	for index := range values {
		targets[index] = &values[index]
	}
	if row.Scan(targets...) != nil {
		return 0, "", nil
	}
	fields := map[string]any{}
	for index, name := range selected {
		fields[name] = values[index]
	}
	var payload []byte
	switch value := fields["ext_buffer"].(type) {
	case []byte:
		payload = value
	case string:
		payload = []byte(value)
	}
	return asInt64(fields["id"]), strings.TrimSpace(asString(fields["owner"])), payload
}

func memberValue(contact Contact, groupNickname, owner string, observed bool) GroupMember {
	display := firstNonEmpty(groupNickname, contact.Display, contact.Nickname, contact.Username)
	return GroupMember{
		Username: contact.Username, Nickname: contact.Nickname, Remark: contact.Remark,
		ContactDisplay: contact.Display, GroupNickname: groupNickname, Display: display,
		IsOwner: contact.Username != "" && contact.Username == owner, ObservedOnly: observed,
	}
}

func normalizedMembers(database *sql.DB, roomID int64, owner string, groupNames map[string]string) ([]GroupMember, bool) {
	if roomID == 0 || !tableExists(database, "chatroom_member") || !tableExists(database, "contact") {
		return nil, false
	}
	membersColumns, contactColumns := columns(database, "chatroom_member"), columns(database, "contact")
	if !membersColumns["room_id"] || !membersColumns["member_id"] || !contactColumns["id"] || !contactColumns["username"] {
		return nil, false
	}
	selectedContacts := selectedColumns(contactColumns, "username", "alias", "remark", "nick_name")
	selectParts := make([]string, len(selectedContacts))
	for index, name := range selectedContacts {
		selectParts[index] = "c." + quoteIdentifier(name)
	}
	query := "SELECT " + strings.Join(selectParts, ",") + " FROM " + quoteIdentifier("chatroom_member") + " cm JOIN " + quoteIdentifier("contact") + " c ON c." + quoteIdentifier("id") + "=cm." + quoteIdentifier("member_id") + " WHERE cm." + quoteIdentifier("room_id") + "=?"
	rows, err := database.Query(query, roomID)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	var raw []Contact
	if err := scanDynamicRows(rows, selectedContacts, func(fields map[string]any) {
		username := strings.TrimSpace(asString(fields["username"]))
		if username == "" {
			return
		}
		contact := Contact{Username: username, Alias: asString(fields["alias"]), Remark: asString(fields["remark"]), Nickname: asString(fields["nick_name"])}
		contact.Display = firstNonEmpty(contact.Remark, contact.Nickname, contact.Username)
		contact.Kind = contactKind(username)
		raw = append(raw, contact)
	}); err != nil {
		return nil, false
	}
	var result []GroupMember
	for _, contact := range raw {
		result = append(result, memberValue(contact, groupNames[contact.Username], owner, false))
	}
	return result, len(result) > 0
}

func Members(root, chat string) (MemberReport, error) {
	report := MemberReport{Chat: chat, Coverage: map[string]any{"complete": false, "method": "none"}}
	if !strings.HasSuffix(chat, "@chatroom") {
		report.Coverage["reason"] = "not_group_chat"
		return report, nil
	}
	contacts := loadContactIdentity(root)
	files, err := sqliteFiles(root)
	if err != nil {
		return report, err
	}
	for _, path := range files {
		if !strings.EqualFold(filepath.Base(path), "contact.db") {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		roomID, owner, payload := chatRoomRecord(database, chat)
		groupNames := map[string]string{}
		for _, member := range parseChatRoomMemberList(payload) {
			groupNames[member.Username] = member.Display
		}
		if values, ok := normalizedMembers(database, roomID, owner, groupNames); ok {
			_ = database.Close()
			report.Items = values
			report.Coverage = map[string]any{"status": "complete", "method": "chatroom_member_table", "observed_only": false}
			sortMembers(report.Items)
			return report, nil
		}
		_ = database.Close()
		if members := parseChatRoomMemberList(payload); len(members) > 0 {
			for _, raw := range members {
				contact, found := contacts[raw.Username]
				if !found {
					contact = Contact{Username: raw.Username, Display: raw.Username, Kind: contactKind(raw.Username)}
				}
				report.Items = append(report.Items, memberValue(contact, raw.Display, owner, false))
			}
			report.Coverage = map[string]any{"status": "complete", "method": "chat_room_ext_buffer", "observed_only": false}
			sortMembers(report.Items)
			return report, nil
		}
	}
	messages, historyErr := HistoryWindow(root, chat, nil, nil, 0)
	if historyErr != nil {
		return report, historyErr
	}
	seen := map[string]bool{}
	for _, message := range messages {
		username := message.SenderUsername
		if username == "" || seen[username] || message.IsFromMe {
			continue
		}
		seen[username] = true
		contact, found := contacts[username]
		if !found {
			contact = Contact{Username: username, Display: username, Kind: contactKind(username)}
		}
		report.Items = append(report.Items, memberValue(contact, message.SenderGroupNickname, "", true))
	}
	report.Coverage = map[string]any{"status": "partial", "method": "observed_message_senders", "observed_only": true}
	sortMembers(report.Items)
	return report, nil
}

func sortMembers(values []GroupMember) {
	sort.Slice(values, func(left, right int) bool {
		if values[left].IsOwner != values[right].IsOwner {
			return values[left].IsOwner
		}
		return strings.ToLower(values[left].Display) < strings.ToLower(values[right].Display)
	})
}
