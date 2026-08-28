//go:build windows

package provider

import (
	"os/exec"
	"syscall"
)

func configureDetachedProviderCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200, HideWindow: true}
}
