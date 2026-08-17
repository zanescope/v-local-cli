package provider

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCurrentUsesExplicitProvider(t *testing.T) {
	name := "v-local-key-provider"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	providerPath := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(providerPath, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(providerPath); err != nil {
		t.Fatalf("test provider path cannot be resolved: path=%q err=%v", providerPath, err)
	} else if info, err := os.Lstat(resolved); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("resolved test provider is not a regular file: path=%q info=%v err=%v", resolved, info, err)
	}
	status := Current(providerPath)
	if !status.Available || status.Source != "explicit" {
		t.Fatalf("显式 Provider 未被识别：%+v", status)
	}
}

func TestCurrentReportsMissingProvider(t *testing.T) {
	t.Setenv(EnvironmentVariable, "")
	status := Current(filepath.Join(t.TempDir(), "missing-provider"))
	if status.Available {
		t.Fatalf("不存在的 Provider 不应标记为可用：%+v", status)
	}
}

func TestCurrentReportsDarwinCompanionHelper(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin companion status")
	}
	directory := t.TempDir()
	providerPath := filepath.Join(directory, "v-local-key-provider")
	helperPath := filepath.Join(directory, "v-local-key-provider-helper")
	for _, path := range []string{providerPath, helperPath} {
		if err := os.WriteFile(path, []byte("test"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	status := Current(providerPath)
	if !status.Available || !status.HelperRequired || !status.HelperAvailable || status.HelperIntegrity == "missing" {
		t.Fatalf("Darwin companion helper not reported: %+v", status)
	}
}
