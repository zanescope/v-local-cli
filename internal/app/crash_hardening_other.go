//go:build !darwin && !linux && !windows

package app

func hardenPlatformCrashReporting() error { return nil }
