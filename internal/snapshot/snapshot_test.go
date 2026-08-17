package snapshot

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestVerifiedKeysExpandsGlobalCandidate(t *testing.T) {
	key := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	report := Report{Results: []DatabaseResult{
		{Database: "contact/contact.db", Status: "decrypted"},
		{Database: "message/message_0.db", Status: "failed"},
	}}
	verified := VerifiedKeys(map[string]string{"*": key}, report)
	if len(verified) != 1 || verified["contact/contact.db"] != key {
		t.Fatalf("逐库候选异常：%v", verified)
	}
}

func TestStableCopyTreatsEmptyOptionalWALAsAbsent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "database.db-wal")
	if err := os.WriteFile(source, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, present, err := stableCopy(source, filepath.Join(root, "copy.wal"), true, maxStableWALBytes)
	if err != nil || present {
		t.Fatalf("空 WAL 应当作为无已提交帧：present=%v err=%v", present, err)
	}
}

func createPlainDatabase(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE evidence(value TEXT); INSERT INTO evidence VALUES(?)", value); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPreventsCoverageRegressionAndKeepsPreviousSnapshot(t *testing.T) {
	key := strings.Repeat("a", 64)
	root := t.TempDir()
	initialSource := filepath.Join(root, "initial")
	generations := filepath.Join(root, "snapshots")
	createPlainDatabase(t, filepath.Join(initialSource, "contact", "contact.db"), "contact")
	createPlainDatabase(t, filepath.Join(initialSource, "message", "message.db"), "message")
	_, initial, err := BuildGeneration(initialSource, generations, map[string]string{"*": key}, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}

	regressedSource := filepath.Join(root, "regressed")
	createPlainDatabase(t, filepath.Join(regressedSource, "contact", "contact.db"), "new-contact")
	report, _, err := BuildGeneration(regressedSource, generations, map[string]string{"*": key}, BuildOptions{PreventCoverageRegression: true, PreviousSnapshot: initial.Path})
	var regression *CoverageRegressionError
	if !errors.As(err, &regression) || regression.Comparison.MissingPrevious != 1 || len(regression.Comparison.MissingDatabases) != 1 || regression.Comparison.MissingDatabases[0] != "message/message.db" || report.PublicationCoverage == nil {
		t.Fatalf("覆盖退化未被拒绝：report=%+v err=%v", report, err)
	}
	if _, err := os.Stat(filepath.Join(initial.Path, "message", "message.db")); err != nil {
		t.Fatalf("退化候选替换了旧快照：%v", err)
	}
	stages, err := filepath.Glob(filepath.Join(generations, ".stage-*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("退化候选的暂存目录未清理：%v err=%v", stages, err)
	}
}

func TestBuildAllowsNonRegressiveCoverage(t *testing.T) {
	key := strings.Repeat("a", 64)
	root := t.TempDir()
	initialSource := filepath.Join(root, "initial")
	generations := filepath.Join(root, "snapshots")
	createPlainDatabase(t, filepath.Join(initialSource, "contact", "contact.db"), "contact")
	_, initial, err := BuildGeneration(initialSource, generations, map[string]string{"*": key}, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}

	expandedSource := filepath.Join(root, "expanded")
	createPlainDatabase(t, filepath.Join(expandedSource, "contact", "contact.db"), "new-contact")
	createPlainDatabase(t, filepath.Join(expandedSource, "message", "message.db"), "message")
	report, _, err := BuildGeneration(expandedSource, generations, map[string]string{"*": key}, BuildOptions{PreventCoverageRegression: true, PreviousSnapshot: initial.Path})
	if err != nil || report.PublicationCoverage == nil || report.PublicationCoverage.PreviousDatabases != 1 || report.PublicationCoverage.CandidateDatabases != 2 || report.PublicationCoverage.MissingPrevious != 0 {
		t.Fatalf("非退化候选未发布：report=%+v err=%v", report, err)
	}
}

func TestBuildGenerationPublishesManifestAndGarbageCollectsSafely(t *testing.T) {
	key := strings.Repeat("a", 64)
	root := t.TempDir()
	source := filepath.Join(root, "source")
	generations := filepath.Join(root, "generations")
	createPlainDatabase(t, filepath.Join(source, "contact", "contact.db"), "contact")
	report, first, err := BuildGeneration(source, generations, map[string]string{"*": key}, BuildOptions{CreatorVersion: "test"})
	if err != nil || report.Summary.Decrypted != 1 || first.ID == "" || first.ManifestSHA256 == "" {
		t.Fatalf("首个代际发布失败：report=%+v generation=%+v err=%v", report, first, err)
	}
	payload, err := os.ReadFile(filepath.Join(first.Path, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != first.ManifestSHA256 {
		t.Fatal("manifest 摘要未绑定发布内容")
	}
	var manifest Manifest
	if err := json.Unmarshal(payload, &manifest); err != nil || manifest.GenerationID != first.ID || manifest.CreatorVersion != "test" || len(manifest.Databases) != 1 || manifest.Databases[0].PlainSHA256 == "" {
		t.Fatalf("manifest 内容异常：%+v err=%v", manifest, err)
	}
	createPlainDatabase(t, filepath.Join(source, "message", "message.db"), "message")
	report, second, err := BuildGeneration(source, generations, map[string]string{"*": key}, BuildOptions{
		CreatorVersion: "test", PreviousSnapshot: first.Path, PreventCoverageRegression: true,
	})
	if err != nil || report.PublicationCoverage == nil || report.PublicationCoverage.CandidateDatabases != 2 {
		t.Fatalf("第二代发布失败：report=%+v generation=%+v err=%v", report, second, err)
	}
	staging := filepath.Join(generations, ".stage-crashed")
	obsolete := filepath.Join(generations, "00000000-obsolete")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(obsolete, 0o700); err != nil {
		t.Fatal(err)
	}
	gcReport, err := GarbageCollect(generations, second.Path, 1, false)
	if err != nil || gcReport.RemovedStaging != 1 || gcReport.RemovedGenerations != 1 {
		t.Fatalf("代际清理异常：report=%+v err=%v", gcReport, err)
	}
	for _, retained := range []string{first.Path, second.Path} {
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("清理删除了保留代际 %s：%v", retained, err)
		}
	}
}
