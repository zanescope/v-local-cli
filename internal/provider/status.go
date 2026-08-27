package provider

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
)

// Protocol 是首个公开的密钥提供器协议。首次发布前已移除从未发布的 v2 开发常量。
const Protocol = "v-local-key-provider/v1"
const EnvironmentVariable = "V_LOCAL_CLI_KEY_PROVIDER"

type Status struct {
	Available       bool   `json:"executable_present"`
	Source          string `json:"source"`
	Name            string `json:"name"`
	Path            string `json:"path,omitempty"`
	Platform        string `json:"platform"`
	Protocol        string `json:"protocol"`
	Integrity       string `json:"integrity"`
	HelperRequired  bool   `json:"helper_required"`
	HelperAvailable bool   `json:"helper_executable_present"`
	HelperName      string `json:"helper_name,omitempty"`
	HelperPath      string `json:"helper_path,omitempty"`
	HelperIntegrity string `json:"helper_integrity"`
}

func Current(explicit string) Status {
	path, source := resolveCandidate(explicit)
	available := false
	name := "v-local-key-provider"
	if path != "" {
		name = filepath.Base(path)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			available = true
		}
	}
	integrity := "missing"
	if available {
		integrity, _ = validateProviderExecutableTrust(path)
		if integrity == "" {
			integrity = "untrusted"
		}
	} else if source == "override_rejected" {
		integrity = "override_rejected"
	}
	status := Status{
		Available: available,
		Source:    source,
		Name:      name,
		Path:      path,
		Platform:  platformName(),
		Protocol:  Protocol,
		Integrity: integrity,
	}
	if runtime.GOOS == "darwin" {
		status.HelperRequired = true
		status.HelperName = "v-local-key-provider-helper"
		status.HelperIntegrity = "missing"
		helper := ""
		if !releaseBuild() {
			helper = os.Getenv("V_LOCAL_KEY_PROVIDER_HELPER")
		}
		if helper == "" && path != "" {
			helper = filepath.Join(filepath.Dir(path), status.HelperName)
		}
		if resolved, ok := canonicalExecutable(helper); ok && resolved != path {
			status.HelperAvailable = true
			status.HelperPath = resolved
			status.HelperName = filepath.Base(resolved)
			status.HelperIntegrity, _ = validateProviderHelperTrust(path, resolved)
			if status.HelperIntegrity == "" {
				status.HelperIntegrity = "untrusted"
			}
		}
	} else {
		status.HelperIntegrity = "not_applicable"
	}
	return status
}

// Resolve 返回密钥提供器的可执行文件路径及来源。
func Resolve(explicit string) (string, string) {
	path, source := resolveCandidate(explicit)
	if path == "" {
		return "", source
	}
	if _, err := validateProviderExecutableTrust(path); err != nil {
		return "", "untrusted_" + source
	}
	return path, source
}

func resolveCandidate(explicit string) (string, string) {
	if releaseBuild() {
		if strings.TrimSpace(explicit) != "" || strings.TrimSpace(os.Getenv(EnvironmentVariable)) != "" {
			return "", "override_rejected"
		}
		path, ok := canonicalExecutable(fixedProviderInstallPath())
		if !ok {
			return "", "fixed_install"
		}
		return path, "fixed_install"
	}
	if explicit != "" {
		path, ok := canonicalExecutable(explicit)
		if !ok {
			return "", "explicit"
		}
		return path, "explicit"
	}
	if configured := os.Getenv(EnvironmentVariable); configured != "" {
		path, ok := canonicalExecutable(configured)
		if !ok {
			return "", "environment"
		}
		return path, "environment"
	}
	if found, err := exec.LookPath("v-local-key-provider"); err == nil {
		path, ok := canonicalExecutable(found)
		if ok {
			return path, "path"
		}
	}
	return "", "missing"
}

func canonicalExecutable(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	candidate := value
	if !filepath.IsAbs(candidate) && !strings.ContainsAny(candidate, `/\`) {
		found, err := exec.LookPath(candidate)
		if err != nil {
			return "", false
		}
		candidate = found
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", false
	}
	info, err := os.Lstat(absolute)
	unsafePath := false
	if err == nil {
		unsafePath, err = providerPathIsLinkOrReparse(absolute, info.Mode())
	}
	if err != nil || unsafePath || !info.Mode().IsRegular() {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || !sameCanonicalPathText(absolute, resolved) {
		return "", false
	}
	return resolved, true
}

func sameCanonicalPathText(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return localplatform.CanonicalSystemPath(left) == localplatform.CanonicalSystemPath(right)
}

func platformName() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	default:
		return runtime.GOOS
	}
}
