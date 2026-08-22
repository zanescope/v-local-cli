//go:build !windows

package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateHierarchyAllowsSymlinkBeforeConfiguredRoot(t *testing.T) {
	realParent := filepath.Join(t.TempDir(), "real-parent")
	realRoot := filepath.Join(realParent, "private-home")
	if err := os.MkdirAll(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(t.TempDir(), "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(linkedParent, "private-home")
	t.Setenv("V_LOCAL_CLI_HOME", home)

	if err := securePrivateDirectory(filepath.Join(home, "accounts")); err != nil {
		t.Fatalf("私有根目录之前的系统或调用方符号链接不应导致拒绝：%v", err)
	}
}

func TestPrivateHierarchyRejectsConfiguredRootSymlink(t *testing.T) {
	realRoot := filepath.Join(t.TempDir(), "real-private-home")
	if err := os.MkdirAll(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "private-home")
	if err := os.Symlink(realRoot, home); err != nil {
		t.Fatal(err)
	}
	t.Setenv("V_LOCAL_CLI_HOME", home)

	if err := securePrivateDirectory(home); err == nil {
		t.Fatal("配置的私有根目录本身是符号链接时必须拒绝")
	}
}
