//go:build darwin && cgo

package shadowkeychain

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

static OSStatus vl_copy_unique_hash(SecStaticCodeRef code, unsigned char *output, long capacity, long *length) {
    CFDictionaryRef information = NULL;
    OSStatus status = SecCodeCopySigningInformation(code, kSecCSSigningInformation, &information);
    if (status != errSecSuccess || information == NULL) {
        if (information != NULL) CFRelease(information);
        return status == errSecSuccess ? errSecInternalComponent : status;
    }
    CFTypeRef value = CFDictionaryGetValue(information, kSecCodeInfoUnique);
    if (value == NULL || CFGetTypeID(value) != CFDataGetTypeID()) {
        CFRelease(information);
        return errSecCSInvalidObjectRef;
    }
    CFIndex count = CFDataGetLength((CFDataRef)value);
    if (count <= 0 || count > capacity) {
        CFRelease(information);
        return errSecCSInvalidObjectRef;
    }
    CFDataGetBytes((CFDataRef)value, CFRangeMake(0, count), output);
    *length = (long)count;
    CFRelease(information);
    return errSecSuccess;
}

static OSStatus vl_static_code_hash(const char *path, unsigned char *output, long capacity, long *length) {
    CFURLRef url = CFURLCreateFromFileSystemRepresentation(kCFAllocatorDefault,
        (const UInt8 *)path, (CFIndex)strlen(path), false);
    if (url == NULL) return errSecAllocate;
    SecStaticCodeRef code = NULL;
    OSStatus status = SecStaticCodeCreateWithPath(url, kSecCSDefaultFlags, &code);
    CFRelease(url);
    if (status == errSecSuccess) status = vl_copy_unique_hash(code, output, capacity, length);
    if (code != NULL) CFRelease(code);
    return status;
}

static OSStatus vl_running_code_hash(int pid, unsigned char *output, long capacity, long *length) {
    CFNumberRef pid_value = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &pid);
    if (pid_value == NULL) return errSecAllocate;
    const void *keys[] = { kSecGuestAttributePid };
    const void *values[] = { pid_value };
    CFDictionaryRef attributes = CFDictionaryCreate(kCFAllocatorDefault, keys, values, 1,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFRelease(pid_value);
    if (attributes == NULL) return errSecAllocate;
    SecCodeRef dynamic_code = NULL;
    OSStatus status = SecCodeCopyGuestWithAttributes(NULL, attributes, kSecCSDefaultFlags, &dynamic_code);
    CFRelease(attributes);
    SecStaticCodeRef static_code = NULL;
    if (status == errSecSuccess) {
        status = SecCodeCopyStaticCode(dynamic_code, kSecCSDefaultFlags, &static_code);
    }
    if (status == errSecSuccess) status = vl_copy_unique_hash(static_code, output, capacity, length);
    if (static_code != NULL) CFRelease(static_code);
    if (dynamic_code != NULL) CFRelease(dynamic_code);
    return status;
}
*/
import "C"

import (
	"encoding/hex"
	"errors"
	"unsafe"
)

func codeHashResult(status C.OSStatus, output *C.uchar, length C.long) (string, error) {
	if status != C.errSecSuccess || length <= 0 || length > 64 {
		return "", errors.New("Shadow Keychain helper code identity is unavailable")
	}
	payload := C.GoBytes(unsafe.Pointer(output), C.int(length))
	return hex.EncodeToString(payload), nil
}

func staticHelperCodeHash(path string) (string, error) {
	value := C.CString(path)
	defer C.free(unsafe.Pointer(value))
	var output [64]C.uchar
	var length C.long
	status := C.vl_static_code_hash(value, &output[0], C.long(len(output)), &length)
	return codeHashResult(status, &output[0], length)
}

func runningHelperCodeHash(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("Shadow Keychain helper PID is invalid")
	}
	var output [64]C.uchar
	var length C.long
	status := C.vl_running_code_hash(C.int(pid), &output[0], C.long(len(output)), &length)
	return codeHashResult(status, &output[0], length)
}
