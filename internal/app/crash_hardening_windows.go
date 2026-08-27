//go:build windows

package app

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32CrashHardening = windows.NewLazySystemDLL("kernel32.dll")
	werGetFlags            = kernel32CrashHardening.NewProc("WerGetFlags")
	werSetFlags            = kernel32CrashHardening.NewProc("WerSetFlags")
)

const werFaultReportingFlagNoHeap = 0x00000001

func hardenPlatformCrashReporting() error {
	windows.SetErrorMode(windows.SEM_FAILCRITICALERRORS | windows.SEM_NOGPFAULTERRORBOX | windows.SEM_NOOPENFILEERRORBOX)
	if werGetFlags.Find() != nil || werSetFlags.Find() != nil {
		return errors.New("Windows Error Reporting hardening is unavailable")
	}
	var flags uint32
	result, _, _ := werGetFlags.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&flags)))
	if int32(result) < 0 {
		return errors.New("Windows Error Reporting flags are unavailable")
	}
	result, _, _ = werSetFlags.Call(uintptr(flags | werFaultReportingFlagNoHeap))
	if int32(result) < 0 {
		return errors.New("Windows Error Reporting heap collection could not be disabled")
	}
	return nil
}
