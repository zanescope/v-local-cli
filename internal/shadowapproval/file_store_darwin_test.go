//go:build darwin

package shadowapproval

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
)

func approvalFileVectors(t *testing.T) contract.GoldenVectors {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "testdata", "shadow-contract-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors contract.GoldenVectors
	if err := contract.DecodeStrict(payload, &vectors); err != nil || vectors.Validate() != nil {
		t.Fatalf("invalid vectors: %v", err)
	}
	return vectors
}

func approvalRoot(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "approvals")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFileStorePersistsAndConsumesExactChallengeOnce(t *testing.T) {
	root := approvalRoot(t)
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	challenge := approvalFileVectors(t).Challenge
	if err := store.Save(context.Background(), challenge); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := store.Load(context.Background())
	if err != nil || !found || loaded != challenge {
		t.Fatalf("challenge load failed: found=%v err=%v value=%+v", found, err, loaded)
	}

	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})
	for index := 0; index < 2; index++ {
		go func() {
			ready.Done()
			<-start
			results <- store.Remove(context.Background(), challenge.ChallengeID)
		}()
	}
	ready.Wait()
	close(start)
	successes := 0
	got := make([]error, 0, 2)
	for index := 0; index < 2; index++ {
		result := <-results
		got = append(got, result)
		if result == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("single-use approval had %d successful consumers: %v", successes, got)
	}
	if _, found, err := store.Load(context.Background()); err != nil || found {
		t.Fatalf("consumed challenge remains: found=%v err=%v", found, err)
	}
}

func TestFileStoreRejectsLinksAndCleansOnlyFixedNextLeaf(t *testing.T) {
	root := approvalRoot(t)
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	challenge := approvalFileVectors(t).Challenge
	if err := store.Save(context.Background(), challenge); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, challengeFileName), filepath.Join(root, "second-link")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(context.Background()); err == nil {
		t.Fatal("hard-linked approval challenge was accepted")
	}

	symlinkRoot := filepath.Join(t.TempDir(), "approval-link")
	if err := os.Symlink(root, symlinkRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(symlinkRoot); err == nil {
		t.Fatal("symlinked approval root was accepted")
	}
}

func TestFileStoreRemovesInterruptedNextBeforeLoad(t *testing.T) {
	root := approvalRoot(t)
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(root, challengeNextFileName)
	if err := os.WriteFile(next, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Load(context.Background()); err != nil || found {
		t.Fatalf("interrupted next cleanup failed: found=%v err=%v", found, err)
	}
	if _, err := os.Lstat(next); !os.IsNotExist(err) {
		t.Fatalf("fixed next leaf remains: %v", err)
	}
}

func TestFileStoreRejectsReplacedRootDirectory(t *testing.T) {
	root := approvalRoot(t)
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), approvalFileVectors(t).Challenge); err == nil {
		t.Fatal("approval store accepted a replacement root inode")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("replacement approval root was mutated: entries=%d err=%v", len(entries), err)
	}
}
