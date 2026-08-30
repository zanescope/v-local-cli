//go:build darwin

package shadowaccount

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
)

func TestResolveCurrentUsesAccountDatabaseAndIgnoresEnvironmentPaths(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uid := os.Geteuid()
	t.Setenv("HOME", filepath.Join(home, "untrusted-home"))
	t.Setenv("TMPDIR", filepath.Join(home, "untrusted-tmp"))
	record, err := resolveCurrent(func(id string) (*user.User, error) {
		return &user.User{Uid: strconv.Itoa(uid), HomeDir: home}, nil
	}, uid)
	if err != nil {
		t.Fatal(err)
	}
	if record.Home != home || record.BindingID == "" ||
		record.SecurityRoot != filepath.Join(home, "Library", "Application Support", "v-local", "shadow-runtime") {
		t.Fatalf("unexpected account binding: %#v", record)
	}
}

func TestResolveCurrentRejectsLinkedHome(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(base, "linked")
	if err := os.Symlink(base, linked); err != nil {
		t.Fatal(err)
	}
	uid := os.Geteuid()
	if _, err := resolveCurrent(func(string) (*user.User, error) {
		return &user.User{Uid: strconv.Itoa(uid), HomeDir: linked}, nil
	}, uid); err == nil {
		t.Fatal("linked account home was accepted")
	}
}
