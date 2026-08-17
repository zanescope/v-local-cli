package state

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestArchitectureDocumentsCurrentStateVersion(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位状态文档契约测试")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	payload, err := os.ReadFile(filepath.Join(root, "references", "architecture.md"))
	if err != nil {
		t.Fatal(err)
	}
	documentation := string(payload)
	expected := fmt.Sprintf("当前状态格式为 v%d", stateVersion)
	if !strings.Contains(documentation, expected) {
		t.Fatalf("架构文档没有声明当前状态版本：%s", expected)
	}
	if strings.Contains(documentation, "支持从 v1 读取迁移") {
		t.Fatal("架构文档宣称了实现中不存在的 v1 隐式迁移")
	}
}
