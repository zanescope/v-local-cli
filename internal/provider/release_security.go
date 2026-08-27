package provider

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// buildMode is changed to "release" by the signed build scripts. Development
// binaries intentionally keep explicit fixtures available for protocol tests.
var buildMode = "development"

// releaseSignerSHA256 is injected into Windows release binaries before signing.
// It binds the CLI and Provider to the same Authenticode leaf certificate.
var releaseSignerSHA256 string

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
