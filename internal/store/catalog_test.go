package store

import (
	"path/filepath"
	"testing"
)

func TestWalkMessagesRecoversHistoryOnlyChatFromName2ID(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "message", "message_0.db")
	if err := ensureParent(path); err != nil {
		t.Fatal(err)
	}
	chat := "history_only"
	table := messageTable(chat)
	createTestDatabase(t, path,
		"CREATE TABLE Name2Id(user_name TEXT)",
		"INSERT INTO Name2Id(rowid,user_name) VALUES(1,'history_only')",
		"CREATE TABLE ["+table+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,status INTEGER,message_content TEXT)",
		"INSERT INTO ["+table+"] VALUES(1,9,1,1000,1700000000,0,'history')",
	)
	var messages []Message
	coverage, err := WalkMessages(root, func(message Message) error {
		messages = append(messages, message)
		return nil
	})
	if err != nil || !coverage.Complete || coverage.TablesDiscovered != 1 || coverage.TablesIndexed != 1 ||
		len(coverage.UnknownTables) != 0 || len(messages) != 1 || messages[0].Chat != chat {
		t.Fatalf("Name2Id 历史会话覆盖异常: coverage=%+v messages=%d err=%v", coverage, len(messages), err)
	}
}

func TestWalkMessagesKeepsUnboundTableIncomplete(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "message", "message_0.db")
	if err := ensureParent(path); err != nil {
		t.Fatal(err)
	}
	table := messageTable("unbound_history")
	createTestDatabase(t, path,
		"CREATE TABLE Name2Id(user_name TEXT)",
		"INSERT INTO Name2Id(rowid,user_name) VALUES(1,'different_user')",
		"CREATE TABLE ["+table+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,status INTEGER,message_content TEXT)",
	)
	coverage, err := WalkMessages(root, func(Message) error { return nil })
	if err != nil || coverage.Complete || coverage.TablesDiscovered != 1 || coverage.TablesIndexed != 0 || len(coverage.UnknownTables) != 1 {
		t.Fatalf("未绑定消息表没有保持 fail-closed: coverage=%+v err=%v", coverage, err)
	}
}
