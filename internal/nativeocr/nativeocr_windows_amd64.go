//go:build windows && amd64

package nativeocr

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	requestHandshake = 10001
	requestOCR       = 10010
	responseOCR      = 10011
	maxPackageBytes  = 96 * 1024 * 1024
	maxResponseBytes = 8 * 1024 * 1024
)

type installation struct {
	root    string
	version string
	bin     string
	exe     string
	mojo    string
}

func versionParts(value string) []int {
	parts := strings.Split(value, ".")
	result := make([]int, len(parts))
	for index, part := range parts {
		for _, character := range part {
			if character < '0' || character > '9' {
				break
			}
			result[index] = result[index]*10 + int(character-'0')
		}
	}
	return result
}

func versionGreater(left, right string) bool {
	a, b := versionParts(left), versionParts(right)
	for index := 0; index < len(a) || index < len(b); index++ {
		var av, bv int
		if index < len(a) {
			av = a[index]
		}
		if index < len(b) {
			bv = b[index]
		}
		if av != bv {
			return av > bv
		}
	}
	return left > right
}

func validInstalledFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && info.Size() > 0
}

func knownProgramFilesRoots() []string {
	roots := []string{}
	seen := map[string]bool{}
	for _, folderID := range []*windows.KNOWNFOLDERID{
		windows.FOLDERID_ProgramFiles,
		windows.FOLDERID_ProgramFilesX64,
		windows.FOLDERID_ProgramFilesX86,
	} {
		base, err := windows.KnownFolderPath(folderID, windows.KF_FLAG_DEFAULT)
		if err != nil || strings.TrimSpace(base) == "" {
			continue
		}
		root := filepath.Join(base, "Tencent", "Weixin")
		key := strings.ToLower(filepath.Clean(root))
		if !seen[key] {
			seen[key] = true
			roots = append(roots, root)
		}
	}
	return roots
}

func discoverInstallation() (installation, bool) {
	values := []installation{}
	for _, root := range knownProgramFilesRoots() {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			folder := filepath.Join(root, entry.Name())
			value := installation{
				root: folder, version: entry.Name(), bin: filepath.Join(folder, "WeChatOcr.bin"),
				exe: filepath.Join(root, "Weixin.exe"), mojo: filepath.Join(folder, "mmmojo_64.dll"),
			}
			if validInstalledFile(value.bin) && validInstalledFile(value.exe) && validInstalledFile(value.mojo) {
				values = append(values, value)
			}
		}
	}
	if len(values) == 0 {
		return installation{}, false
	}
	sort.Slice(values, func(left, right int) bool { return versionGreater(values[left].version, values[right].version) })
	return values[0], true
}

func Current(showPaths bool) Status {
	status := Status{
		Platform: runtime.GOOS, Architecture: runtime.GOARCH, Source: "installed_wechat_package",
		ExternalDependency: false, PrivateIPC: true, NetworkRequested: false,
		SubprocessSandboxed: false, VendorNoSandbox: true,
	}
	value, found := discoverInstallation()
	if !found {
		status.Reason = "未找到同时包含 WeChatOcr.bin、Weixin.exe 和 mmmojo_64.dll 的微信安装"
		return status
	}
	status.Available = true
	status.WeChatVersion = value.version
	if showPaths {
		status.WeChatPath = value.root
	}
	return status
}

func removeTemporaryPackage(directory string) error {
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for {
		lastErr = os.RemoveAll(directory)
		if _, err := os.Lstat(directory); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func safeArchiveLeaf(name string) bool {
	if name == "" || len(name) > 255 || filepath.Clean(name) != name || filepath.Base(name) != name ||
		strings.ContainsAny(name, `/:\`) || strings.TrimRight(name, " .") != name {
		return false
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}

func extractInstalledPackage(value installation) (string, func() error, error) {
	reader, err := zip.OpenReader(value.bin)
	if err != nil {
		return "", nil, errors.New("微信 OCR 包不是有效的 ZIP 组件")
	}
	defer reader.Close()
	directory, err := os.MkdirTemp("", "v-local-cli-wechat-ocr-*")
	if err != nil {
		return "", nil, err
	}
	_ = os.Chmod(directory, 0o700)
	cleanup := func() error { return removeTemporaryPackage(directory) }
	var total int64
	for _, entry := range reader.File {
		name := entry.Name
		if !safeArchiveLeaf(name) || entry.FileInfo().IsDir() {
			_ = cleanup()
			return "", nil, errors.New("微信 OCR 包包含不安全路径")
		}
		if entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > maxPackageBytes || total+int64(entry.UncompressedSize64) > maxPackageBytes {
			_ = cleanup()
			return "", nil, errors.New("微信 OCR 包大小超过安全上限")
		}
		total += int64(entry.UncompressedSize64)
		source, openErr := entry.Open()
		if openErr != nil {
			_ = cleanup()
			return "", nil, openErr
		}
		targetPath := filepath.Join(directory, name)
		target, createErr := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			_ = source.Close()
			_ = cleanup()
			return "", nil, createErr
		}
		written, copyErr := io.Copy(target, io.LimitReader(source, int64(entry.UncompressedSize64)+1))
		closeErr := target.Close()
		_ = source.Close()
		if copyErr != nil || closeErr != nil || written != int64(entry.UncompressedSize64) {
			_ = cleanup()
			return "", nil, errors.New("微信 OCR 包解压不完整")
		}
	}
	if !validInstalledFile(filepath.Join(directory, "wxocr.dll")) {
		_ = cleanup()
		return "", nil, errors.New("微信 OCR 包缺少 wxocr.dll")
	}
	return directory, cleanup, nil
}

type nativeProcedure struct {
	address uintptr
}

func (procedure *nativeProcedure) Call(arguments ...uintptr) (uintptr, uintptr, error) {
	first, second, callErr := syscall.SyscallN(procedure.address, arguments...)
	if callErr != syscall.Errno(0) {
		return first, second, callErr
	}
	return first, second, nil
}

type mojoProcedures struct {
	module windows.Handle
	createEnvironment, setCallbacks, setInitParams, appendSwitch, startEnvironment, stopEnvironment,
	removeEnvironment, initialize, createWriteInfo, getWriteRequest, sendWriteInfo,
	removeWriteInfo, getReadRequest, removeReadInfo *nativeProcedure
}

func loadMojo(path string) (*mojoProcedures, error) {
	module, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, errors.New("无法从微信安装目录安全加载 mmmojo 组件")
	}
	procedure := func(name string) (*nativeProcedure, error) {
		address, resolveErr := windows.GetProcAddress(module, name)
		if resolveErr != nil {
			return nil, resolveErr
		}
		return &nativeProcedure{address: address}, nil
	}
	names := []string{
		"CreateMMMojoEnvironment", "SetMMMojoEnvironmentCallbacks", "SetMMMojoEnvironmentInitParams",
		"AppendMMSubProcessSwitchNative", "StartMMMojoEnvironment", "StopMMMojoEnvironment",
		"RemoveMMMojoEnvironment", "InitializeMMMojo", "CreateMMMojoWriteInfo", "GetMMMojoWriteInfoRequest",
		"SendMMMojoWriteInfo", "RemoveMMMojoWriteInfo", "GetMMMojoReadInfoRequest", "RemoveMMMojoReadInfo",
	}
	resolved := make([]*nativeProcedure, 0, len(names))
	for _, name := range names {
		value, resolveErr := procedure(name)
		if resolveErr != nil {
			_ = windows.FreeLibrary(module)
			return nil, errors.New("微信 mmmojo 接口与当前实验协议不兼容")
		}
		resolved = append(resolved, value)
	}
	return &mojoProcedures{
		module:            module,
		createEnvironment: resolved[0], setCallbacks: resolved[1], setInitParams: resolved[2], appendSwitch: resolved[3],
		startEnvironment: resolved[4], stopEnvironment: resolved[5], removeEnvironment: resolved[6], initialize: resolved[7],
		createWriteInfo: resolved[8], getWriteRequest: resolved[9], sendWriteInfo: resolved[10], removeWriteInfo: resolved[11],
		getReadRequest: resolved[12], removeReadInfo: resolved[13],
	}, nil
}

type callbackResponse struct {
	taskID uint64
	code   int32
	result Result
	err    error
}

type mojoSession struct {
	procedures  *mojoProcedures
	environment uintptr
	handshake   chan bool
	responses   chan callbackResponse
	failures    chan error
}

var (
	sessionCounter                                                                             atomic.Uint64
	sessionRegistry                                                                            sync.Map
	callbackPointers                                                                           sync.Once
	readPushCallback, connectCallback, disconnectCallback, launchFailedCallback, errorCallback uintptr
)

func sessionFor(value uintptr) *mojoSession {
	found, ok := sessionRegistry.Load(uint64(value))
	if !ok {
		return nil
	}
	return found.(*mojoSession)
}

func copyReadPayload(session *mojoSession, info uintptr) ([]byte, error) {
	var size uint32
	pointer, _, _ := session.procedures.getReadRequest.Call(info, uintptr(unsafe.Pointer(&size)))
	defer session.procedures.removeReadInfo.Call(info)
	if pointer == 0 || size == 0 || size > maxResponseBytes {
		return nil, errors.New("微信 OCR IPC 响应大小无效")
	}
	payload := make([]byte, int(size))
	var read uintptr
	if err := windows.ReadProcessMemory(windows.CurrentProcess(), pointer, &payload[0], uintptr(size), &read); err != nil || read != uintptr(size) {
		return nil, errors.New("读取微信 OCR IPC 响应失败")
	}
	return payload, nil
}

func initializeCallbacks() {
	callbackPointers.Do(func() {
		readPushCallback = syscall.NewCallback(func(requestID, info, userData uintptr) uintptr {
			session := sessionFor(userData)
			if session == nil {
				return 0
			}
			payload, err := copyReadPayload(session, info)
			if err != nil {
				select {
				case session.failures <- err:
				default:
				}
				return 0
			}
			switch uint32(requestID) {
			case requestHandshake:
				offset := 0
				_, _, _, supported, parseErr := readField(payload, &offset)
				select {
				case session.handshake <- parseErr == nil && supported == 1:
				default:
				}
			case responseOCR:
				taskID, code, result, parseErr := parseOCRResponse(payload)
				select {
				case session.responses <- callbackResponse{taskID: taskID, code: code, result: result, err: parseErr}:
				default:
				}
			}
			return 0
		})
		connectCallback = syscall.NewCallback(func(connected, userData uintptr) uintptr {
			if connected == 0 {
				if session := sessionFor(userData); session != nil {
					select {
					case session.failures <- errors.New("微信 OCR IPC 连接失败"):
					default:
					}
				}
			}
			return 0
		})
		disconnectCallback = syscall.NewCallback(func(userData uintptr) uintptr {
			if session := sessionFor(userData); session != nil {
				select {
				case session.failures <- errors.New("微信 OCR IPC 已断开"):
				default:
				}
			}
			return 0
		})
		launchFailedCallback = syscall.NewCallback(func(code, userData uintptr) uintptr {
			if session := sessionFor(userData); session != nil {
				select {
				case session.failures <- fmt.Errorf("微信 OCR 子进程启动失败：%d", code):
				default:
				}
			}
			return 0
		})
		errorCallback = syscall.NewCallback(func(_ uintptr, _ uintptr, userData uintptr) uintptr {
			if session := sessionFor(userData); session != nil {
				select {
				case session.failures <- errors.New("微信 OCR IPC 返回内部错误"):
				default:
				}
			}
			return 0
		})
	})
}

func newSession(value installation, componentDirectory string) (*mojoSession, uint64, error) {
	procedures, err := loadMojo(value.mojo)
	if err != nil {
		return nil, 0, err
	}
	procedures.initialize.Call(0, 0)
	environment, _, _ := procedures.createEnvironment.Call()
	if environment == 0 {
		_ = windows.FreeLibrary(procedures.module)
		return nil, 0, errors.New("无法创建微信 OCR IPC 环境")
	}
	session := &mojoSession{procedures: procedures, environment: environment, handshake: make(chan bool, 1), responses: make(chan callbackResponse, 1), failures: make(chan error, 2)}
	id := sessionCounter.Add(1)
	sessionRegistry.Store(id, session)
	initializeCallbacks()
	setCallback := func(kind uintptr, callback uintptr) { procedures.setCallbacks.Call(environment, kind, callback) }
	procedures.setCallbacks.Call(environment, 0, uintptr(id))
	setCallback(1, readPushCallback)
	setCallback(4, connectCallback)
	setCallback(5, disconnectCallback)
	setCallback(7, launchFailedCallback)
	setCallback(8, errorCallback)
	procedures.setInitParams.Call(environment, 0, 1)
	executable, _ := syscall.UTF16PtrFromString(value.exe)
	procedures.setInitParams.Call(environment, 2, uintptr(unsafe.Pointer(executable)))
	appendSwitch := func(name, setting string) error {
		key, keyErr := syscall.BytePtrFromString(name)
		if keyErr != nil {
			return keyErr
		}
		wide, wideErr := syscall.UTF16PtrFromString(setting)
		if wideErr != nil {
			return wideErr
		}
		procedures.appendSwitch.Call(environment, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(wide)))
		runtime.KeepAlive(key)
		runtime.KeepAlive(wide)
		return nil
	}
	for name, setting := range map[string]string{"no-sandbox": "", "user-lib-dir": value.root, "type": "wxocr", "app-path": componentDirectory} {
		if err := appendSwitch(name, setting); err != nil {
			session.close(id)
			return nil, 0, err
		}
	}
	procedures.startEnvironment.Call(environment)
	runtime.KeepAlive(executable)
	return session, id, nil
}

func (session *mojoSession) close(id uint64) {
	if session.environment != 0 {
		session.procedures.stopEnvironment.Call(session.environment)
		session.procedures.removeEnvironment.Call(session.environment)
		session.environment = 0
	}
	if session.procedures.module != 0 {
		_ = windows.FreeLibrary(session.procedures.module)
		session.procedures.module = 0
	}
	sessionRegistry.Delete(id)
}

func (session *mojoSession) sendRequest(payload []byte) error {
	writeInfo, _, _ := session.procedures.createWriteInfo.Call(1, 0, requestOCR)
	if writeInfo == 0 {
		return errors.New("无法创建微信 OCR IPC 请求")
	}
	pointer, _, _ := session.procedures.getWriteRequest.Call(writeInfo, uintptr(len(payload)))
	if pointer == 0 {
		session.procedures.removeWriteInfo.Call(writeInfo)
		return errors.New("无法分配微信 OCR IPC 请求")
	}
	var written uintptr
	if err := windows.WriteProcessMemory(windows.CurrentProcess(), pointer, &payload[0], uintptr(len(payload)), &written); err != nil || written != uintptr(len(payload)) {
		session.procedures.removeWriteInfo.Call(writeInfo)
		return errors.New("写入微信 OCR IPC 请求失败")
	}
	sent, _, _ := session.procedures.sendWriteInfo.Call(session.environment, writeInfo)
	if sent == 0 {
		session.procedures.removeWriteInfo.Call(writeInfo)
		return errors.New("微信 OCR IPC 请求发送失败")
	}
	runtime.KeepAlive(payload)
	return nil
}

func Recognize(ctx context.Context, imagePath string) (result Result, returnedErr error) {
	value, found := discoverInstallation()
	if !found {
		return Result{}, ErrUnsupported
	}
	absolute, err := filepath.Abs(imagePath)
	if err != nil {
		return Result{}, err
	}
	componentDirectory, cleanup, err := extractInstalledPackage(value)
	if err != nil {
		return Result{}, err
	}
	removed := false
	defer func() {
		cleanupErr := cleanup()
		removed = cleanupErr == nil
		result.TemporaryFilesRemoved = removed
		if cleanupErr != nil && returnedErr == nil {
			returnedErr = errors.New("微信 OCR 临时组件未能完整删除")
		}
	}()
	session, sessionID, err := newSession(value, componentDirectory)
	if err != nil {
		return Result{}, err
	}
	defer session.close(sessionID)
	startup := time.NewTimer(10 * time.Second)
	defer startup.Stop()
	select {
	case supported := <-session.handshake:
		if !supported {
			return Result{}, errors.New("当前微信 OCR 组件拒绝了实验协议")
		}
	case failure := <-session.failures:
		return Result{}, failure
	case <-startup.C:
		return Result{}, errors.New("等待微信 OCR 组件握手超时")
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	const taskID = 2
	if err := session.sendRequest(ocrRequest(taskID, filepath.ToSlash(absolute))); err != nil {
		return Result{}, err
	}
	select {
	case response := <-session.responses:
		if response.err != nil {
			return Result{}, response.err
		}
		if response.taskID != taskID || response.code != 0 {
			return Result{}, fmt.Errorf("微信 OCR 返回错误：%d", response.code)
		}
		response.result.Backend = "wechat_native_experimental"
		response.result.WeChatVersion = value.version
		response.result.PrivateIPCInvoked = true
		response.result.NetworkRequested = false
		return response.result, nil
	case failure := <-session.failures:
		return Result{}, failure
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}
