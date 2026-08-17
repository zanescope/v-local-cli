//go:build windows

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zanescope/v-local-cli/internal/nativeocr"
	"golang.org/x/sys/windows"
)

func TestPublishFilePreservesLockedWindowsTarget(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "locked.json")
	if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	temporary, err := writeTemporaryFileNear(target, []byte("replacement"))
	if err != nil {
		t.Fatal(err)
	}
	pointer, err := windows.UTF16PtrFromString(target)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(pointer, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	publishErr := publishFile(temporary, target)
	if closeErr := windows.CloseHandle(handle); closeErr != nil {
		t.Fatal(closeErr)
	}
	if publishErr == nil {
		t.Fatal("独占锁定的 Windows 目标被覆盖")
	}
	payload, err := os.ReadFile(target)
	if err != nil || string(payload) != "existing" {
		t.Fatalf("覆盖锁定目标失败后旧内容未保留：payload=%q err=%v", payload, err)
	}
	if _, err := os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatalf("覆盖锁定目标失败后临时输出未清理：%v", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(directory, ".v-local-cli-backup-*.old"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("覆盖锁定目标失败后遗留备份：%v err=%v", leftovers, err)
	}
}

func TestRecognizeTemporaryChatImageReportsLockedWindowsPlaintext(t *testing.T) {
	previous := recognizeNativeOCR
	defer func() { recognizeNativeOCR = previous }()
	var handle windows.Handle
	recognizeNativeOCR = func(_ context.Context, path string) (nativeocr.Result, error) {
		pointer, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nativeocr.Result{}, err
		}
		handle, err = windows.CreateFile(pointer, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		return nativeocr.Result{}, err
	}
	directory := t.TempDir()
	_, invoked, operationErr, cleanupErr := recognizeTemporaryChatImage(context.Background(), directory, "png", []byte("image plaintext"))
	if !invoked || operationErr != nil {
		t.Fatalf("recognition setup failed: invoked=%v err=%v", invoked, operationErr)
	}
	if cleanupErr == nil {
		t.Fatal("exclusive Windows lock should make plaintext cleanup failure visible")
	}
	if closeErr := windows.CloseHandle(handle); closeErr != nil {
		t.Fatal(closeErr)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one explicitly reported locked plaintext file: entries=%v err=%v", entries, err)
	}
	if err := os.Remove(filepath.Join(directory, entries[0].Name())); err != nil {
		t.Fatal(err)
	}
}
