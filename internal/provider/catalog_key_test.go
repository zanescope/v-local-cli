package provider

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCatalogKeyForPrivateRootIsStableAndPrivate(t *testing.T) {
	root := t.TempDir()
	first, err := catalogKeyForPrivateRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalogKeyForPrivateRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("catalog key changed between acquisitions")
	}
	decoded, err := hex.DecodeString(first)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("catalog key was not a 32-byte hex value: %q", first)
	}
	info, err := os.Lstat(filepath.Join(root, catalogKeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("catalog key is not a regular file: %v", info.Mode())
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("catalog key is accessible to group or other users: %v", info.Mode().Perm())
	}
}

func TestCatalogKeyForPrivateRootRejectsCorruptExistingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, catalogKeyFileName)
	if err := os.WriteFile(path, []byte("not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := catalogKeyForPrivateRoot(root); err == nil {
		t.Fatal("corrupt catalog key was accepted or silently replaced")
	}
}
