package platform

import "testing"

func TestCanonicalSystemPathFoldsDarwinAliasesAndKeepsSuffixVisible(t *testing.T) {
	for input, want := range map[string]string{
		"/private/var/folders/test/provider": "/var/folders/test/provider",
		"/private/tmp/provider":              "/tmp/provider",
		"/private/etc/hosts":                 "/etc/hosts",
		"/private/var":                       "/var",
		// 只折叠这几个固定前缀：相邻但不同的目录名不能被当成别名。
		"/private/variable/provider": "/private/variable/provider",
		"/private/vars":              "/private/vars",
		// 已经是规范写法时保持不变，别名之下的路径原样保留以便上层继续检查链接。
		"/var/folders/test/provider": "/var/folders/test/provider",
		"/private":                   "/private",
	} {
		if got := canonicalSystemPath("darwin", input); got != want {
			t.Fatalf("canonicalSystemPath(darwin, %q)=%q, want %q", input, got, want)
		}
	}
}

func TestCanonicalSystemPathLeavesOtherPlatformsUntouched(t *testing.T) {
	// 在 Linux 上 /private/var 是普通目录，折叠它会把两个不同位置合并成同一个标识。
	// Windows 上路径分隔符不同，任何改写都会改变既有的账号与 catalog 标识。
	for _, goos := range []string{"linux", "windows"} {
		for _, input := range []string{
			"/private/var/folders/test/provider", "/private/tmp/provider",
			`C:\Users\test\AppData\Local\v-local-cli`,
		} {
			if got := canonicalSystemPath(goos, input); got != input {
				t.Fatalf("canonicalSystemPath(%s, %q)=%q, want unchanged", goos, input, got)
			}
		}
	}
}
