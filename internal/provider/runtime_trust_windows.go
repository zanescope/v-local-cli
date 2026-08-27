//go:build windows

package provider

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wintrustRuntime                     = windows.NewLazySystemDLL("wintrust.dll")
	wtHelperProvDataFromStateData       = wintrustRuntime.NewProc("WTHelperProvDataFromStateData")
	wtHelperGetProvSignerFromChain      = wintrustRuntime.NewProc("WTHelperGetProvSignerFromChain")
	wtHelperGetProvCertificateFromChain = wintrustRuntime.NewProc("WTHelperGetProvCertFromChain")
)

type cryptProviderCertificate struct {
	Size        uint32
	Certificate *windows.CertContext
}

func pointerReturnedByWinTrust(address uintptr) unsafe.Pointer {
	// LazyProc.Call 会把原生指针返回值暴露为 uintptr。该内存在 WTD_STATEACTION_CLOSE
	// 之前归 WinTrust 所有，因此立即以指针类型保存其位模式，不对其做算术运算，也不让
	// 整数值超过原生生命周期继续存在。
	return *(*unsafe.Pointer)(unsafe.Pointer(&address))
}

func verifiedWindowsSignerSHA256(data *windows.WinTrustData) (string, error) {
	if data == nil || data.StateData == 0 || wtHelperProvDataFromStateData.Find() != nil ||
		wtHelperGetProvSignerFromChain.Find() != nil || wtHelperGetProvCertificateFromChain.Find() != nil {
		return "", errors.New("Authenticode signer chain is unavailable")
	}
	providerData, _, _ := wtHelperProvDataFromStateData.Call(uintptr(data.StateData))
	if providerData == 0 {
		return "", errors.New("Authenticode provider state is unavailable")
	}
	signer, _, _ := wtHelperGetProvSignerFromChain.Call(providerData, 0, 0, 0)
	if signer == 0 {
		return "", errors.New("Authenticode primary signer is unavailable")
	}
	certificatePointer, _, _ := wtHelperGetProvCertificateFromChain.Call(signer, 0)
	if certificatePointer == 0 {
		return "", errors.New("Authenticode signer certificate is unavailable")
	}
	providerCertificate := (*cryptProviderCertificate)(pointerReturnedByWinTrust(certificatePointer))
	if providerCertificate == nil || providerCertificate.Size < uint32(unsafe.Sizeof(cryptProviderCertificate{})) {
		return "", errors.New("Authenticode signer certificate is invalid")
	}
	certificate := providerCertificate.Certificate
	if certificate == nil || certificate.EncodedCert == nil || certificate.Length == 0 || certificate.Length > 16*1024*1024 {
		return "", errors.New("Authenticode signer certificate is invalid")
	}
	encoded := unsafe.Slice(certificate.EncodedCert, int(certificate.Length))
	digest := sha256.Sum256(encoded)
	runtime.KeepAlive(data)
	return hex.EncodeToString(digest[:]), nil
}

func expectedWindowsSignerSHA256() (string, error) {
	expected := strings.ToLower(strings.TrimSpace(releaseSignerSHA256))
	decoded, err := hex.DecodeString(expected)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("release Authenticode signer identity is not embedded")
	}
	return expected, nil
}

func verifyWindowsAuthenticode(path string) error {
	expectedSigner, err := expectedWindowsSignerSHA256()
	if err != nil {
		return err
	}
	filePath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	file := &windows.WinTrustFileInfo{Size: uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})), FilePath: filePath}
	data := &windows.WinTrustData{
		Size: uint32(unsafe.Sizeof(windows.WinTrustData{})), UIChoice: windows.WTD_UI_NONE,
		// 运行时采集绝不能让签名验证变成网络操作。release 验证会在发布前完成依赖联网和
		// 吊销状态的检查；运行时则在不获取 URL 的情况下验证嵌入的 Authenticode 签名和
		// 已缓存的信任链。
		RevocationChecks: windows.WTD_REVOKE_NONE, UnionChoice: windows.WTD_CHOICE_FILE,
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(file),
		ProvFlags: windows.WTD_REVOCATION_CHECK_NONE | windows.WTD_CACHE_ONLY_URL_RETRIEVAL |
			windows.WTD_DISABLE_MD2_MD4,
	}
	verifyErr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	actualSigner := ""
	var signerErr error
	if verifyErr == nil {
		actualSigner, signerErr = verifiedWindowsSignerSHA256(data)
	}
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	_ = windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	if verifyErr != nil {
		return errors.New("Authenticode verification failed")
	}
	if signerErr != nil {
		return signerErr
	}
	if subtle.ConstantTimeCompare([]byte(actualSigner), []byte(expectedSigner)) != 1 {
		return errors.New("Authenticode signer does not match the release identity")
	}
	return nil
}

func validateProviderExecutableTrust(path string) (string, error) {
	if !releaseBuild() {
		return "development_unverified", nil
	}
	expected := fixedProviderInstallPath()
	provider, ok := canonicalExecutable(path)
	if expected == "" || !ok || !sameCanonicalPathText(provider, expected) {
		return "untrusted", errors.New("release Provider is outside the fixed installation path")
	}
	current, err := os.Executable()
	if err != nil {
		return "untrusted", err
	}
	current, err = filepath.Abs(current)
	if err != nil {
		return "untrusted", err
	}
	if err := verifyWindowsAuthenticode(current); err != nil {
		return "untrusted", errors.New("CLI Authenticode verification failed")
	}
	if err := verifyWindowsAuthenticode(provider); err != nil {
		return "untrusted", err
	}
	return "authenticode_verified", nil
}

func validateProviderHelperTrust(_, _ string) (string, error) {
	return "not_applicable", nil
}

func windowsProcessImagePath(pid int) (string, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(process)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil || size == 0 {
		return "", errors.New("daemon process image is unavailable")
	}
	return filepath.Clean(windows.UTF16ToString(buffer[:size])), nil
}

func validateAcquisitionDaemonProcessIdentity(endpoint acquisitionDaemonEndpoint, providerPath string) error {
	if !releaseBuild() {
		return nil
	}
	if endpoint.DaemonPath == "" || !sameExecutablePath(endpoint.DaemonPath, providerPath) {
		return errors.New("release daemon did not advertise the fixed Provider image")
	}
	actual, err := windowsProcessImagePath(endpoint.PID)
	if err != nil || !sameExecutablePath(actual, endpoint.DaemonPath) {
		return errors.New("daemon PID is not running the advertised image")
	}
	_, err = validateProviderExecutableTrust(endpoint.DaemonPath)
	return err
}
