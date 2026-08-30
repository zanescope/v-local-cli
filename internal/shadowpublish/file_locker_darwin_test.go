//go:build darwin

package shadowpublish

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func publicationLockRoot(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "state")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFileLockerSerializesWithoutPersistentLockLeaf(t *testing.T) {
	root := publicationLockRoot(t)
	first, err := NewFileLocker(root, testAccount)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFileLocker(root, testAccount)
	if err != nil {
		t.Fatal(err)
	}
	release, err := first.Acquire(context.Background(), testAccount)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := second.Acquire(ctx, testAccount); err == nil {
		t.Fatal("second publication transaction bypassed directory lock")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("publication lock created persistent residue: entries=%d err=%v", len(entries), err)
	}
}

func TestFileLockerRejectsReplacedRootDirectory(t *testing.T) {
	root := publicationLockRoot(t)
	locker, err := NewFileLocker(root, testAccount)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := locker.Acquire(context.Background(), testAccount); err == nil {
		t.Fatal("publication locker accepted a replacement root inode")
	}
}
