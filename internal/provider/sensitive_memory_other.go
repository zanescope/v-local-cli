//go:build !windows

package provider

func platformExcludeSensitiveMemory(_ []byte) func() { return nil }
