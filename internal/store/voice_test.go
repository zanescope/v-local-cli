package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVoiceEvidenceAndCache(t *testing.T) {
	root := t.TempDir()
	chat := "wxid_voice_test"
	messagePath := filepath.Join(root, "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(messagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", messagePath)
	if err != nil {
		t.Fatal(err)
	}
	table := messageTable(chat)
	if _, err := database.Exec("CREATE TABLE [" + table + "](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,real_sender_id INTEGER,create_time INTEGER,message_content TEXT); INSERT INTO [" + table + "] VALUES(1,9001,34,1700000000000,0,1700000000,'voice')"); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()

	mediaPath := filepath.Join(root, "media", "media_0.db")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o700); err != nil {
		t.Fatal(err)
	}
	mediaDB, err := sql.Open("sqlite", mediaPath)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("\x02#!SILK_V3test")
	if _, err := mediaDB.Exec("CREATE TABLE VoiceInfo(svr_id INTEGER,voice_data BLOB); INSERT INTO VoiceInfo VALUES(?,?)", 9001, payload); err != nil {
		t.Fatal(err)
	}
	_ = mediaDB.Close()

	evidenceID := "wechat:" + chat + ":9001"
	message, err := FindVoiceMessage(root, evidenceID)
	if err != nil || message.Kind != "voice" || message.ServerID != 9001 {
		t.Fatalf("语音证据定位异常：message=%+v err=%v", message, err)
	}
	voice, err := VoiceData(root, message.ServerID)
	if err != nil || string(voice) != string(payload) {
		t.Fatalf("语音负载定位异常：bytes=%d err=%v", len(voice), err)
	}
	cachePath := filepath.Join(t.TempDir(), "voice-transcripts.db")
	record := VoiceTranscript{
		EvidenceID: evidenceID, Chat: chat, ServerID: 9001, Timestamp: 1700000000,
		SortKey: 1700000000000, Transcript: "这是需要搜索的语音转写", AudioSHA256: strings.Repeat("a", 64),
		Engine: "whisper-cli", Model: "ggml-base.bin", Language: "zh", Source: "local_whisper_cpp",
	}
	if err := SaveVoiceTranscript(cachePath, record); err != nil {
		t.Fatal(err)
	}
	cached, found, err := LoadVoiceTranscript(cachePath, evidenceID)
	if err != nil || !found || cached.Transcript != record.Transcript || cached.Source != record.Source {
		t.Fatalf("语音转写暂存异常：found=%v cached=%+v err=%v", found, cached, err)
	}
	items, err := SearchVoiceTranscripts(cachePath, "需要搜索", chat, nil, nil, 10)
	if err != nil || len(items) != 1 || items[0].EvidenceID != evidenceID {
		t.Fatalf("语音转写搜索异常：items=%+v err=%v", items, err)
	}
}

func TestVoiceEvidenceRejectsNonVoice(t *testing.T) {
	if _, _, err := ParseMessageEvidenceID("publication:gh_test:1:1"); err == nil {
		t.Fatal("不应接受非聊天消息证据标识")
	}
	if _, _, err := ParseMessageEvidenceID("wechat:test:not-a-number"); err == nil {
		t.Fatal("不应接受非数字消息标识")
	}
	if _, _, err := DecodeVoiceWAV([]byte("not silk")); err == nil {
		t.Fatal("不应把无效负载解码为 WAV")
	}
}
