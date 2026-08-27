package platform

import (
	"runtime"
	"strings"
)

// CanonicalSystemPath 折叠 macOS 的固定系统别名。/etc、/tmp 和 /var 都是指向
// /private 下同名目录的系统别名，即使调用方没有经过任何用户可控链接，
// EvalSymlinks 也会把它们展开成 /private 形式，因此两种写法必须产生同一个路径
// 标识。其它平台原样返回。
//
// 每一处从路径派生稳定标识的地方都必须经过它：账号标识、catalog 路径键、快照文件
// 身份和 acquisition 目录标识只要有一部分不做归一化，同一个账号就会在不同子系统里
// 得到互相矛盾的身份——共用一个 acquisition endpoint 却拿不到同一把账号锁。
//
// 这里只规范化这几个固定前缀。别名之下引入的链接仍会改变规范路径并被上层拒绝。
func CanonicalSystemPath(value string) string {
	return canonicalSystemPath(runtime.GOOS, value)
}

func canonicalSystemPath(goos, value string) string {
	if goos != "darwin" {
		return value
	}
	for _, prefix := range []string{"/private/etc", "/private/tmp", "/private/var"} {
		if value == prefix || strings.HasPrefix(value, prefix+"/") {
			return strings.TrimPrefix(value, "/private")
		}
	}
	return value
}
