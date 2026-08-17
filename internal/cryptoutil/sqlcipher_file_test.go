package cryptoutil

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDecryptSQLCipherSnapshotFilesReplaysPlainSQLiteWAL(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.db")
	database, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE evidence(value TEXT); INSERT INTO evidence VALUES('from-wal')"); err != nil {
		t.Fatal(err)
	}
	wal := source + "-wal"
	if info, err := os.Stat(wal); err != nil || info.Size() <= 32 {
		t.Fatalf("测试 WAL 未生成：info=%v err=%v", info, err)
	}
	destination := filepath.Join(root, "snapshot.db")
	info, size, err := DecryptSQLCipherSnapshotFiles(source, wal, destination, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != "applied" || info.AppliedPages == 0 || size == 0 {
		t.Fatalf("明文 WAL 未完整回放：info=%+v size=%d", info, size)
	}
	copyDatabase, err := sql.Open("sqlite", "file:"+filepath.ToSlash(destination)+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer copyDatabase.Close()
	var value string
	if err := copyDatabase.QueryRow("SELECT value FROM evidence").Scan(&value); err != nil || value != "from-wal" {
		t.Fatalf("回放后数据不可读：value=%q err=%v", value, err)
	}
}

func TestDecryptSQLCipherSnapshotFilesCopiesPlainSQLiteVariablePageSize(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.db")
	database, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("PRAGMA page_size=1024; VACUUM; CREATE TABLE evidence(value TEXT); INSERT INTO evidence VALUES('plain')"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "snapshot.db")
	info, size, err := DecryptSQLCipherSnapshotFiles(source, "", destination, strings.Repeat("a", 64))
	if err != nil || info.Status != "absent" || size == 0 {
		t.Fatalf("可变页大小明文库复制失败：info=%+v size=%d err=%v", info, size, err)
	}
}
