package cryptoutil

import (
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// craftWALWithCommitSize 构造一个校验和完全合法、但提交记录中 db-size 可任意指定的 WAL。
// WAL 校验算法是公开且无密钥的，所以这种伪造对攻击者没有门槛。
func craftWALWithCommitSize(page []byte, pageNumber, claimedPages uint32) []byte {
	header := make([]byte, 32)
	binary.BigEndian.PutUint32(header[0:4], 0x377f0682) // 小端校验和变体
	binary.BigEndian.PutUint32(header[4:8], 3007000)
	binary.BigEndian.PutUint32(header[8:12], SQLCipherPageSize)
	binary.BigEndian.PutUint32(header[12:16], 1)
	binary.BigEndian.PutUint32(header[16:20], 0x11223344) // salt-1
	binary.BigEndian.PutUint32(header[20:24], 0x55667788) // salt-2
	first, second, _ := walChecksum(header[:24], false, 0, 0)
	binary.BigEndian.PutUint32(header[24:28], first)
	binary.BigEndian.PutUint32(header[28:32], second)

	frame := make([]byte, 24)
	binary.BigEndian.PutUint32(frame[0:4], pageNumber)
	binary.BigEndian.PutUint32(frame[4:8], claimedPages) // 提交标记：提交后的库页数
	copy(frame[8:16], header[16:24])                     // salt 必须与头一致
	first, second, _ = walChecksum(frame[:8], false, first, second)
	first, second, _ = walChecksum(page, false, first, second)
	binary.BigEndian.PutUint32(frame[16:20], first)
	binary.BigEndian.PutUint32(frame[20:24], second)

	return append(append(header, frame...), page...)
}

func plainDatabaseForWALTest(t *testing.T, root string) (string, []byte) {
	t.Helper()
	source := filepath.Join(root, "source.db")
	database, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE evidence(value TEXT); INSERT INTO evidence VALUES('x')"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	return source, original
}

// 回归：WAL 提交记录里的 db-size 曾被直接用于 Truncate，8 KB 主库可被撑成任意大小
// （uint32 上限约 17.6 TB），随后 snapshot 的 sha256File 会把整个文件读一遍。
func TestScanWALFileRejectsCommitSizeBeyondAvailablePages(t *testing.T) {
	root := t.TempDir()
	source, original := plainDatabaseForWALTest(t, root)
	mainPages := len(original) / SQLCipherPageSize

	walPath := source + "-wal"
	payload := craftWALWithCommitSize(original[:SQLCipherPageSize], 1, 5000)
	if err := os.WriteFile(walPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(root, "snapshot.db")
	info, size, err := DecryptSQLCipherSnapshotFiles(source, walPath, destination, strings.Repeat("a", 64))
	if err == nil {
		t.Fatalf("越界的 WAL 提交大小被接受：状态=%s 输出=%d 字节（主库仅 %d 页）",
			info.Status, size, mainPages)
	}
	if info.Status != "invalid_commit_size" {
		t.Fatalf("越界提交大小的状态应为 invalid_commit_size，实际为 %q", info.Status)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatal("拒绝 WAL 后没有清理已写出的快照文件")
	}
}

// 上界不能误伤正常提交：主库页数以内的提交必须照常回放。
func TestScanWALFileAcceptsCommitSizeWithinAvailablePages(t *testing.T) {
	root := t.TempDir()
	source, original := plainDatabaseForWALTest(t, root)
	mainPages := uint32(len(original) / SQLCipherPageSize)

	walPath := source + "-wal"
	payload := craftWALWithCommitSize(original[:SQLCipherPageSize], 1, mainPages)
	if err := os.WriteFile(walPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(root, "snapshot.db")
	info, size, err := DecryptSQLCipherSnapshotFiles(source, walPath, destination, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("合法 WAL 提交被上界误伤：%v", err)
	}
	if info.Status != "applied" || info.CommittedFrames != 1 {
		t.Fatalf("合法 WAL 未被回放：状态=%s 提交帧=%d", info.Status, info.CommittedFrames)
	}
	if size != int64(mainPages)*SQLCipherPageSize {
		t.Fatalf("回放后大小异常：%d 字节，期望 %d", size, int64(mainPages)*SQLCipherPageSize)
	}
}
