//go:build darwin

package provider

import "os/exec"

// configureProviderCommandEnvironment prevents the caller from injecting
// loader, debugger, language-runtime or tool-resolution settings into the
// security boundary process. Acquisition requests contain explicit roots, so
// the provider does not need the ambient user environment for discovery.
func configureProviderCommandEnvironment(command *exec.Cmd) {
	command.Env = []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"LC_ALL=C",
		"LANG=C",
		"HOME=/var/empty",
		"TMPDIR=/tmp",
	}
}
