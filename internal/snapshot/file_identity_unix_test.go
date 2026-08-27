//go:build !windows

package snapshot

import (
	"runtime"
	"testing"
)

func TestPlatformPathKeyFoldsDarwinSystemAlias(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("系统别名折叠只适用于 macOS")
	}
	aliased := platformPathKey("/private/var/folders/test/database.db")
	canonical := platformPathKey("/var/folders/test/database.db")
	if aliased != canonical {
		t.Fatalf("同一源文件的两种系统别名写法产生了不同的快照路径键：%q != %q", aliased, canonical)
	}
	if platformPathKey("/private/variable/database.db") == platformPathKey("/variable/database.db") {
		t.Fatal("非系统别名前缀被当成别名折叠")
	}
}
