package store

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoryEnrichesRedPacketCard(t *testing.T) {
	root := t.TempDir()
	messagePath := filepath.Join(root, "message", "message_0.db")
	if err := ensureParent(messagePath); err != nil {
		t.Fatal(err)
	}
	chat := "alice"
	table := messageTable(chat)
	messageTime := time.Date(2026, time.August, 13, 9, 30, 15, 0, time.Local).Unix()
	card := `<msg><appmsg><title>恭喜发财</title><type>2001</type><wcpayinfo><nativeurl>wxpay://hongbao?id=1</nativeurl><paymsgid>send-1</paymsgid><amount>880</amount></wcpayinfo></appmsg></msg>`
	createTestDatabase(t, messagePath,
		"CREATE TABLE ["+table+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT)",
		fmt.Sprintf("INSERT INTO [%s] VALUES(1,9001,8594229559345,1000,%d,'%s')", table, messageTime, card),
	)
	generalPath := filepath.Join(root, "general", "general.db")
	if err := ensureParent(generalPath); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, generalPath,
		"CREATE TABLE redEnvelopeTable(message_server_id INTEGER,session_name TEXT,sender_user_name TEXT,native_url TEXT,send_id TEXT,scene_id INTEGER,hb_status INTEGER,hb_type INTEGER,receive_status INTEGER)",
		"INSERT INTO redEnvelopeTable VALUES(9001,'alice','bob','wxpay://hongbao?id=1','send-1',1,4,1,2)",
	)

	messages, err := History(root, chat, 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("红包历史异常：items=%+v err=%v", messages, err)
	}
	redPacket, ok := messages[0].Details["red_packet"].(map[string]any)
	if !ok {
		t.Fatalf("红包结构缺失：%+v", messages[0])
	}
	if detailString(redPacket, "receive_status") != "received" || detailInteger(redPacket, "receive_status_code") != 2 || detailString(redPacket, "receive_status_label") != "已领取" {
		t.Fatalf("红包领取状态异常：%+v", redPacket)
	}
	if detailString(redPacket, "packet_status") != "fully_claimed" || detailInteger(redPacket, "packet_status_code") != 4 {
		t.Fatalf("红包整体状态异常：%+v", redPacket)
	}
	if detailString(redPacket, "message_date") != time.Unix(messageTime, 0).Local().Format("2006-01-02") || detailString(redPacket, "message_time") == "" || detailString(redPacket, "receive_time_status") != "not_retained" {
		t.Fatalf("红包日期字段异常：%+v", redPacket)
	}
	if detailString(redPacket, "amount") != "¥8.80" || detailString(redPacket, "amount_status") != "retained" || detailString(redPacket, "amount_source") != "message_xml.amount" || detailString(redPacket, "amount_kind") != "card_amount" {
		t.Fatalf("红包金额字段异常：%+v", redPacket)
	}
	for _, expected := range []string{"已领取", "¥8.80", "消息时间：2026-08-13 09:30:15", "恭喜发财"} {
		if !strings.Contains(messages[0].Content, expected) {
			t.Fatalf("红包摘要缺少 %q：%s", expected, messages[0].Content)
		}
	}
}

func TestHistoryUsesRetainedRedPacketReceiveTimeAndAmount(t *testing.T) {
	root := t.TempDir()
	messagePath := filepath.Join(root, "message", "message_0.db")
	if err := ensureParent(messagePath); err != nil {
		t.Fatal(err)
	}
	table := messageTable("alice")
	card := `<msg><appmsg><title>节日快乐</title><type>2001</type><wcpayinfo><nativeurl>wxpay://hongbao?id=2</nativeurl></wcpayinfo></appmsg></msg>`
	createTestDatabase(t, messagePath,
		"CREATE TABLE ["+table+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT)",
		"INSERT INTO ["+table+"] VALUES(1,9002,8594229559345,1000,100,'"+card+"')",
	)
	receiveTime := time.Date(2026, time.August, 13, 10, 5, 0, 0, time.Local).Unix()
	generalPath := filepath.Join(root, "general", "general.db")
	if err := ensureParent(generalPath); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, generalPath,
		"CREATE TABLE redEnvelopeTable(message_server_id INTEGER,session_name TEXT,native_url TEXT,hb_status INTEGER,receive_status INTEGER,receive_time INTEGER,receive_amount INTEGER)",
		fmt.Sprintf("INSERT INTO redEnvelopeTable VALUES(9002,'alice','wxpay://hongbao?id=2',5,0,%d,666)", receiveTime*1000),
	)

	messages, err := History(root, "alice", 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("红包历史异常：items=%+v err=%v", messages, err)
	}
	redPacket := messages[0].Details["red_packet"].(map[string]any)
	if detailString(redPacket, "receive_status") != "not_received" || detailString(redPacket, "packet_status") != "expired" || detailInteger(redPacket, "receive_timestamp") != receiveTime {
		t.Fatalf("红包领取证据异常：%+v", redPacket)
	}
	if detailString(redPacket, "amount") != "¥6.66" || detailString(redPacket, "amount_source") != "redEnvelopeTable.receive_amount" || detailString(redPacket, "receive_time_status") != "retained" {
		t.Fatalf("红包领取时间或金额异常：%+v", redPacket)
	}
	if !strings.Contains(messages[0].Content, "未领取·已过期") {
		t.Fatalf("红包摘要状态异常：%s", messages[0].Content)
	}
}

func createTestDatabase(t *testing.T, path string, statements ...string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("执行测试 SQL 失败：%v", err)
		}
	}
}

func TestContactsAndHistory(t *testing.T) {
	root := t.TempDir()
	contactPath := filepath.Join(root, "contact", "contact.db")
	if err := ensureParent(contactPath); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, contactPath,
		"CREATE TABLE contact(username TEXT, alias TEXT, remark TEXT, nick_name TEXT)",
		"INSERT INTO contact VALUES('alice','alice-id','阿丽','Alice')",
	)
	messagePath := filepath.Join(root, "message", "message_0.db")
	if err := ensureParent(messagePath); err != nil {
		t.Fatal(err)
	}
	table := messageTable("alice")
	createTestDatabase(t, messagePath,
		"CREATE TABLE Name2Id(user_name TEXT)",
		"INSERT INTO Name2Id(rowid,user_name) VALUES(1,'alice')",
		"CREATE TABLE ["+table+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,real_sender_id INTEGER,create_time INTEGER,message_content TEXT)",
		"INSERT INTO ["+table+"] VALUES(7,9,1,2000,1,2,'你好 Go')",
	)
	contacts, err := Contacts(root, "阿丽", 10)
	if err != nil || len(contacts) != 1 || contacts[0].Username != "alice" {
		t.Fatalf("联系人结果异常：items=%+v err=%v", contacts, err)
	}
	messages, err := History(root, "alice", 10)
	if err != nil || len(messages) != 1 || messages[0].Content != "你好 Go" || messages[0].Sender != "阿丽" || messages[0].SenderUsername != "alice" || messages[0].SenderNickname != "Alice" {
		t.Fatalf("消息结果异常：items=%+v err=%v", messages, err)
	}
	hits, err := Search(root, "go", "alice", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("搜索结果异常：items=%+v err=%v", hits, err)
	}
}

func TestHistoryAndSearchTimeWindow(t *testing.T) {
	root := t.TempDir()
	messagePath := filepath.Join(root, "message", "message_0.db")
	if err := ensureParent(messagePath); err != nil {
		t.Fatal(err)
	}
	table := messageTable("alice")
	createTestDatabase(t, messagePath,
		"CREATE TABLE ["+table+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT)",
		"INSERT INTO ["+table+"] VALUES(1,11,1,1000,100,'范围外消息')",
		"INSERT INTO ["+table+"] VALUES(2,12,1,2000,200,'范围内 Go 消息')",
	)
	start, end := int64(150), int64(250)
	messages, err := HistoryWindow(root, "alice", &start, &end, 10)
	if err != nil || len(messages) != 1 || messages[0].ServerID != 12 {
		t.Fatalf("时间范围历史异常：items=%+v err=%v", messages, err)
	}
	hits, err := SearchWindow(root, "go", "alice", &start, &end, 10)
	if err != nil || len(hits) != 1 || hits[0].ServerID != 12 {
		t.Fatalf("时间范围搜索异常：items=%+v err=%v", hits, err)
	}
}

func TestSearchWindowFiltersBeforeApplyingResultLimit(t *testing.T) {
	root := t.TempDir()
	messagePath := filepath.Join(root, "message", "message_0.db")
	if err := ensureParent(messagePath); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", messagePath)
	if err != nil {
		t.Fatal(err)
	}
	table := messageTable("alice")
	if _, err := database.Exec("CREATE TABLE [" + table + "](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT)"); err != nil {
		t.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	statement, err := transaction.Prepare("INSERT INTO [" + table + "] VALUES(?,?,?,?,?,?)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := statement.Exec(1, 1, 1, 1, 1, "唯一 needle 命中"); err != nil {
		t.Fatal(err)
	}
	for index := 2; index <= 5002; index++ {
		if _, err := statement.Exec(index, index, 1, index, index, "较新的非命中消息"); err != nil {
			t.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	hits, err := SearchWindow(root, "needle", "alice", nil, nil, 10)
	if err != nil || len(hits) != 1 || hits[0].ServerID != 1 {
		t.Fatalf("搜索在过滤前截断了候选：items=%+v err=%v", hits, err)
	}
}

func TestCrossChatSearchKeepsOnlyNewestGlobalMatchesWhileMerging(t *testing.T) {
	root := t.TempDir()
	contactPath := filepath.Join(root, "contact", "contact.db")
	if err := ensureParent(contactPath); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, contactPath,
		"CREATE TABLE contact(username TEXT, alias TEXT, remark TEXT, nick_name TEXT)",
		"INSERT INTO contact VALUES('alice','','','Alice'),('bob','','','Bob')",
	)
	messagePath := filepath.Join(root, "message", "message_0.db")
	if err := ensureParent(messagePath); err != nil {
		t.Fatal(err)
	}
	aliceTable, bobTable := messageTable("alice"), messageTable("bob")
	createTestDatabase(t, messagePath,
		"CREATE TABLE ["+aliceTable+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT)",
		"INSERT INTO ["+aliceTable+"] VALUES(1,101,1,100,100,'needle alice old'),(2,102,1,400,400,'needle alice new')",
		"CREATE TABLE ["+bobTable+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT)",
		"INSERT INTO ["+bobTable+"] VALUES(1,201,1,200,200,'needle bob old'),(2,202,1,500,500,'needle bob new')",
	)

	hits, err := SearchWindow(root, "needle", "", nil, nil, 2)
	if err != nil || len(hits) != 2 || hits[0].ServerID != 202 || hits[1].ServerID != 102 {
		t.Fatalf("跨会话有限搜索没有保留全局最新命中：items=%+v err=%v", hits, err)
	}
}

func TestSearchMatchesStructuredCardDetails(t *testing.T) {
	root := t.TempDir()
	messagePath := filepath.Join(root, "message", "message_0.db")
	if err := ensureParent(messagePath); err != nil {
		t.Fatal(err)
	}
	table := messageTable("alice")
	card := `<msg><appmsg><title>展示标题</title><des>摘要</des><type>5</type><sourceusername>gh_hidden_target</sourceusername></appmsg></msg>`
	createTestDatabase(t, messagePath,
		"CREATE TABLE ["+table+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT)",
		"INSERT INTO ["+table+"] VALUES(1,11,49,1000,100,'"+card+"')",
	)
	hits, err := Search(root, "gh_hidden_target", "alice", 10)
	if err != nil || len(hits) != 1 || hits[0].Kind != "link" {
		t.Fatalf("结构化卡片搜索异常：items=%+v err=%v", hits, err)
	}
	var output bytes.Buffer
	temporary := t.TempDir()
	count, err := StreamExportWindow(&output, temporary, root, "search", "gh_hidden_target", "alice", nil, nil, "jsonl")
	if err != nil || count != 1 {
		t.Fatalf("全量导出的结构化卡片搜索异常：count=%d err=%v", count, err)
	}
}

func TestPrivateAndGroupStats(t *testing.T) {
	root := t.TempDir()
	messagePath := filepath.Join(root, "message", "message_0.db")
	if err := ensureParent(messagePath); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", messagePath)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		"CREATE TABLE Name2Id(user_name TEXT)",
		"INSERT INTO Name2Id(rowid,user_name) VALUES(1,'alice'),(2,'bob'),(3,'me')",
	}
	groupTable := messageTable("room@chatroom")
	privateTable := messageTable("alice")
	for _, table := range []string{groupTable, privateTable} {
		statements = append(statements, "CREATE TABLE ["+table+"](local_type INTEGER,real_sender_id INTEGER,create_time INTEGER,status INTEGER)")
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	first := time.Date(2026, time.August, 1, 9, 0, 0, 0, time.Local).Unix()
	second := time.Date(2026, time.August, 2, 15, 0, 0, 0, time.Local).Unix()
	groupRows := []struct {
		kind, sender, timestamp int64
	}{{1, 2, first}, {3, 2, second}, {34, 1, second}, {10000, 0, second}}
	for _, row := range groupRows {
		status := int64(4)
		if row.sender == 2 {
			status = 2
		}
		if _, err := database.Exec("INSERT INTO ["+groupTable+"] VALUES(?,?,?,?)", row.kind, row.sender, row.timestamp, status); err != nil {
			t.Fatal(err)
		}
	}
	privateRows := []struct {
		kind, sender, timestamp int64
	}{{1, 1, first}, {3, 3, second}, {34, 0, second}}
	for _, row := range privateRows {
		if _, err := database.Exec("INSERT INTO ["+privateTable+"] VALUES(?,?,?,?)", row.kind, row.sender, row.timestamp, 4); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	group, err := Stats(root, "room@chatroom", nil, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if group.TotalMessages != 3 || group.SystemMessages != 1 || group.Participants != 2 || len(group.Members) != 2 {
		t.Fatalf("群聊统计异常：%+v", group)
	}
	if group.Members[0].Sender != "bob" || group.Members[0].Messages != 2 || group.Members[0].ActiveDays != 2 {
		t.Fatalf("群成员排行异常：%+v", group.Members)
	}
	if group.Members[0].SenderIdentity != "self" || !group.Members[0].IsFromMe || group.Members[1].SenderIdentity != "contact" {
		t.Fatalf("群成员我的身份解析异常：%+v", group.Members)
	}
	if group.ByCategory["text"] != 1 || group.ByMediaKind["image"] != 1 || group.ByMediaKind["voice"] != 1 || group.PeakHour == nil || *group.PeakHour != 15 {
		t.Fatalf("群聊分布异常：%+v", group)
	}

	private, err := Stats(root, "alice", nil, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if private.TotalMessages != 3 || private.ActiveDays != 2 || private.Direction == nil ||
		private.Direction.Sent != 1 || private.Direction.Received != 2 || private.Direction.Unknown != 0 {
		t.Fatalf("私聊统计异常：%+v", private)
	}
	if messageKind(244813135921) != "quote" {
		t.Fatalf("大整数消息类型未正确拆分：%s", messageKind(244813135921))
	}
}

func ensureParent(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o700)
}
