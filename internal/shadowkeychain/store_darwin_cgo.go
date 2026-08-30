//go:build darwin && cgo

package shadowkeychain

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>

static CFStringRef vl_string(const char *value) {
    return CFStringCreateWithCString(kCFAllocatorDefault, value, kCFStringEncodingUTF8);
}

static OSStatus vl_shadow_put(const char *service_value, const char *account_value, const unsigned char *bytes, long length) {
    CFStringRef service = vl_string(service_value);
    CFStringRef account = vl_string(account_value);
    CFDataRef data = CFDataCreate(kCFAllocatorDefault, bytes, (CFIndex)length);
    if (service == NULL || account == NULL || data == NULL) {
        if (service != NULL) CFRelease(service);
        if (account != NULL) CFRelease(account);
        if (data != NULL) CFRelease(data);
        return errSecAllocate;
    }
    const void *keys[] = { kSecClass, kSecAttrService, kSecAttrAccount, kSecValueData, kSecAttrAccessible };
    const void *values[] = { kSecClassGenericPassword, service, account, data, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly };
    CFDictionaryRef query = CFDictionaryCreate(kCFAllocatorDefault, keys, values, 5,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    OSStatus status = query == NULL ? errSecAllocate : SecItemAdd(query, NULL);
    if (query != NULL) CFRelease(query);
    CFRelease(data);
    CFRelease(account);
    CFRelease(service);
    return status;
}

static OSStatus vl_shadow_get(const char *service_value, const char *account_value, CFDataRef *result) {
    CFStringRef service = vl_string(service_value);
    CFStringRef account = vl_string(account_value);
    if (service == NULL || account == NULL) {
        if (service != NULL) CFRelease(service);
        if (account != NULL) CFRelease(account);
        return errSecAllocate;
    }
    const void *keys[] = { kSecClass, kSecAttrService, kSecAttrAccount, kSecReturnData, kSecMatchLimit };
    const void *values[] = { kSecClassGenericPassword, service, account, kCFBooleanTrue, kSecMatchLimitOne };
    CFDictionaryRef query = CFDictionaryCreate(kCFAllocatorDefault, keys, values, 5,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFTypeRef copied = NULL;
    OSStatus status = query == NULL ? errSecAllocate : SecItemCopyMatching(query, &copied);
    if (query != NULL) CFRelease(query);
    CFRelease(account);
    CFRelease(service);
    if (status == errSecSuccess && copied != NULL && CFGetTypeID(copied) == CFDataGetTypeID()) {
        *result = (CFDataRef)copied;
    } else {
        if (copied != NULL) CFRelease(copied);
        *result = NULL;
        if (status == errSecSuccess) status = errSecDecode;
    }
    return status;
}

static long vl_shadow_data_length(CFDataRef data) {
    return data == NULL ? 0 : (long)CFDataGetLength(data);
}

static void vl_shadow_data_copy(CFDataRef data, unsigned char *output, long length) {
    if (data != NULL && output != NULL && length > 0) {
        CFDataGetBytes(data, CFRangeMake(0, (CFIndex)length), output);
    }
}

static void vl_shadow_data_release(CFDataRef data) {
    if (data != NULL) CFRelease(data);
}

static OSStatus vl_shadow_delete(const char *service_value, const char *account_value) {
    CFStringRef service = vl_string(service_value);
    CFStringRef account = vl_string(account_value);
    if (service == NULL || account == NULL) {
        if (service != NULL) CFRelease(service);
        if (account != NULL) CFRelease(account);
        return errSecAllocate;
    }
    const void *keys[] = { kSecClass, kSecAttrService, kSecAttrAccount };
    const void *values[] = { kSecClassGenericPassword, service, account };
    CFDictionaryRef query = CFDictionaryCreate(kCFAllocatorDefault, keys, values, 3,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    OSStatus status = query == NULL ? errSecAllocate : SecItemDelete(query);
    if (query != NULL) CFRelease(query);
    CFRelease(account);
    CFRelease(service);
    return status;
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

const shadowKeychainService = "v-local-cli-shadow-v1"

type platformStore struct{}

func newPlatformStore() (itemStore, error) { return platformStore{}, nil }

func keychainNames(item string) (*C.char, *C.char, func()) {
	service := C.CString(shadowKeychainService)
	account := C.CString(item)
	return service, account, func() {
		C.free(unsafe.Pointer(service))
		C.free(unsafe.Pointer(account))
	}
}

func (platformStore) Put(item string, payload []byte) error {
	if len(payload) == 0 || len(payload) > chunkBytes {
		return errors.New("Shadow Keychain item size is invalid")
	}
	service, account, release := keychainNames(item)
	defer release()
	status := C.vl_shadow_put(service, account, (*C.uchar)(unsafe.Pointer(&payload[0])), C.long(len(payload)))
	if status != C.errSecSuccess {
		return errors.New("Shadow Keychain item write failed")
	}
	return nil
}

func (platformStore) Get(item string) ([]byte, bool, error) {
	service, account, release := keychainNames(item)
	defer release()
	var data C.CFDataRef
	status := C.vl_shadow_get(service, account, &data)
	if status == C.errSecItemNotFound {
		return nil, false, nil
	}
	if status != C.errSecSuccess || data == 0 {
		return nil, false, errors.New("Shadow Keychain item read failed")
	}
	defer C.vl_shadow_data_release(data)
	length := int(C.vl_shadow_data_length(data))
	if length <= 0 || length > chunkBytes {
		return nil, false, errors.New("Shadow Keychain item length is invalid")
	}
	payload := make([]byte, length)
	C.vl_shadow_data_copy(data, (*C.uchar)(unsafe.Pointer(&payload[0])), C.long(length))
	return payload, true, nil
}

func (platformStore) Delete(item string) error {
	service, account, release := keychainNames(item)
	defer release()
	status := C.vl_shadow_delete(service, account)
	if status != C.errSecSuccess && status != C.errSecItemNotFound {
		return errors.New("Shadow Keychain item deletion failed")
	}
	return nil
}
