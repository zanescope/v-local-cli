package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestMain(m *testing.M) {
	// 使用内存钥匙串，避免依赖 CI 运行环境（尤其是 Linux）中不存在的系统凭据服务。
	// keyring 包是进程单例，这里的初始化对 state 包的凭据读写同样生效。
	keyring.MockInit()
	os.Exit(m.Run())
}

// testHome 返回一个已解析符号链接的私有 home 目录。macOS 的临时目录位于 /var
// （符号链接到 /private/var），而产品会按安全策略拒绝符号链接路径；真实使用的
// ~/Library/Caches 不含符号链接，这里解析后复现真实路径形态，不弱化产品的拒绝逻辑。
func testHome(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("无法解析测试 home 符号链接：%v", err)
	}
	return resolved
}
