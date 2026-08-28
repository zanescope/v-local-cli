//go:build !windows

package provider

import (
	"runtime"
	"testing"
)

func TestCredentialPathKeyFoldsDarwinSystemAlias(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("系统别名折叠只适用于 macOS")
	}
	aliased := credentialPathKey("/private/var/folders/test/database.db")
	canonical := credentialPathKey("/var/folders/test/database.db")
	if aliased != canonical {
		t.Fatalf("同一数据库的两种系统别名写法产生了不同的 catalog 路径键：%q != %q", aliased, canonical)
	}
	if other := credentialPathKey("/private/variable/database.db"); other == credentialPathKey("/variable/database.db") {
		t.Fatal("非系统别名前缀被当成别名折叠")
	}
}
