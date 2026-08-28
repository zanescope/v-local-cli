//go:build windows

package provider

import (
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

func TestPointerReturnedByWinTrustPreservesNativeAddress(t *testing.T) {
	expected := &cryptProviderCertificate{Size: 1234}
	actual := (*cryptProviderCertificate)(pointerReturnedByWinTrust(uintptr(unsafe.Pointer(expected))))
	if actual != expected || actual.Size != expected.Size {
		t.Fatal("WinTrust pointer return did not preserve the native address")
	}
	runtime.KeepAlive(expected)
}

func TestExpectedWindowsSignerRequiresExactSHA256(t *testing.T) {
	previous := releaseSignerSHA256
	t.Cleanup(func() { releaseSignerSHA256 = previous })

	releaseSignerSHA256 = "not-a-certificate-digest"
	if _, err := expectedWindowsSignerSHA256(); err == nil {
		t.Fatal("invalid release signer identity was accepted")
	}
	releaseSignerSHA256 = strings.Repeat("a", 64)
	if actual, err := expectedWindowsSignerSHA256(); err != nil || actual != releaseSignerSHA256 {
		t.Fatalf("valid release signer identity was rejected: actual=%q err=%v", actual, err)
	}
}
