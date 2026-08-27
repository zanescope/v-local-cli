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

func TestCanonicalExecutableRejectsLinkedAncestor(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	linkedDirectory := filepath.Join(root, "linked")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "v-local-key-provider"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	realProvider := filepath.Join(realDirectory, name)
	if err := os.WriteFile(realProvider, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, ok := canonicalExecutable(realProvider); !ok {
		t.Fatal("direct executable was rejected")
	}
	if err := os.Symlink(realDirectory, linkedDirectory); err != nil {
		t.Skipf("directory symlink is unavailable on this host: %v", err)
	}
	if resolved, ok := canonicalExecutable(filepath.Join(linkedDirectory, name)); ok {
		t.Fatalf("executable beneath a linked ancestor was accepted as %q", resolved)
	}
}

func TestSameCanonicalPathTextFoldsDarwinSystemAlias(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("系统别名折叠只适用于 macOS")
	}
	if !sameCanonicalPathText("/private/var/folders/test/provider", "/var/folders/test/provider") {
		t.Fatal("同一固定安装路径的两种系统别名写法被判为不同")
	}
	if sameCanonicalPathText("/private/variable/provider", "/variable/provider") {
		t.Fatal("非系统别名前缀被当成别名折叠")
	}
}

func TestFixedProviderInstallPathIsArchitectureScoped(t *testing.T) {
	windows := fixedProviderInstallPathFor("windows", "arm64", filepath.Join("C:", "Users", "test", "AppData", "Local"))
	if filepath.Base(windows) != "v-local-key-provider.exe" || filepath.Base(filepath.Dir(windows)) != "windows-arm64" {
		t.Fatalf("Windows fixed Provider path is not architecture scoped: %q", windows)
	}
	darwin := fixedProviderInstallPathFor("darwin", "amd64", filepath.Join(string(filepath.Separator), "Users", "test", "Library", "Application Support"))
	if filepath.Base(darwin) != "v-local-key-provider" || filepath.Base(filepath.Dir(darwin)) != "darwin-amd64" {
		t.Fatalf("Darwin fixed Provider path is not architecture scoped: %q", darwin)
	}
	if path := fixedProviderInstallPathFor("linux", "amd64", t.TempDir()); path != "" {
		t.Fatalf("unsupported platform received a fixed Provider path: %q", path)
	}
}

func TestReleaseBuildRejectsExplicitAndEnvironmentProviderOverrides(t *testing.T) {
	previous := buildMode
	buildMode = "release"
	t.Cleanup(func() { buildMode = previous })
	t.Setenv(EnvironmentVariable, "")
	if path, source := resolveCandidate(filepath.Join(t.TempDir(), "provider")); path != "" || source != "override_rejected" {
		t.Fatalf("release explicit override was not rejected: path=%q source=%q", path, source)
	}
	t.Setenv(EnvironmentVariable, filepath.Join(t.TempDir(), "provider"))
	if path, source := resolveCandidate(""); path != "" || source != "override_rejected" {
		t.Fatalf("release environment override was not rejected: path=%q source=%q", path, source)
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
