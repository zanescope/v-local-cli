package store

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestWeChatExistingVoiceAndOCRIndexes(t *testing.T) {
	root := t.TempDir()
	chat := "wxid_index_fixture"
	messageDirectory := filepath.Join(root, "message")
	if err := os.MkdirAll(messageDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	messageDB, err := sql.Open("sqlite", filepath.Join(messageDirectory, "message_0.db"))
	if err != nil {
		t.Fatal(err)
	}
	digest := md5.Sum([]byte(chat))
	table := "Msg_" + hex.EncodeToString(digest[:])
	if _, err := messageDB.Exec("CREATE TABLE [" + table + "](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT);" +
		"INSERT INTO [" + table + "] VALUES(11,9001,34,1700000002000,1700000002,'voice');" +
		"INSERT INTO [" + table + "] VALUES(12,9002,3,1700000001000,1700000001,'image')"); err != nil {
		t.Fatal(err)
	}
	_ = messageDB.Close()

	indexDB, err := sql.Open("sqlite", filepath.Join(messageDirectory, "message_fts.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexDB.Exec(`
		CREATE TABLE name2id(username TEXT);
		INSERT INTO name2id(rowid,username) VALUES(7,?);
		CREATE TABLE message_fts_v4_0(acontent TEXT,create_time INTEGER,local_type INTEGER,message_local_id INTEGER,sender_id INTEGER,session_id INTEGER,sort_seq INTEGER);
		CREATE TABLE ImgFts0V0(acontent TEXT,create_time INTEGER,local_type INTEGER,message_local_id INTEGER,sender_id INTEGER,session_id INTEGER,sort_seq INTEGER);
		INSERT INTO message_fts_v4_0 VALUES('微信已有语音转写',1700000002,34,11,0,7,1700000002000);
		INSERT INTO ImgFts0V0 VALUES('微信已有图片文字',1700000001,3,12,0,7,1700000001000);
	`, chat); err != nil {
		t.Fatal(err)
	}
	_ = indexDB.Close()

	voiceMessages, err := VoiceMessages(root, chat, nil, nil, 0)
	if err != nil || len(voiceMessages) != 1 {
		t.Fatalf("语音候选异常：%v %v", voiceMessages, err)
	}
	voiceTexts, err := WeChatVoiceTexts(root, voiceMessages)
	if err != nil || voiceTexts[voiceMessages[0].EvidenceID].Text != "微信已有语音转写" {
		t.Fatalf("微信已有语音转写读取异常：%v %v", voiceTexts, err)
	}
	imageMessages, err := ImageMessages(root, chat, nil, nil, 0)
	if err != nil || len(imageMessages) != 1 {
		t.Fatalf("图片候选异常：%v %v", imageMessages, err)
	}
	imageTexts, err := WeChatOCRTexts(root, imageMessages)
	if err != nil || imageTexts[imageMessages[0].EvidenceID].Text != "微信已有图片文字" {
		t.Fatalf("微信已有 OCR 读取异常：%v %v", imageTexts, err)
	}
	status, err := WeChatTextIndexStatus(root)
	if err != nil || status.VoiceIndexedRows != 1 || status.ImageIndexedRows != 1 || status.EngineInvoked || status.PrivateIPCInvoked {
		t.Fatalf("微信索引状态异常：%+v %v", status, err)
	}
	image, err := FindImageMessage(root, imageMessages[0].EvidenceID)
	if err != nil || image.LocalID != 12 {
		t.Fatalf("图片证据重新定位异常：%+v %v", image, err)
	}
}

func TestWeChatIndexRequiresExactSessionAndLocalID(t *testing.T) {
	root := t.TempDir()
	messageDirectory := filepath.Join(root, "message")
	if err := os.MkdirAll(messageDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	indexDB, err := sql.Open("sqlite", filepath.Join(messageDirectory, "message_fts.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexDB.Exec(`
		CREATE TABLE name2id(username TEXT);
		INSERT INTO name2id(rowid,username) VALUES(7,'wxid_other');
		CREATE TABLE message_fts_v4_0(acontent TEXT,local_type INTEGER,message_local_id INTEGER,session_id INTEGER);
		INSERT INTO message_fts_v4_0 VALUES('不能串用的转写',34,11,7);
	`); err != nil {
		t.Fatal(err)
	}
	_ = indexDB.Close()
	messages := []Message{{Chat: "wxid_target", LocalID: 11, LocalType: 34, Kind: "voice", EvidenceID: "wechat:wxid_target:9001"}}
	texts, err := WeChatVoiceTexts(root, messages)
	if err != nil || len(texts) != 0 {
		t.Fatalf("不同会话的索引不得串用：%v %v", texts, err)
	}
}
