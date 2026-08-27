//go:build !windows

package provider

import (
	"os/exec"
	"syscall"
)

func configureDetachedProviderCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
