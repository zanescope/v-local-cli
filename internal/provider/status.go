package provider

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Protocol v2 在请求里加入 deadline_ms，让提供器按调用方给的时限自我收敛，
// 在被杀之前返回已验证出的密钥和诊断，而不是丢失全部工作。
const Protocol = "v-local-key-provider/v2"
const EnvironmentVariable = "V_LOCAL_CLI_KEY_PROVIDER"

type Status struct {
	Available       bool   `json:"available"`
	Source          string `json:"source"`
	Name            string `json:"name"`
	Path            string `json:"path,omitempty"`
	Platform        string `json:"platform"`
	Protocol        string `json:"protocol"`
	Integrity       string `json:"integrity"`
	HelperRequired  bool   `json:"helper_required"`
	HelperAvailable bool   `json:"helper_available"`
	HelperName      string `json:"helper_name,omitempty"`
	HelperPath      string `json:"helper_path,omitempty"`
	HelperIntegrity string `json:"helper_integrity"`
}

func Current(explicit string) Status {
	path, source := resolve(explicit)
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
		integrity = "not_verified_by_cli"
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
		helper := os.Getenv("V_LOCAL_KEY_PROVIDER_HELPER")
		if helper == "" && path != "" {
			helper = filepath.Join(filepath.Dir(path), status.HelperName)
		}
		if resolved, ok := canonicalExecutable(helper); ok && resolved != path {
			status.HelperAvailable = true
			status.HelperPath = resolved
			status.HelperName = filepath.Base(resolved)
			status.HelperIntegrity = "not_verified_by_cli"
		}
	} else {
		status.HelperIntegrity = "not_applicable"
	}
	return status
}

// Resolve 返回密钥提供器的可执行文件路径及来源。
func Resolve(explicit string) (string, string) {
	return resolve(explicit)
}

func resolve(explicit string) (string, string) {
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
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", false
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	return resolved, true
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
