//go:build darwin

package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinPrivateDirectoryUsesOwnerOnlyPermissions(t *testing.T) {
	home := filepath.Join(testHome(t), "private-home")
	t.Setenv("V_LOCAL_CLI_HOME", home)
	accountID := AccountID("darwin-private-directory")
	temporary, err := EnsureExportTempPath(accountID)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{home, filepath.Join(home, "accounts"), filepath.Dir(temporary), temporary} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("macOS 私有目录权限不是 0700：path=%q mode=%#o", path, info.Mode().Perm())
		}
	}
}

func TestDarwinPrivateHierarchyRejectsAccountSymlink(t *testing.T) {
	root := testHome(t)
	home := filepath.Join(root, "private-home")
	accounts := filepath.Join(home, "accounts")
	if err := os.MkdirAll(accounts, 0o700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external-account")
	if err := os.MkdirAll(external, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("V_LOCAL_CLI_HOME", home)
	accountID := AccountID("darwin-symlink-account")
	if err := os.Symlink(external, filepath.Join(accounts, accountID)); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureExportTempPath(accountID); err == nil {
		t.Fatal("macOS 私有账号路径不应跟随符号链接")
	}
}
