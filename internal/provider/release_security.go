package provider

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// buildMode 由签名构建脚本改为 "release"。开发二进制有意保留显式 fixture，供协议测试使用。
var buildMode = "development"

// releaseSignerSHA256 在签名前注入 Windows release 二进制，使 CLI 与 Provider 绑定到
// 同一张 Authenticode 叶证书。
var releaseSignerSHA256 string

// releaseTeamID 在签名前注入 macOS 发布二进制，把 CLI 与 Provider 同时绑定到发行方
// 的 Developer ID Team。只比较两者 Team ID 相等是不够的：任何持有 Developer ID 证书
// 的人都能签出一对自洽的二进制。这与 Windows 侧固定叶证书 SHA-256 是同一强度。
var releaseTeamID string

var ErrComponentUntrusted = errors.New("v-local-key-provider 未通过发行版信任验证")

func releaseBuild() bool {
	return strings.EqualFold(strings.TrimSpace(buildMode), "release")
}

func fixedProviderInstallPathFor(platform, architecture, base string) string {
	if strings.TrimSpace(base) == "" {
		return ""
	}
	directory := ""
	binary := "v-local-key-provider"
	switch platform {
	case "windows":
		directory = "windows-" + architecture
		binary += ".exe"
	case "darwin":
		directory = "darwin-" + architecture
	default:
		return ""
	}
	return filepath.Join(base, "v-local", "key-provider", directory, binary)
}

func fixedProviderInstallPath() string {
	var base string
	switch runtime.GOOS {
	case "windows":
		base = strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	case "darwin":
		base, _ = os.UserConfigDir()
	default:
		return ""
	}
	return fixedProviderInstallPathFor(runtime.GOOS, runtime.GOARCH, base)
}
