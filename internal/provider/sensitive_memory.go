package provider

import (
	"runtime"
	"sync"
	"unsafe"
)

type sensitiveMemoryKey struct {
	address uintptr
	length  int
}

var sensitiveMemoryRegions sync.Map

func sensitiveRegionKey(value []byte) (sensitiveMemoryKey, bool) {
	if len(value) == 0 {
		return sensitiveMemoryKey{}, false
	}
	return sensitiveMemoryKey{address: uintptr(unsafe.Pointer(unsafe.SliceData(value))), length: len(value)}, true
}

func markSensitiveBytes(value []byte) {
	key, ok := sensitiveRegionKey(value)
	if !ok {
		return
	}
	cleanup := platformExcludeSensitiveMemory(value)
	if cleanup == nil {
		return
	}
	if _, loaded := sensitiveMemoryRegions.LoadOrStore(key, cleanup); loaded {
		cleanup()
	}
	runtime.KeepAlive(value)
}

func unmarkSensitiveBytes(value []byte) {
	if key, ok := sensitiveRegionKey(value); ok {
		if cleanup, loaded := sensitiveMemoryRegions.LoadAndDelete(key); loaded {
			cleanup.(func())()
		}
	}
	runtime.KeepAlive(value)
}

func clearSensitiveBytes(value []byte) {
	unmarkSensitiveBytes(value)
	for index := range value {
		value[index] = 0
	}
	runtime.KeepAlive(value)
}

func appendSensitiveBytesLimited(current, incoming []byte, limit int) ([]byte, bool) {
	if len(incoming) == 0 {
		return current, false
	}
	remaining := limit - len(current)
	if remaining <= 0 {
		return current, true
	}
	chunk := incoming
	over := false
	if len(chunk) > remaining {
		chunk = chunk[:remaining]
		over = true
	}

	unmarkSensitiveBytes(current)
	required := len(current) + len(chunk)
	if required > cap(current) {
		capacity := cap(current) * 2
		if capacity < required {
			capacity = required
		}
		if capacity > limit {
			capacity = limit
		}
		next := make([]byte, len(current), capacity)
		copy(next, current)
		for index := range current {
			current[index] = 0
		}
		runtime.KeepAlive(current)
		current = next
	}
	current = append(current, chunk...)
	markSensitiveBytes(current)
	return current, over
}
