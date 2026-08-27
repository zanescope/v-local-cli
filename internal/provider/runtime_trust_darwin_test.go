//go:build darwin

package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinTrustedDirectoryTreeRejectsWritableAncestor(t *testing.T) {
	root, err := os.MkdirTemp(".", ".darwin-cli-trust-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "v-local", "key-provider", "darwin-test")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := darwinTrustedDirectoryTree(nested); err != nil {
		t.Fatalf("private direct directory tree was rejected: %v", err)
	}
	if err := os.Chmod(filepath.Dir(nested), 0o770); err != nil {
		t.Fatal(err)
	}
	if err := darwinTrustedDirectoryTree(nested); err == nil {
		t.Fatal("group-writable component ancestor was accepted")
	}
}
