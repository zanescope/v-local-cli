package app

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zanescope/v-local-cli/internal/state"
	_ "modernc.org/sqlite"
)

func createChatImageExportFixture(t *testing.T) (string, string, []byte) {
	t.Helper()
	snapshot := t.TempDir()
	accountPath := t.TempDir()
	chat := "dong_zzc"
	stem := strings.Repeat("a", 32)

	value := image.NewRGBA(image.Rect(0, 0, 2048, 1536))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, value); err != nil {
		t.Fatal(err)
	}
	payload := encoded.Bytes()

	messagePath := filepath.Join(snapshot, "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(messagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	messageDB, err := sql.Open("sqlite", messagePath)
	if err != nil {
		t.Fatal(err)
	}
	tableDigest := md5.Sum([]byte(chat))
	table := "Msg_" + hex.EncodeToString(tableDigest[:])
	if _, err := messageDB.Exec("CREATE TABLE [" + table + "](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := messageDB.Exec("INSERT INTO [" + table + "] VALUES(12,9002,3,1700000001000,1700000001,'[图片]')"); err != nil {
		t.Fatal(err)
	}
	if err := messageDB.Close(); err != nil {
		t.Fatal(err)
	}

	resourcePath := filepath.Join(snapshot, "resource", "message_resource.db")
	if err := os.MkdirAll(filepath.Dir(resourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	resourceDB, err := sql.Open("sqlite", resourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resourceDB.Exec("CREATE TABLE MessageResourceInfo(message_local_id INTEGER,message_local_type INTEGER,message_svr_id INTEGER,packed_info BLOB)"); err != nil {
		t.Fatal(err)
	}
	inner := append([]byte{0x0a, 0x20}, []byte(stem)...)
	packed := append([]byte{0x12, byte(len(inner))}, inner...)
	if _, err := resourceDB.Exec("INSERT INTO MessageResourceInfo VALUES(?,?,?,?)", 12, 3, 9002, packed); err != nil {
		t.Fatal(err)
	}
	if err := resourceDB.Close(); err != nil {
		t.Fatal(err)
	}

	hardlinkPath := filepath.Join(snapshot, "hardlink", "hardlink.db")
	if err := os.MkdirAll(filepath.Dir(hardlinkPath), 0o700); err != nil {
		t.Fatal(err)
	}
	hardlinkDB, err := sql.Open("sqlite", hardlinkPath)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		"CREATE TABLE dir2id(username TEXT)",
		"INSERT INTO dir2id(rowid,username) VALUES(1,'segment-a'),(2,'segment-b')",
		"CREATE TABLE image_hardlink_info_v4(md5 TEXT,file_name TEXT,dir1 INTEGER,dir2 INTEGER)",
		"INSERT INTO image_hardlink_info_v4 VALUES('', '" + stem + ".dat', 1, 2)",
	}
	for _, statement := range statements {
		if _, err := hardlinkDB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := hardlinkDB.Close(); err != nil {
		t.Fatal(err)
	}

	mediaPath := filepath.Join(accountPath, "msg", "attach", "segment-a", "segment-b", stem+".dat")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return snapshot, accountPath, payload
}

func TestExportChatImageUsesEvidenceBoundFullResolutionCandidate(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	snapshot, accountPath, expected := createChatImageExportFixture(t)
	accountID := state.AccountID("chat-image-acceptance")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: "acceptance-test", AccountPath: accountPath,
		SnapshotPath: snapshot, GenerationID: "generation-chat-image", Storage: "snapshot-only",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}

	evidenceID := "wechat:dong_zzc:9002"
	outputPath := filepath.Join(t.TempDir(), "dong-zzc-full.png")
	code, output, failure := runForTest("export-chat-image", "--account", "acceptance-test", "--output", outputPath, evidenceID)
	if code != 0 {
		t.Fatalf("聊天图片导出失败：code=%d output=%v failure=%v", code, output, failure)
	}
	data := output["data"].(map[string]any)
	digest := sha256.Sum256(expected)
	if data["evidence_id"] != evidenceID || data["width"] != float64(2048) || data["height"] != float64(1536) ||
		data["sha256"] != hex.EncodeToString(digest[:]) || data["verified_by"] != "message_resource_stem+hardlink_map+full_decode" ||
		data["container_validation"] != "full_decode" || data["network_access_performed"] != false {
		t.Fatalf("聊天图片强绑定元数据异常：%v", data)
	}
	exported, err := os.ReadFile(outputPath)
	if err != nil || !bytes.Equal(exported, expected) {
		t.Fatalf("聊天图片导出内容异常：bytes=%d err=%v", len(exported), err)
	}
	serialized, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(serialized, []byte(accountPath)) || bytes.Contains(serialized, []byte(filepath.ToSlash(accountPath))) {
		t.Fatalf("聊天图片导出响应泄露源账号路径：%s", serialized)
	}

	code, _, failure = runForTest("export-chat-image", "--account", "acceptance-test", "--output", outputPath, evidenceID)
	if code == 0 || failure["error"].(map[string]any)["type"] != "output_exists" {
		t.Fatalf("聊天图片导出未保护已有输出：code=%d failure=%v", code, failure)
	}
}

func TestExportChatImageRejectsConflictingStrongCandidates(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	snapshot, accountPath, _ := createChatImageExportFixture(t)
	stem := strings.Repeat("a", 32)
	conflictPath := filepath.Join(accountPath, "msg", "attach", "another-session", stem+".dat")
	if err := os.MkdirAll(filepath.Dir(conflictPath), 0o700); err != nil {
		t.Fatal(err)
	}
	var conflict bytes.Buffer
	if err := png.Encode(&conflict, image.NewRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflictPath, conflict.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	accountID := state.AccountID("chat-image-conflict")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: "conflict-test", AccountPath: accountPath,
		SnapshotPath: snapshot, GenerationID: "generation-conflict", Storage: "snapshot-only",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "must-not-exist.png")
	code, _, failure := runForTest("export-chat-image", "--account", "conflict-test", "--output", outputPath, "wechat:dong_zzc:9002")
	if code == 0 || failure["error"].(map[string]any)["type"] != "chat_image_unavailable" {
		t.Fatalf("冲突的强候选未 fail closed：code=%d failure=%v", code, failure)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("冲突候选仍产生了输出：err=%v", err)
	}
}
