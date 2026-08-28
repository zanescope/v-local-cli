package store

import (
	"path/filepath"
	"testing"
)

func createQueryExtensionDB(t *testing.T, path string, statements ...string) {
	t.Helper()
	if err := ensureParent(path); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, path, statements...)
}

func TestSessionsUnreadAndStructuredSummary(t *testing.T) {
	root := t.TempDir()
	createQueryExtensionDB(t, filepath.Join(root, "contact", "contact.db"),
		"CREATE TABLE contact(username TEXT, alias TEXT, remark TEXT, nick_name TEXT, verify_flag INTEGER)",
		"INSERT INTO contact VALUES('alice','','阿丽','Alice',0)",
		"INSERT INTO contact VALUES('service_account','','服务号','服务号',8)",
	)
	createQueryExtensionDB(t, filepath.Join(root, "session", "session.db"),
		"CREATE TABLE SessionTable(username TEXT, unread_count INTEGER, summary BLOB, last_timestamp INTEGER, last_msg_type INTEGER, last_msg_sender TEXT, last_sender_display_name TEXT)",
		"INSERT INTO SessionTable VALUES('alice',2,'你好',1700000000,1,'alice','')",
		"INSERT INTO SessionTable VALUES('quiet',0,'无未读',1600000000,1,'quiet','')",
		"INSERT INTO SessionTable VALUES('service_account',1,'服务通知',1500000000,1,'service_account','')",
	)
	report, err := Sessions(root, true, "person", 20)
	if err != nil || len(report.Items) != 1 {
		t.Fatalf("sessions 异常：report=%+v err=%v", report, err)
	}
	if report.Items[0].Username != "alice" || report.Items[0].Display != "阿丽" || report.Items[0].UnreadCount != 2 || report.Items[0].LastSummary != "你好" {
		t.Fatalf("session 字段异常：%+v", report.Items[0])
	}
	if report.Coverage["status"] != "complete" {
		t.Fatalf("session coverage 异常：%v", report.Coverage)
	}
	official, err := Sessions(root, false, "official", 20)
	if err != nil || len(official.Items) != 1 || official.Items[0].Username != "service_account" {
		t.Fatalf("verify_flag 公众号分类异常：report=%+v err=%v", official, err)
	}
}

func TestMembersPreferNormalizedTable(t *testing.T) {
	root := t.TempDir()
	createQueryExtensionDB(t, filepath.Join(root, "contact", "contact.db"),
		"CREATE TABLE contact(id INTEGER, username TEXT, alias TEXT, remark TEXT, nick_name TEXT)",
		"INSERT INTO contact VALUES(1,'room@chatroom','','项目群','项目群')",
		"INSERT INTO contact VALUES(2,'alice','','阿丽','Alice')",
		"INSERT INTO contact VALUES(3,'bob','','','Bob')",
		"CREATE TABLE chat_room(id INTEGER, username TEXT, owner TEXT, ext_buffer BLOB)",
		"INSERT INTO chat_room VALUES(10,'room@chatroom','alice',X'')",
		"CREATE TABLE chatroom_member(room_id INTEGER, member_id INTEGER)",
		"INSERT INTO chatroom_member VALUES(10,2)",
		"INSERT INTO chatroom_member VALUES(10,3)",
	)
	report, err := Members(root, "room@chatroom")
	if err != nil || len(report.Items) != 2 {
		t.Fatalf("members 异常：report=%+v err=%v", report, err)
	}
	if report.Coverage["status"] != "complete" || report.Coverage["method"] != "chatroom_member_table" || !report.Items[0].IsOwner || report.Items[0].Username != "alice" {
		t.Fatalf("members coverage/owner 异常：%+v", report)
	}
}

func TestFavoritesAndSafeURL(t *testing.T) {
	root := t.TempDir()
	createQueryExtensionDB(t, filepath.Join(root, "favorite", "favorite.db"),
		"CREATE TABLE fav_db_item(local_id INTEGER, type INTEGER, update_time INTEGER, content TEXT, fromusr TEXT, realchatname TEXT)",
		`INSERT INTO fav_db_item VALUES(7,5,1700000000000,'<msg><title>收藏文章</title><digest>正文关键词</digest><url>https://example.com/read?id=1</url></msg>','alice','room')`,
		`INSERT INTO fav_db_item VALUES(8,5,1700000001,'<msg><title>危险</title><url>file:///tmp/secret</url></msg>','alice','room')`,
	)
	report, err := Favorites(root, "关键词", "article", 20)
	if err != nil || len(report.Items) != 1 {
		t.Fatalf("favorites 异常：report=%+v err=%v", report, err)
	}
	item := report.Items[0]
	if item.Title != "收藏文章" || item.URL != "https://example.com/read?id=1" || item.Timestamp != 1700000000 || item.EvidenceID != "wechat:favorite:7" {
		t.Fatalf("favorite 字段异常：%+v", item)
	}
}

func TestResolveContactRejectsAmbiguousDisplay(t *testing.T) {
	root := t.TempDir()
	createQueryExtensionDB(t, filepath.Join(root, "contact", "contact.db"),
		"CREATE TABLE contact(username TEXT, alias TEXT, remark TEXT, nick_name TEXT)",
		"INSERT INTO contact VALUES('alice','','同名','Alice')",
		"INSERT INTO contact VALUES('bob','','同名','Bob')",
	)
	if _, err := ResolveContact(root, "同名"); err == nil {
		t.Fatal("同名联系人应返回歧义")
	} else if ambiguous, ok := err.(*AmbiguousContactError); !ok || len(ambiguous.Candidates) != 2 {
		t.Fatalf("歧义候选异常：%T %+v", err, err)
	}
	match, err := ResolveContact(root, "alice")
	if err != nil || match.Contact.Username != "alice" || match.Field != "username" {
		t.Fatalf("精确 username 解析异常：match=%+v err=%v", match, err)
	}
	if _, err := ResolveContact(root, "同"); err == nil {
		t.Fatal("单字符模糊输入不应自动解析联系人")
	}
	if _, err := ResolveContact(root, "---"); err == nil {
		t.Fatal("纯分隔符输入不应自动解析联系人")
	}
}
