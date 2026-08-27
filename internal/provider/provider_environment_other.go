//go:build !darwin

package provider

import "os/exec"

func configureProviderCommandEnvironment(_ *exec.Cmd) {}
