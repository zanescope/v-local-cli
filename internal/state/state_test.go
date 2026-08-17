package state

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestStateCommitCrashHelper(t *testing.T) {
	if os.Getenv("V_LOCAL_CLI_TEST_STATE_COMMIT_CRASH") != "1" {
		return
	}
	path := os.Getenv("V_LOCAL_CLI_TEST_STATE_PATH")
	temporary := os.Getenv("V_LOCAL_CLI_TEST_STATE_TEMPORARY")
	if path == "" || temporary == "" {
		t.Fatal("missing crash helper paths")
	}
	if err := commitStateFile(path, temporary, func() { os.Exit(99) }); err != nil {
		t.Fatal(err)
	}
	t.Fatal("state commit unexpectedly survived injected crash")
}

func TestLoadRecoversValidBackupAfterInterruptedSave(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	accountID := AccountID("backup-account")
	snapshot := filepath.Join(home, "accounts", accountID, "snapshots", "generation")
	if err := os.MkdirAll(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	value := AccountState{AccountID: accountID, AccountName: "test", SnapshotPath: snapshot, GenerationID: "generation", Storage: "snapshot-only"}
	if err := Save(&value); err != nil {
		t.Fatal(err)
	}
	path, _ := StatePath(accountID)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".old", payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(accountID)
	if err != nil || loaded.GenerationID != "generation" {
		t.Fatalf("备份恢复失败：state=%+v err=%v", loaded, err)
	}
	if _, err := os.Stat(path + ".old"); err != nil {
		t.Fatalf("只读加载不应删除备份：%v", err)
	}
}

func TestSaveDoesNotOverwriteLegacyPredictableTemporaryPath(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	accountID := AccountID("temporary-path-account")
	snapshot := filepath.Join(home, "accounts", accountID, "snapshots", "generation")
	if err := os.MkdirAll(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	value := AccountState{AccountID: accountID, AccountName: "first", SnapshotPath: snapshot, GenerationID: "generation", Storage: "snapshot-only"}
	if err := Save(&value); err != nil {
		t.Fatal(err)
	}
	path, err := StatePath(accountID)
	if err != nil {
		t.Fatal(err)
	}
	legacyTemporary := path + ".tmp"
	if err := os.WriteFile(legacyTemporary, []byte("do-not-overwrite"), 0o600); err != nil {
		t.Fatal(err)
	}
	value.AccountName = "second"
	if err := Save(&value); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(legacyTemporary)
	if err != nil || string(payload) != "do-not-overwrite" {
		t.Fatalf("状态保存覆盖了可预测的旧临时路径：payload=%q err=%v", payload, err)
	}
	loaded, err := Load(accountID)
	if err != nil || loaded.AccountName != "second" {
		t.Fatalf("随机临时路径保存失败：state=%+v err=%v", loaded, err)
	}
}

func TestLoadRecoversAfterProcessExitBetweenBackupAndPublish(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	accountID := AccountID("process-exit-account")
	snapshot := filepath.Join(home, "accounts", accountID, "snapshots", "generation")
	if err := os.MkdirAll(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	stable := AccountState{
		AccountID:    accountID,
		AccountName:  "stable",
		SnapshotPath: snapshot,
		GenerationID: "generation",
		Storage:      "snapshot-only",
	}
	if err := Save(&stable); err != nil {
		t.Fatal(err)
	}
	path, err := StatePath(accountID)
	if err != nil {
		t.Fatal(err)
	}
	pending := stable
	pending.Version = stateVersion
	pending.AccountName = "pending"
	pending.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	payload, err := json.MarshalIndent(&pending, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(filepath.Dir(path), ".state-crash-injected.tmp")
	if err := os.WriteFile(temporary, append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestStateCommitCrashHelper$")
	command.Env = append(os.Environ(),
		"V_LOCAL_CLI_TEST_STATE_COMMIT_CRASH=1",
		"V_LOCAL_CLI_TEST_STATE_PATH="+path,
		"V_LOCAL_CLI_TEST_STATE_TEMPORARY="+temporary,
	)
	err = command.Run()
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 99 {
		t.Fatalf("expected injected process exit 99, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("current state should be absent at injected crash point: %v", err)
	}
	if _, err := os.Stat(path + ".old"); err != nil {
		t.Fatalf("recoverable backup missing after injected crash: %v", err)
	}
	if _, err := os.Stat(temporary); err != nil {
		t.Fatalf("crash-injected temporary file should demonstrate abrupt-exit residue: %v", err)
	}
	loaded, err := Load(accountID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AccountName != "stable" {
		t.Fatalf("load selected uncommitted state: %+v", loaded)
	}
}

func TestCommitStateFileRestoresOldStateWhenPublishFails(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.json")
	temporary := filepath.Join(directory, ".state-publish-failure.tmp")
	if err := os.WriteFile(path, []byte("stable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temporary, []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := commitStateFile(path, temporary, func() {
		if removeErr := os.Remove(temporary); removeErr != nil {
			t.Fatal(removeErr)
		}
	})
	if err == nil {
		t.Fatal("expected state publish failure")
	}
	payload, readErr := os.ReadFile(path)
	if readErr != nil || string(payload) != "stable" {
		t.Fatalf("old state was not restored: payload=%q err=%v", payload, readErr)
	}
	if _, statErr := os.Stat(path + ".old"); !os.IsNotExist(statErr) {
		t.Fatalf("rollback backup should have returned to current path: %v", statErr)
	}
}
