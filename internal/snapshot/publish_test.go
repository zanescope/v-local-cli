package snapshot

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestPublishDirectoryMovesGeneration(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, ".stage-x")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(root, "generation-x")
	if err := publishDirectory(stage, final); err != nil {
		t.Fatalf("正常发布失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(final, "manifest.json")); err != nil {
		t.Fatalf("发布后清单缺失：%v", err)
	}
}

// 不可重试的错误必须立刻上报，不能被退避循环拖住。
func TestPublishDirectoryReportsPermanentFailureImmediately(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "does-not-exist")
	err := publishDirectory(missing, filepath.Join(root, "target"))
	if err == nil {
		t.Fatal("源目录不存在时应当报错")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("期望 ErrNotExist，实际 %v", err)
	}
}

func TestTransientRenameErrorClassification(t *testing.T) {
	// ERROR_ACCESS_DENIED(5) 是实际观察到的瞬时失败；32/33 是同类句柄争用，
	// 且标准库不会把它们映射到 fs.ErrPermission，必须按错误码识别。
	windowsContention := []syscall.Errno{5, 32, 33}
	for _, code := range windowsContention {
		got := transientRenameError(&os.LinkError{Op: "rename", Err: code})
		if got != (runtime.GOOS == "windows") {
			t.Errorf("errno %d 的可重试判定为 %v，与平台预期不符", code, got)
		}
	}
	if transientRenameError(&os.LinkError{Op: "rename", Err: fs.ErrNotExist}) {
		t.Error("不存在的路径不应被判为可重试")
	}
	if transientRenameError(nil) {
		t.Error("nil 不应被判为可重试")
	}
}
