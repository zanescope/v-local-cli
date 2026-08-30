//go:build darwin

package shadowpublish

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fileStoreRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFileStateStoreAtomicallySeparatesPendingAndReadyWithoutSecret(t *testing.T) {
	root := fileStoreRoot(t)
	store, err := NewFileStateStore(root, testAccount)
	if err != nil {
		t.Fatal(err)
	}
	pending := GenerationState{
		Version: StateVersion, Status: "pending", AccountBindingID: testAccount,
		GenerationID: testNew, BuildSetDigest: testBuild, AttemptID: testAttempt, PreviousGenerationID: testOld,
	}
	if err := store.SavePending(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	ready := oldReady()
	if err := store.SaveReady(context.Background(), ready); err != nil {
		t.Fatal(err)
	}
	loadedPending, found, err := store.LoadPending(context.Background(), testAccount)
	if err != nil || !found || loadedPending.GenerationID != testNew {
		t.Fatalf("pending=%+v found=%v err=%v", loadedPending, found, err)
	}
	loadedReady, found, err := store.LoadReady(context.Background(), testAccount)
	if err != nil || !found || loadedReady.GenerationID != testOld {
		t.Fatalf("ready=%+v found=%v err=%v", loadedReady, found, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".next") {
			t.Fatalf("normal state transaction left temporary residue %q", entry.Name())
		}
		payload, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), testSecret) {
			t.Fatal("secret entered generation state file")
		}
	}
	if err := store.RemovePending(context.Background(), testAccount); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.LoadPending(context.Background(), testAccount); err != nil || found {
		t.Fatalf("pending state remains: found=%v err=%v", found, err)
	}
}

func TestFileStateStoreNeverPromotesInterruptedNext(t *testing.T) {
	root := fileStoreRoot(t)
	store, err := NewFileStateStore(root, testAccount)
	if err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(root, "shadow-generation.pending.json.next")
	if err := os.WriteFile(next, []byte(`{"status":"ready"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.LoadPending(context.Background(), testAccount); err != nil || found {
		t.Fatalf("interrupted next was promoted: found=%v err=%v", found, err)
	}
	if _, err := os.Stat(next); !os.IsNotExist(err) {
		t.Fatalf("interrupted next was not exactly reconciled: %v", err)
	}
}

func TestFileStateStoreAtomicallyReplacesReadyWithoutNextResidue(t *testing.T) {
	root := fileStoreRoot(t)
	store, err := NewFileStateStore(root, testAccount)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveReady(context.Background(), oldReady()); err != nil {
		t.Fatal(err)
	}
	replacement := GenerationState{
		Version: StateVersion, Status: "ready", AccountBindingID: testAccount,
		GenerationID: testNew, BuildSetDigest: testBuild, AttemptID: testAttempt,
		ObsoleteGenerationID: testOld,
	}
	if err := store.SaveReady(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := store.LoadReady(context.Background(), testAccount)
	if err != nil || !found || loaded != replacement {
		t.Fatalf("replacement=%+v found=%v err=%v", loaded, found, err)
	}
	if _, err := os.Stat(filepath.Join(root, readyStateLeaf+".next")); !os.IsNotExist(err) {
		t.Fatalf("atomic ready replacement left next residue: %v", err)
	}
}

func TestFileStateStoreRejectsReplacedRootDirectory(t *testing.T) {
	root := fileStoreRoot(t)
	store, err := NewFileStateStore(root, testAccount)
	if err != nil {
		t.Fatal(err)
	}
	moved := root + ".moved"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveReady(context.Background(), oldReady()); err == nil {
		t.Fatal("state store accepted a replacement root inode")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("replacement root was mutated: entries=%d err=%v", len(entries), err)
	}
}

func TestFileStateStoreRejectsSymlinkAndHardlinkStateTargets(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		root := fileStoreRoot(t)
		store, err := NewFileStateStore(root, testAccount)
		if err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(fileStoreRoot(t), "outside")
		if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "shadow-generation.ready.json")); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveReady(context.Background(), oldReady()); err == nil {
			t.Fatal("state store replaced a symlink target")
		}
	})
	t.Run("hardlink", func(t *testing.T) {
		root := fileStoreRoot(t)
		store, err := NewFileStateStore(root, testAccount)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SaveReady(context.Background(), oldReady()); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "shadow-generation.ready.json")
		if err := os.Link(path, filepath.Join(root, "alias")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.LoadReady(context.Background(), testAccount); err == nil {
			t.Fatal("hard-linked state file was trusted")
		}
	})
}

func TestFileStateStoreHonorsCancelledContext(t *testing.T) {
	root := fileStoreRoot(t)
	store, err := NewFileStateStore(root, testAccount)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.SaveReady(ctx, oldReady()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "shadow-generation.ready.json")); !os.IsNotExist(err) {
		t.Fatal("cancelled state save mutated its target")
	}
}
