//go:build windows

package provider

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32SensitiveMemory          = windows.NewLazySystemDLL("kernel32.dll")
	werRegisterExcludedMemoryBlock   = kernel32SensitiveMemory.NewProc("WerRegisterExcludedMemoryBlock")
	werUnregisterExcludedMemoryBlock = kernel32SensitiveMemory.NewProc("WerUnregisterExcludedMemoryBlock")
)

func platformExcludeSensitiveMemory(value []byte) func() {
	if len(value) == 0 || werRegisterExcludedMemoryBlock.Find() != nil || werUnregisterExcludedMemoryBlock.Find() != nil {
		return nil
	}
	address := unsafe.Pointer(unsafe.SliceData(value))
	result, _, _ := werRegisterExcludedMemoryBlock.Call(uintptr(address), uintptr(len(value)))
	runtime.KeepAlive(value)
	if int32(result) < 0 {
		return nil
	}
	return func() {
		_, _, _ = werUnregisterExcludedMemoryBlock.Call(uintptr(address))
		runtime.KeepAlive(value)
	}
}
