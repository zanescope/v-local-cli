package snapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestArchitectureMatchesManifestEvidenceFields(t *testing.T) {
	encoded, err := json.Marshal(DatabaseResult{SourceSize: 1, SourceModTime: 2, PlainSHA256: "digest"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"source_size"`, `"source_mtime_ns"`, `"plain_sha256"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("manifest 数据库记录缺少字段：%s", field)
		}
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位快照文档契约测试")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	payload, err := os.ReadFile(filepath.Join(root, "references", "architecture.md"))
	if err != nil {
		t.Fatal(err)
	}
	documentation := string(payload)
	for _, statement := range []string{"源文件大小/修改时间", "明文 SHA-256", "不是密码学来源签名"} {
		if !strings.Contains(documentation, statement) {
			t.Errorf("架构文档缺少 manifest 证据边界：%s", statement)
		}
	}
}
