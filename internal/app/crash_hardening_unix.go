//go:build darwin || linux

package app

import "golang.org/x/sys/unix"

func hardenPlatformCrashReporting() error {
	return unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0})
}
