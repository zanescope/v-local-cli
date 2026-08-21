package messageindex

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zanescope/v-local-cli/internal/state"
	"github.com/zanescope/v-local-cli/internal/store"
	_ "modernc.org/sqlite"
)

func TestSearchTextExcludesStructuredSecretFields(t *testing.T) {
	text := searchText(store.Message{
		Content: "公开正文",
		Details: map[string]any{
			"label": "可搜索标签", "api_token": "token-value", "aesKey": "key-value",
			"nested": map[string]any{"password": "password-value", "summary": "嵌套摘要"},
		},
	})
	for _, forbidden := range []string{"token-value", "key-value", "password-value"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("结构化敏感字段进入全文索引：%s", forbidden)
		}
	}
	for _, expected := range []string{"公开正文", "可搜索标签", "嵌套摘要"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("普通结构化文本未进入全文索引：%s", expected)
		}
	}
}

func createDB(t *testing.T, path string, statements ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func indexedAccount(t *testing.T, home, generation, content string, serverID int) state.AccountState {
	t.Helper()
	snapshot := filepath.Join(t.TempDir(), "snapshot")
	createDB(t, filepath.Join(snapshot, "contact", "contact.db"),
		"CREATE TABLE contact(username TEXT, alias TEXT, remark TEXT, nick_name TEXT)",
		"INSERT INTO contact VALUES('alice','','阿丽','Alice')",
	)
	sum := md5.Sum([]byte("alice"))
	table := "Msg_" + hex.EncodeToString(sum[:])
	createDB(t, filepath.Join(snapshot, "message", "message_0.db"),
		"CREATE TABLE ["+table+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,status INTEGER,message_content TEXT)",
		"INSERT INTO ["+table+"] VALUES(1,"+fmtInt(serverID)+",1,1000,1700000000,0,'"+content+"')",
	)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	return state.AccountState{
		AccountID: state.AccountID("index-account"), AccountName: "test", GenerationID: generation,
		SnapshotPath: snapshot, SnapshotManifestSHA256: "manifest-" + generation,
	}
}

func fmtInt(value int) string {
	if value == 0 {
		return "0"
	}
	result := ""
	for value > 0 {
		result = string(rune('0'+value%10)) + result
		value /= 10
	}
	return result
}

func TestBuildSearchAndGenerationDiff(t *testing.T) {
	home := t.TempDir()
	base := indexedAccount(t, home, "generation-one", "旧消息", 11)
	report, err := Build(base, false)
	if err != nil || report.Manifest.DocumentCount != 1 || !report.Manifest.Coverage.Complete {
		t.Fatalf("build base 异常：report=%+v err=%v", report, err)
	}
	search, err := Search(base, "旧消息", "alice", nil, nil, 20)
	if err != nil || len(search.Items) != 1 || search.Coverage["backend"] != "generation_index" {
		t.Fatalf("index search 异常：report=%+v err=%v", search, err)
	}
	current := indexedAccount(t, home, "generation-two", "更新消息", 11)
	if _, err := Build(current, false); err != nil {
		t.Fatal(err)
	}
	currentPath, _ := DatabasePath(current.AccountID, current.GenerationID)
	basePath, _ := DatabasePath(base.AccountID, base.GenerationID)
	diff, err := Diff(currentPath, basePath, Position{}, 20)
	if err != nil || len(diff.Items) != 1 || diff.Items[0].Kind != "updated" || diff.Items[0].Message.Content != "更新消息" {
		t.Fatalf("generation diff 异常：report=%+v err=%v", diff, err)
	}
}

func TestIndexManifestBindsDatabaseAndCountsUniqueEvidence(t *testing.T) {
	home := t.TempDir()
	account := indexedAccount(t, home, "generation-binding", "同一消息", 22)
	sum := md5.Sum([]byte("alice"))
	table := "Msg_" + hex.EncodeToString(sum[:])
	createDB(t, filepath.Join(account.SnapshotPath, "message", "message_1.db"),
		"CREATE TABLE ["+table+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,status INTEGER,message_content TEXT)",
		"INSERT INTO ["+table+"] VALUES(9,22,1,1000,1700000000,0,'同一消息')",
	)
	report, err := Build(account, false)
	if err != nil || report.Manifest.DocumentCount != 1 {
		t.Fatalf("唯一证据计数异常：report=%+v err=%v", report, err)
	}
	reused, err := Build(account, true)
	if err != nil || reused.Status != "ready" || reused.Manifest.CreatedAt != report.Manifest.CreatedAt {
		t.Fatalf("有效 generation 索引不应被 force 重写：report=%+v err=%v", reused, err)
	}
	tampered := account
	tampered.SnapshotManifestSHA256 = "different-manifest"
	status, err := Inspect(tampered)
	if err != nil || status.Valid || status.Reason != "generation_binding_mismatch" {
		t.Fatalf("外部 manifest 绑定未拒绝：status=%+v err=%v", status, err)
	}
	path, _ := DatabasePath(account.AccountID, account.GenerationID)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE metadata SET value='wrong' WHERE key='account_id'"); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	status, err = Inspect(account)
	if err != nil || status.Valid || status.Reason != "database_binding_mismatch" {
		t.Fatalf("索引内部绑定未拒绝：status=%+v err=%v", status, err)
	}
}

func TestGarbageCollectPreservesPinnedGeneration(t *testing.T) {
	home := t.TempDir()
	first := indexedAccount(t, home, "20260101T000000Z-first", "一", 31)
	second := indexedAccount(t, home, "20260102T000000Z-second", "二", 32)
	current := indexedAccount(t, home, "20260103T000000Z-current", "三", 33)
	for _, account := range []state.AccountState{first, second, current} {
		if _, err := Build(account, false); err != nil {
			t.Fatal(err)
		}
	}
	report, err := GarbageCollect(current.AccountID, current.GenerationID, 0, map[string][]string{
		first.GenerationID: {"inbox:test"},
	}, false)
	if err != nil || report.RemovedGenerations != 1 {
		t.Fatalf("派生索引 GC 异常：report=%+v err=%v", report, err)
	}
	for _, account := range []state.AccountState{first, current} {
		path, _ := DatabasePath(account.AccountID, account.GenerationID)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("应保留 generation %s：%v", account.GenerationID, err)
		}
	}
	path, _ := DatabasePath(second.AccountID, second.GenerationID)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("未引用 generation 应删除：%v", err)
	}
}
