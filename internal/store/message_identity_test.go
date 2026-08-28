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

func TestMessageIdentityResolvesReplyTargetWithoutInferringAdjacency(t *testing.T) {
	identity := messageIdentity{
		contacts:       map[string]Contact{"wxid_member": {Username: "wxid_member", Nickname: "用户昵称", Display: "联系人显示名"}},
		groupNicknames: map[string]string{"wxid_member": "群昵称"},
	}
	resolved := Message{Chat: "group@chatroom", ReplyTo: &MessageReply{ToUsername: "wxid_member"}}
	identity.enrich(&resolved, 4)
	if resolved.ReplyTo.IdentityStatus != "resolved" || resolved.ReplyTo.ToName != "群昵称" {
		t.Fatalf("引用对象身份解析错误：%+v", resolved.ReplyTo)
	}
	unresolved := Message{Chat: "group@chatroom", ReplyTo: &MessageReply{ToUsername: "wxid_unknown", ToName: "保留显示名"}}
	identity.enrich(&unresolved, 4)
	if unresolved.ReplyTo.IdentityStatus != "unresolved" || unresolved.ReplyTo.ToName != "保留显示名" {
		t.Fatalf("未解析引用对象未保留原始证据：%+v", unresolved.ReplyTo)
	}
	displayOnly := Message{Chat: "group@chatroom", ReplyTo: &MessageReply{ToName: "只有显示名"}}
	identity.enrich(&displayOnly, 4)
	if displayOnly.ReplyTo.IdentityStatus != "display_only" {
		t.Fatalf("仅显示名引用对象状态异常：%+v", displayOnly.ReplyTo)
	}
}

func TestMessageIdentityMarksSystemMessagesExplicitly(t *testing.T) {
	identity := messageIdentity{
		contacts:       map[string]Contact{"wxid_member": {Username: "wxid_member", Display: "不应成为系统消息说话人"}},
		groupNicknames: map[string]string{"wxid_member": "群昵称"},
	}
	message := Message{Chat: "group@chatroom", Kind: "system", IsSystem: true, SenderUsername: "wxid_member"}
	identity.enrich(&message, 2)
	if message.SenderIdentity != "system" || message.IsFromMe || message.Sender != "系统" {
		t.Fatalf("系统消息身份异常：%+v", message)
	}
}

func appendProtoText(target []byte, field byte, value string) []byte {
	return appendProtoBytes(target, field, []byte(value))
}

func appendProtoBytes(target []byte, field byte, value []byte) []byte {
	target = append(target, field<<3|2, byte(len(value)))
	return append(target, value...)
}
