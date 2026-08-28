//go:build darwin

package state

import "testing"

// 账号标识决定账号状态目录、keyring 条目和 setup/refresh 的账号级文件锁，而
// acquisition endpoint 与跨重启 checkpoint 由 provider 侧的目录标识决定。两者必须
// 对同一个账号得出同一个身份，否则同一账号的两种系统别名写法会共用一个 acquisition
// endpoint，却拿不到同一把账号锁，snapshot_busy 互斥随之失效。
func TestAccountIDFoldsDarwinSystemAlias(t *testing.T) {
	aliased := AccountID("/private/var/folders/test/account")
	canonical := AccountID("/var/folders/test/account")
	if aliased != canonical {
		t.Fatalf("同一账号的两种系统别名写法产生了不同的账号标识：%q != %q", aliased, canonical)
	}
	if AccountID("/private/variable/account") == AccountID("/variable/account") {
		t.Fatal("非系统别名前缀被当成别名折叠")
	}
}
