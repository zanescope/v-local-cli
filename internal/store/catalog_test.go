package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
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
	statistics, err := StatsAll(root, nil, nil, 0)
	unknown, _ := statistics.Coverage["unknown_tables"].([]string)
	if err != nil || statistics.Coverage["complete"] != false || len(unknown) != 1 || statistics.SourceTables != 0 {
		t.Fatalf("跨会话统计静默忽略未绑定表: stats=%+v err=%v", statistics, err)
	}
}

func TestMessagesWindowReturnsAllRecognizedChatsInGlobalOrder(t *testing.T) {
	root := t.TempDir()
	contactPath := filepath.Join(root, "contact", "contact.db")
	if err := ensureParent(contactPath); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, contactPath,
		"CREATE TABLE contact(username TEXT,nick_name TEXT)",
		"INSERT INTO contact VALUES('alice','Alice'),('room@chatroom','AI 讨论群')",
	)
	messagePath := filepath.Join(root, "message", "message_0.db")
	if err := ensureParent(messagePath); err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, time.August, 29, 9, 0, 0, 0, time.Local).Unix()
	second := time.Date(2026, time.August, 29, 15, 0, 0, 0, time.Local).Unix()
	outside := time.Date(2026, time.August, 28, 23, 59, 59, 0, time.Local).Unix()
	aliceTable, roomTable := messageTable("alice"), messageTable("room@chatroom")
	createTestDatabase(t, messagePath,
		"CREATE TABLE ["+aliceTable+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT)",
		"CREATE TABLE ["+roomTable+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT)",
		fmt.Sprintf("INSERT INTO [%s] VALUES(1,11,1,900,%d,'AI 模型评测')", aliceTable, first),
		fmt.Sprintf("INSERT INTO [%s] VALUES(2,12,1,90,%d,'范围外')", aliceTable, outside),
		fmt.Sprintf("INSERT INTO [%s] VALUES(3,13,1,100,%d,'Agent 工作流')", roomTable, second),
	)
	start := time.Date(2026, time.August, 29, 0, 0, 0, 0, time.Local).Unix()
	end := time.Date(2026, time.August, 29, 23, 59, 59, 0, time.Local).Unix()
	items, coverage, err := MessagesWindow(root, &start, &end, 1)
	if err != nil || !coverage.Complete || coverage.RowsEmitted != 2 || len(items) != 1 {
		t.Fatalf("跨会话消息范围异常: coverage=%+v items=%+v err=%v", coverage, items, err)
	}
	if items[0].Chat != "room@chatroom" || items[0].ChatDisplay != "AI 讨论群" || items[0].Content != "Agent 工作流" {
		t.Fatalf("跨会话全局排序或显示身份异常: %+v", items[0])
	}
}

func TestStatsAllMarksRowConversionFailureIncomplete(t *testing.T) {
	root := t.TempDir()
	contactPath := filepath.Join(root, "contact", "contact.db")
	if err := ensureParent(contactPath); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, contactPath,
		"CREATE TABLE contact(username TEXT,nick_name TEXT)",
		"INSERT INTO contact VALUES('alice','Alice')",
	)
	messagePath := filepath.Join(root, "message", "message_0.db")
	if err := ensureParent(messagePath); err != nil {
		t.Fatal(err)
	}
	table := messageTable("alice")
	createTestDatabase(t, messagePath,
		"CREATE TABLE ["+table+"](local_type TEXT,create_time INTEGER)",
		"INSERT INTO ["+table+"] VALUES('not-an-integer',1700000000)",
	)

	statistics, err := StatsAll(root, nil, nil, 0)
	failed, _ := statistics.Coverage["failed_tables"].([]string)
	if err != nil || statistics.Coverage["complete"] != false || len(failed) != 1 ||
		statistics.SourceTables != 0 || statistics.SourceRows != 0 || statistics.TotalMessages != 0 {
		t.Fatalf("跨会话统计行转换失败未降低覆盖率: stats=%+v err=%v", statistics, err)
	}
}
