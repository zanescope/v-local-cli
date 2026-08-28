//go:build darwin

package provider

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	darwinCLIIdentifier      = "com.zanescope.v-local-cli"
	darwinProviderIdentifier = "com.zanescope.v-local-key-provider"
	darwinHelperIdentifier   = "com.zanescope.v-local-key-provider.helper"
)

var errDarwinCodesignOutputLimit = errors.New("codesign output exceeded the safety limit")

type darwinCodesignBuffer struct {
	limitedBuffer
	onLimit func()
}

func (buffer *darwinCodesignBuffer) Write(value []byte) (int, error) {
	written, _ := buffer.limitedBuffer.Write(value)
	if buffer.over {
		if buffer.onLimit != nil {
			buffer.onLimit()
		}
		return written, errDarwinCodesignOutputLimit
	}
	return written, nil
}

type darwinCodeIdentity struct {
	identifier  string
	teamID      string
	developerID bool
}

func darwinTrustedDirectoryTree(directory string) error {
	uid := uint32(os.Geteuid())
	for current := filepath.Clean(directory); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("component directory tree is not direct")
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil || !sameCanonicalPathText(current, resolved) {
			return errors.New("component directory tree is not canonical")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != uid && stat.Uid != 0) || info.Mode().Perm()&0o022 != 0 {
			return errors.New("component directory tree owner or write permissions are not trusted")
		}
		if parent := filepath.Dir(current); parent == current {
			return nil
		}
	}
}

func darwinTrustedExecutable(path, expectedName string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil || filepath.Base(absolute) != expectedName {
		return "", errors.New("component path or name is not fixed")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode()&0o111 == 0 {
		return "", errors.New("component is not a directly installed executable")
	}
	parent := filepath.Dir(absolute)
	if err := darwinTrustedDirectoryTree(parent); err != nil {
		return "", err
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("component parent is not a direct directory")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil || !sameFilePath(parent, resolvedParent) {
		return "", errors.New("component parent contains a symbolic link")
	}
	fileStat, fileOK := info.Sys().(*syscall.Stat_t)
	uid := uint32(os.Geteuid())
	if !fileOK || (fileStat.Uid != uid && fileStat.Uid != 0) || info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("component owner or write permissions are not trusted")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || !sameFilePath(absolute, resolved) {
		return "", errors.New("component path is not canonical")
	}
	return absolute, nil
}

func darwinCodesign(arguments ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/codesign", arguments...)
	command.Env = []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C", "LANG=C", "HOME=/var/empty", "TMPDIR=/tmp",
	}
	stdout := darwinCodesignBuffer{limitedBuffer: limitedBuffer{limit: 256 * 1024}, onLimit: cancel}
	stderr := darwinCodesignBuffer{limitedBuffer: limitedBuffer{limit: 256 * 1024}, onLimit: cancel}
	defer stdout.Clear()
	defer stderr.Clear()
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if ctx.Err() != nil || stdout.over || stderr.over || len(stdout.Bytes())+len(stderr.Bytes()) > 256*1024 {
		return nil, errors.New("codesign verification did not finish safely")
	}
	output := make([]byte, 0, len(stdout.Bytes())+len(stderr.Bytes()))
	output = append(output, stdout.Bytes()...)
	output = append(output, stderr.Bytes()...)
	markSensitiveBytes(output)
	return output, err
}

func darwinIdentity(path, expectedIdentifier string) (darwinCodeIdentity, error) {
	verified, err := darwinCodesign("--verify", "--strict", "--verbose=2", path)
	clearSensitiveBytes(verified)
	if err != nil {
		return darwinCodeIdentity{}, errors.New("component code signature is invalid")
	}
	details, err := darwinCodesign("--display", "--verbose=4", path)
	defer clearSensitiveBytes(details)
	if err != nil {
		return darwinCodeIdentity{}, errors.New("component code identity is unavailable")
	}
	requirement, err := darwinCodesign("--display", "--requirements", "-", path)
	defer clearSensitiveBytes(requirement)
	if err != nil {
		return darwinCodeIdentity{}, errors.New("component designated requirement is unavailable")
	}
	identity := darwinCodeIdentity{}
	for _, line := range strings.Split(string(details), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Identifier="):
			identity.identifier = strings.TrimSpace(strings.TrimPrefix(line, "Identifier="))
		case strings.HasPrefix(line, "TeamIdentifier="):
			identity.teamID = strings.TrimSpace(strings.TrimPrefix(line, "TeamIdentifier="))
		case strings.HasPrefix(line, "Authority=Developer ID Application:"):
			identity.developerID = true
		}
	}
	if identity.identifier != expectedIdentifier || identity.teamID == "" || !identity.developerID ||
		!strings.Contains(string(requirement), "anchor apple generic") {
		return darwinCodeIdentity{}, errors.New("component is not bound to the expected Developer ID requirement")
	}
	return identity, nil
}

func validateProviderExecutableTrust(path string) (string, error) {
	if !releaseBuild() {
		return unverifiedBuildIntegrity(), nil
	}
	expected := fixedProviderInstallPath()
	if expected == "" || !sameCanonicalPathText(path, expected) {
		return "untrusted", errors.New("release Provider is outside the fixed installation path")
	}
	provider, err := darwinTrustedExecutable(path, "v-local-key-provider")
	if err != nil {
		return "untrusted", err
	}
	cli, err := os.Executable()
	if err != nil {
		return "untrusted", err
	}
	cli, err = darwinTrustedExecutable(cli, "v-local-cli")
	if err != nil {
		return "untrusted", err
	}
	expectedTeam, err := expectedDarwinTeamID()
	if err != nil {
		return "untrusted", err
	}
	cliIdentity, err := darwinIdentity(cli, darwinCLIIdentifier)
	if err != nil || !sameDarwinTeamID(cliIdentity.teamID, expectedTeam) {
		return "untrusted", errors.New("CLI is not signed by the release Developer ID team")
	}
	providerIdentity, err := darwinIdentity(provider, darwinProviderIdentifier)
	if err != nil || !sameDarwinTeamID(providerIdentity.teamID, expectedTeam) {
		return "untrusted", errors.New("Provider is not signed by the release Developer ID team")
	}
	return "developer_id_verified", nil
}

// expectedDarwinTeamID 返回编译期绑定的 Developer ID Team。Apple 的 Team ID 是 10 位
// 大写字母数字；没有注入或格式不对时一律失败关闭，不退回「两边相等即可」。
func expectedDarwinTeamID() (string, error) {
	value := strings.ToUpper(strings.TrimSpace(releaseTeamID))
	if len(value) != 10 {
		return "", errors.New("release Developer ID team identity is not embedded")
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'A' || character > 'Z') {
			return "", errors.New("release Developer ID team identity is not embedded")
		}
	}
	return value, nil
}

func sameDarwinTeamID(actual, expected string) bool {
	return expected != "" && strings.EqualFold(strings.TrimSpace(actual), expected)
}

func validateProviderHelperTrust(providerPath, helperPath string) (string, error) {
	if !releaseBuild() {
		return unverifiedBuildIntegrity(), nil
	}
	provider, err := darwinTrustedExecutable(providerPath, "v-local-key-provider")
	if err != nil {
		return "untrusted", err
	}
	helper, err := darwinTrustedExecutable(helperPath, "v-local-key-provider-helper")
	if err != nil || !sameFilePath(filepath.Dir(provider), filepath.Dir(helper)) {
		return "untrusted", errors.New("helper is not a fixed Provider sibling")
	}
	expectedTeam, err := expectedDarwinTeamID()
	if err != nil {
		return "untrusted", err
	}
	providerIdentity, err := darwinIdentity(provider, darwinProviderIdentifier)
	if err != nil || !sameDarwinTeamID(providerIdentity.teamID, expectedTeam) {
		return "untrusted", errors.New("Provider is not signed by the release Developer ID team")
	}
	helperIdentity, err := darwinIdentity(helper, darwinHelperIdentifier)
	if err != nil || !sameDarwinTeamID(helperIdentity.teamID, expectedTeam) {
		return "untrusted", errors.New("helper is not signed by the release Developer ID team")
	}
	return "developer_id_verified", nil
}

const (
	// proc_info(2) 的参数顺序是 (callnum, pid, flavor, arg, buffer, buffersize)。
	// libproc 的 proc_pidpath 等价于用 PROC_INFO_CALL_PIDINFO 作 callnum、
	// PROC_PIDPATHINFO 作 flavor 调用它；漏掉 callnum 会让其余参数整体错位，内核
	// 一律返回 EINVAL。发布件是 CGO_ENABLED=0 构建的，无法直接链接 libproc。
	darwinProcInfoCallPIDInfo = 2
	darwinProcPIDPathInfo     = 11
	// PROC_PIDPATHINFO_MAXSIZE
	darwinProcPIDPathMaxSize = 4 * 1024
)

func darwinProcessImagePath(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("daemon process identifier is invalid")
	}
	buffer := make([]byte, darwinProcPIDPathMaxSize)
	_, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO,
		darwinProcInfoCallPIDInfo, uintptr(pid), darwinProcPIDPathInfo, 0,
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if errno != 0 {
		return "", errors.New("daemon process image is unavailable")
	}
	// 返回值不是可依赖的长度：libproc 自己也只用它区分成败，再用 NUL 终止符取长度。
	end := bytes.IndexByte(buffer, 0)
	if end <= 0 {
		return "", errors.New("daemon process image is empty")
	}
	return filepath.Clean(string(buffer[:end])), nil
}

func validateAcquisitionDaemonProcessIdentity(endpoint acquisitionDaemonEndpoint, providerPath string) error {
	if !releaseBuild() {
		return nil
	}
	if endpoint.DaemonPath == "" || filepath.Base(endpoint.DaemonPath) != "v-local-key-provider-helper" {
		return errors.New("release daemon did not advertise the companion helper")
	}
	actual, err := darwinProcessImagePath(endpoint.PID)
	if err != nil || !sameExecutablePath(actual, endpoint.DaemonPath) {
		return errors.New("daemon PID is not running the advertised helper image")
	}
	if _, err := validateProviderExecutableTrust(providerPath); err != nil {
		return err
	}
	_, err = validateProviderHelperTrust(providerPath, endpoint.DaemonPath)
	return err
}
