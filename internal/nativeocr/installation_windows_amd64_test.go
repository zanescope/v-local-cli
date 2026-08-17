//go:build windows && amd64

package nativeocr

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnownProgramFilesRootsIgnoreEnvironmentOverrides(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "fake-program-files")
	t.Setenv("ProgramFiles", fake)
	t.Setenv("ProgramW6432", fake)
	t.Setenv("ProgramFiles(x86)", fake)
	roots := knownProgramFilesRoots()
	if len(roots) == 0 {
		t.Fatal("Windows 已知文件夹没有返回 Program Files 根")
	}
	for _, root := range roots {
		if strings.HasPrefix(strings.ToLower(root), strings.ToLower(fake)) {
			t.Fatalf("原生 OCR 安装发现信任了可改写的环境变量：%s", root)
		}
		if !filepath.IsAbs(root) {
			t.Fatalf("Program Files 已知文件夹不是绝对路径：%s", root)
		}
	}
}

func TestExtractInstalledPackageRejectsCorruptCRC(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	header := &zip.FileHeader{Name: "wxocr.dll", Method: zip.Store}
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("unique-wxocr-payload")
	if _, err := entry.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	payload := archive.Bytes()
	index := bytes.Index(payload, content)
	if index < 0 {
		t.Fatal("测试 ZIP 中没有找到存储的 DLL 内容")
	}
	payload[index] ^= 0xff
	path := filepath.Join(t.TempDir(), "WeChatOcr.bin")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if directory, cleanup, err := extractInstalledPackage(installation{bin: path}); err == nil {
		if cleanup != nil {
			_ = cleanup()
		}
		t.Fatalf("损坏 CRC 的微信 OCR 包被接受：%s", directory)
	}
}

func TestSafeArchiveLeafRejectsWindowsAliases(t *testing.T) {
	for _, name := range []string{"../wxocr.dll", `dir\wxocr.dll`, "wxocr.dll:payload", "wxocr.dll.", "wxocr.dll ", "CON", "nul.txt", "COM1.dll", "COM1.foo.bar", "LPT9"} {
		if safeArchiveLeaf(name) {
			t.Errorf("危险 Windows ZIP 条目被接受：%q", name)
		}
	}
	for _, name := range []string{"wxocr.dll", "helper-1.dll", "model.dat"} {
		if !safeArchiveLeaf(name) {
			t.Errorf("普通 OCR ZIP 文件名被拒绝：%q", name)
		}
	}
}
