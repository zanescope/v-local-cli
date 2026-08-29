//go:build !windows

package wxgfqual

import "os/exec"

func runProviderCommand(command *exec.Cmd) (ProviderIsolation, error) {
	return ProviderIsolation{Method: "none"}, command.Run()
}
