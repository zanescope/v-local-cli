package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestMain(m *testing.M) {
	// 使用内存钥匙串，避免依赖 CI 运行环境（尤其是 Linux）中不存在的系统凭据服务。
	keyring.MockInit()
	os.Exit(m.Run())
}

// testHome 解析符号链接后返回私有 home 目录：macOS 临时目录在 /var（符号链接），
// 产品按安全策略拒绝符号链接路径，真实使用的缓存目录无符号链接。见 app 包同名说明。
func testHome(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("无法解析测试 home 符号链接：%v", err)
	}
	return resolved
}
