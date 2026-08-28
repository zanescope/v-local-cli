//go:build darwin

package provider

import "os/exec"

// configureProviderCommandEnvironment 防止调用方向安全边界进程注入 loader、debugger、
// language runtime 或工具解析设置。采集请求包含明确的 root，因此 Provider 执行发现时
// 不需要用户的环境变量。
func configureProviderCommandEnvironment(command *exec.Cmd) {
	command.Env = []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"LC_ALL=C",
		"LANG=C",
		"HOME=/var/empty",
		"TMPDIR=/tmp",
	}
}
