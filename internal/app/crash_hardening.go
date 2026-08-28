package app

import (
	"runtime/debug"
	"sync"
)

var (
	crashHardeningOnce sync.Once
	crashHardeningErr  error
)

func hardenSensitiveProcess() error {
	crashHardeningOnce.Do(func() {
		debug.SetTraceback("none")
		crashHardeningErr = hardenPlatformCrashReporting()
	})
	return crashHardeningErr
}
