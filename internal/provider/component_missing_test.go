package provider

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
)

// 传入无法解析为可执行文件的显式路径时，Acquire 必须返回可被 errors.Is 识别的
// ErrComponentMissing，setup 据此把「组件未安装」与「组件已安装但取证失败」分开报告。
func TestAcquireReturnsComponentMissingWhenUnresolved(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-key-component")
	if _, err := Acquire(context.Background(), missing, localplatform.Account{}); !errors.Is(err, ErrComponentMissing) {
		t.Fatalf("组件无法解析时应返回 ErrComponentMissing，实际：%v", err)
	}
}
