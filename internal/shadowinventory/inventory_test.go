package shadowinventory

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func canonicalRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestScanNormalizesDirectoriesAndSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS App inode/link-count inventory is intentionally unavailable on Windows")
	}
	root := filepath.Join(canonicalRoot(t), "Source.app")
	if err := os.MkdirAll(filepath.Join(root, "Contents", "Resources"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Contents", "Resources", "value.txt"), []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("value.txt", filepath.Join(root, "Contents", "Resources", "value.link")); err != nil {
		t.Fatal(err)
	}
	entries, digest, err := Scan(root)
	if err != nil || len(entries) != 4 || len(digest) != 64 {
		t.Fatalf("inventory entries=%d digest=%q err=%v", len(entries), digest, err)
	}
	for _, entry := range entries {
		if entry.Type != "file" && entry.Size != 0 {
			t.Fatalf("non-file size was not normalized: %+v", entry)
		}
	}
}

func TestSafeRelativeUsesHostIndependentWirePaths(t *testing.T) {
	if !safeRelative("Contents/Frameworks/helper") {
		t.Fatalf("slash-delimited inventory path was rejected on %s", runtime.GOOS)
	}
	for _, value := range []string{"", "/absolute", "../escape", "a/../escape", `a\\escape`, "C:/escape", "file:stream"} {
		if safeRelative(value) {
			t.Errorf("unsafe inventory path %q was accepted", value)
		}
	}
}

func TestScanRejectsHardLinkedFile(t *testing.T) {
	root := filepath.Join(canonicalRoot(t), "Source.app")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(root, "first")
	if err := os.WriteFile(first, []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, filepath.Join(root, "second")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if _, _, err := Scan(root); err == nil {
		t.Fatal("inventory accepted a hard-linked file")
	}
}

func TestScanHonorsCancelledContext(t *testing.T) {
	root := filepath.Join(canonicalRoot(t), "Source.app")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := ScanContext(ctx, root); err == nil {
		t.Fatal("inventory ignored cancellation")
	}
}
