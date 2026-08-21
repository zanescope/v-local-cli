package store

import (
	"encoding/binary"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type messageIdentity struct {
	contacts       map[string]Contact
	groupNicknames map[string]string
}

func loadMessageIdentity(root, chat string) messageIdentity {
	identity := messageIdentity{contacts: loadContactIdentity(root), groupNicknames: map[string]string{}}
	if strings.HasSuffix(chat, "@chatroom") {
		identity.groupNicknames = loadGroupNicknames(root, chat)
	}
	return identity
}

func loadContactIdentity(root string) map[string]Contact {
	result := map[string]Contact{}
	if contacts, err := Contacts(root, "", 0); err == nil {
		for _, contact := range contacts {
			result[contact.Username] = contact
		}
	}
	return result
}

func loadGroupNicknames(root, chat string) map[string]string {
	result := map[string]string{}
	files, err := sqliteFiles(root)
	if err != nil {
		return result
	}
	for _, path := range files {
		if !strings.EqualFold(filepath.Base(path), "contact.db") {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		var payload []byte
		queryErr := database.QueryRow("SELECT ext_buffer FROM chat_room WHERE username=? LIMIT 1", chat).Scan(&payload)
		_ = database.Close()
		if queryErr == nil {
			result = parseChatRoomMembers(payload)
			break
		}
	}
	return result
}

func parseChatRoomMembers(payload []byte) map[string]string {
	result := map[string]string{}
	for _, member := range parseChatRoomMemberList(payload) {
		if member.Display != "" {
			result[member.Username] = member.Display
		}
	}
	return result
}

func nextProtoField(payload []byte, offset int) (int, int, []byte, int, bool) {
	if offset < 0 || offset >= len(payload) {
		return 0, 0, nil, offset, false
	}
	key, size := binary.Uvarint(payload[offset:])
	if size <= 0 || key>>3 == 0 {
		return 0, 0, nil, offset, false
	}
	field, wire := int(key>>3), int(key&7)
	offset += size
	switch wire {
	case 0:
		_, valueSize := binary.Uvarint(payload[offset:])
		if valueSize <= 0 {
			return 0, 0, nil, offset, false
		}
		return field, wire, nil, offset + valueSize, true
	case 1:
		if offset+8 > len(payload) {
			return 0, 0, nil, offset, false
		}
		return field, wire, payload[offset : offset+8], offset + 8, true
	case 2:
		length, lengthSize := binary.Uvarint(payload[offset:])
		if lengthSize <= 0 || length > uint64(len(payload)-offset-lengthSize) {
			return 0, 0, nil, offset, false
		}
		start := offset + lengthSize
		end := start + int(length)
		return field, wire, payload[start:end], end, true
	case 5:
		if offset+4 > len(payload) {
			return 0, 0, nil, offset, false
		}
		return field, wire, payload[offset : offset+4], offset + 4, true
	default:
		return 0, 0, nil, offset, false
	}
}

func validProtoText(value []byte) bool {
	if len(value) == 0 || !utf8.Valid(value) {
		return false
	}
	for _, current := range string(value) {
		if unicode.IsControl(current) && !unicode.IsSpace(current) {
			return false
		}
	}
	return true
}

func (identity messageIdentity) enrich(message *Message, status int64) {
	username := message.SenderUsername
	message.IsFromMe = status == 2 || (!strings.HasSuffix(message.Chat, "@chatroom") && username != "" && username != message.Chat)
	message.SenderIdentity = "contact"
	if message.IsFromMe {
		message.SenderIdentity = "self"
	} else if username == "" {
		message.SenderIdentity = "unknown"
	}
	if contact, found := identity.contacts[username]; found {
		message.SenderNickname = contact.Nickname
		message.SenderRemark = contact.Remark
		message.SenderContactDisplay = contact.Display
	}
	message.SenderGroupNickname = identity.groupNicknames[username]
	message.Sender = firstNonEmpty(message.SenderGroupNickname, message.SenderContactDisplay, message.SenderNickname, username)
	if message.IsFromMe && message.Sender == "" {
		message.Sender = "我"
	}
	for index, mention := range message.Mentions {
		if mention == "所有人" {
			continue
		}
		if groupName := identity.groupNicknames[mention]; groupName != "" {
			message.Mentions[index] = groupName + "（" + mention + "）"
		} else if contact, found := identity.contacts[mention]; found {
			message.Mentions[index] = contact.Display + "（" + mention + "）"
		}
	}
}
