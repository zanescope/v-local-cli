package store

import "testing"

func TestParseChatRoomMembers(t *testing.T) {
	member := appendProtoText(nil, 1, "wxid_member")
	member = appendProtoText(member, 2, "群昵称")
	room := appendProtoBytes(nil, 1, member)
	items := parseChatRoomMembers(room)
	if items["wxid_member"] != "群昵称" {
		t.Fatalf("群成员 protobuf 解析错误：%+v", items)
	}
}

func TestMessageIdentityDistinguishesSelfAndContact(t *testing.T) {
	identity := messageIdentity{
		contacts:       map[string]Contact{"wxid_member": {Username: "wxid_member", Nickname: "用户昵称", Display: "联系人显示名"}},
		groupNicknames: map[string]string{"wxid_member": "群昵称"},
	}
	contact := Message{Chat: "group@chatroom", SenderUsername: "wxid_member"}
	identity.enrich(&contact, 4)
	if contact.SenderIdentity != "contact" || contact.IsFromMe || contact.Sender != "群昵称" || contact.SenderNickname != "用户昵称" || contact.SenderGroupNickname != "群昵称" {
		t.Fatalf("群成员身份解析错误：%+v", contact)
	}
	self := Message{Chat: "group@chatroom"}
	identity.enrich(&self, 2)
	if self.SenderIdentity != "self" || !self.IsFromMe || self.Sender != "我" {
		t.Fatalf("我的身份解析错误：%+v", self)
	}
}

func appendProtoText(target []byte, field byte, value string) []byte {
	return appendProtoBytes(target, field, []byte(value))
}

func appendProtoBytes(target []byte, field byte, value []byte) []byte {
	target = append(target, field<<3|2, byte(len(value)))
	return append(target, value...)
}
