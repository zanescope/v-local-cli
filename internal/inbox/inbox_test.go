package inbox

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zanescope/v-local-cli/internal/messageindex"
	"github.com/zanescope/v-local-cli/internal/state"
	_ "modernc.org/sqlite"
)

func createIndexedGeneration(t *testing.T, home, generation string, messages []string) state.AccountState {
	t.Helper()
	accountID := state.AccountID("inbox-account")
	snapshot := filepath.Join(home, "accounts", accountID, "snapshots", generation)
	if err := os.MkdirAll(filepath.Join(snapshot, "contact"), 0o700); err != nil {
		t.Fatal(err)
	}
	contact, err := sql.Open("sqlite", filepath.Join(snapshot, "contact", "contact.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contact.Exec("CREATE TABLE contact(username TEXT,alias TEXT,remark TEXT,nick_name TEXT); INSERT INTO contact VALUES('alice','','阿丽','Alice')"); err != nil {
		t.Fatal(err)
	}
	_ = contact.Close()
	if err := os.MkdirAll(filepath.Join(snapshot, "message"), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(snapshot, "message", "message_0.db"))
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum([]byte("alice"))
	table := "Msg_" + hex.EncodeToString(sum[:])
	if _, err := database.Exec("CREATE TABLE [" + table + "](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,status INTEGER,message_content TEXT)"); err != nil {
		t.Fatal(err)
	}
	for index, content := range messages {
		if _, err := database.Exec(
			"INSERT INTO ["+table+"] VALUES(?,?,?,?,?,?,?)",
			index+1, index+100, 1, index+1, 1700000000+index, 0, content,
		); err != nil {
			t.Fatal(err)
		}
	}
	_ = database.Close()
	t.Setenv("V_LOCAL_CLI_HOME", home)
	account := state.AccountState{
		AccountID: accountID, AccountName: "test", SnapshotPath: snapshot,
		GenerationID: generation, SnapshotManifestSHA256: "manifest-" + strconv.Itoa(len(messages)) + "-" + generation,
	}
	if _, err := messageindex.Build(account, false); err != nil {
		t.Fatal(err)
	}
	return account
}

func TestPollOrCreateReloadsCommittedGenerationUnderLock(t *testing.T) {
	home := t.TempDir()
	base := createIndexedGeneration(t, home, "generation-committed-base", []string{"旧"})
	if err := state.Save(&base); err != nil {
		t.Fatal(err)
	}
	first, observed, stage, err := PollOrCreate(base.AccountID, "agent-committed", "now", 10)
	if err != nil || stage != "poll" || observed.GenerationID != base.GenerationID || len(first.Items) != 0 {
		t.Fatalf("首次原子创建/poll 异常：result=%+v state=%+v stage=%s err=%v", first, observed, stage, err)
	}
	current := createIndexedGeneration(t, home, "generation-committed-current", []string{"旧", "新"})
	if err := state.Save(&current); err != nil {
		t.Fatal(err)
	}
	second, observed, stage, err := PollOrCreate(base.AccountID, "agent-committed", "now", 10)
	if err != nil || stage != "poll" || observed.GenerationID != current.GenerationID || len(second.Items) != 1 || second.Items[0].Message.Content != "新" {
		t.Fatalf("未在锁内采用最新 state：result=%+v state=%+v stage=%s err=%v", second, observed, stage, err)
	}
}

func TestPollReplaysUntilAckAndAdvancesAtomically(t *testing.T) {
	home := t.TempDir()
	base := createIndexedGeneration(t, home, "generation-one", []string{"第一条"})
	if _, err := Create(base, "agent-a", "now"); err != nil {
		t.Fatal(err)
	}
	current := createIndexedGeneration(t, home, "generation-two", []string{"第一条", "第二条"})
	first, err := Poll(current, "agent-a", 10)
	if err != nil || len(first.Items) != 1 || first.Items[0].Message.Content != "第二条" || !first.AckRequired {
		t.Fatalf("首次 poll 异常：result=%+v err=%v", first, err)
	}
	replayed, err := Poll(current, "agent-a", 1)
	if err != nil || replayed.BatchID != first.BatchID || !replayed.Replayed || len(replayed.Items) != 1 {
		t.Fatalf("未 ack 重放异常：result=%+v err=%v", replayed, err)
	}
	cursor, err := Ack(current.AccountID, "agent-a", first.BatchID)
	if err != nil || cursor.BaseGeneration != current.GenerationID || cursor.Pending != nil {
		t.Fatalf("ack 异常：cursor=%+v err=%v", cursor, err)
	}
	empty, err := Poll(current, "agent-a", 10)
	if err != nil || len(empty.Items) != 0 || empty.AckRequired {
		t.Fatalf("ack 后应无重复：result=%+v err=%v", empty, err)
	}
}

func TestCursorRejectsGenerationManifestMismatch(t *testing.T) {
	home := t.TempDir()
	account := createIndexedGeneration(t, home, "generation-bound", []string{"消息"})
	if _, err := Create(account, "agent-bound", "now"); err != nil {
		t.Fatal(err)
	}
	tampered := account
	tampered.SnapshotManifestSHA256 = "different-snapshot-manifest"
	if _, err := Poll(tampered, "agent-bound", 10); err == nil || !strings.Contains(err.Error(), "绑定不匹配") {
		t.Fatalf("游标未拒绝 generation 摘要变化：%v", err)
	}
}

func TestPollLimitNeverAdvancesUnreturnedMessages(t *testing.T) {
	home := t.TempDir()
	base := createIndexedGeneration(t, home, "generation-base", nil)
	if _, err := Create(base, "agent-b", "now"); err != nil {
		t.Fatal(err)
	}
	current := createIndexedGeneration(t, home, "generation-current", []string{"一", "二", "三"})
	first, err := Poll(current, "agent-b", 2)
	if err != nil || len(first.Items) != 2 || !first.HasMore {
		t.Fatalf("截断批次异常：result=%+v err=%v", first, err)
	}
	if _, err := Ack(current.AccountID, "agent-b", first.BatchID); err != nil {
		t.Fatal(err)
	}
	second, err := Poll(current, "agent-b", 2)
	if err != nil || len(second.Items) != 1 || second.Items[0].Message.Content != "三" {
		t.Fatalf("截断尾部丢失：result=%+v err=%v", second, err)
	}
}

func TestCursorRecoversInterruptedAtomicPublish(t *testing.T) {
	home := t.TempDir()
	base := createIndexedGeneration(t, home, "generation-recovery-base", []string{"旧"})
	if _, err := Create(base, "agent-recovery", "now"); err != nil {
		t.Fatal(err)
	}
	path, err := cursorPath(base.AccountID, "agent-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if cursor, err := Get(base.AccountID, "agent-recovery"); err != nil || cursor.BaseGeneration != base.GenerationID {
		t.Fatalf("中断恢复失败：cursor=%+v err=%v", cursor, err)
	}
	current := createIndexedGeneration(t, home, "generation-recovery-current", []string{"旧", "新"})
	if result, err := Poll(current, "agent-recovery", 10); err != nil || len(result.Items) != 1 {
		t.Fatalf("恢复后 poll 失败：result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("恢复后的主游标未重新发布：%v", err)
	}
}
