//go:build darwin

package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinTrustedDirectoryTreeRejectsWritableAncestor(t *testing.T) {
	root, err := os.MkdirTemp(".", ".darwin-cli-trust-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "v-local", "key-provider", "darwin-test")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := darwinTrustedDirectoryTree(nested); err != nil {
		t.Fatalf("private direct directory tree was rejected: %v", err)
	}
	if err := os.Chmod(filepath.Dir(nested), 0o770); err != nil {
		t.Fatal(err)
	}
	if err := darwinTrustedDirectoryTree(nested); err == nil {
		t.Fatal("group-writable component ancestor was accepted")
	}
}

// release 构建靠这条路径确认 acquisition daemon 的 PID 确实在运行它自称的镜像。
// 它一旦返回错误，loadAcquisitionEndpoint 就会失败，整条 macOS daemon 获取路径都
// 走不通，因此必须对当前进程做一次真实校验，而不只是测试错误分支。
func TestDarwinProcessImagePathMatchesCurrentExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := darwinProcessImagePath(os.Getpid())
	if err != nil {
		t.Fatalf("当前进程的镜像路径不可用：%v", err)
	}
	if !sameFilePath(image, executable) {
		t.Fatalf("进程镜像路径与当前可执行文件不一致：image=%q executable=%q", image, executable)
	}
}

func TestDarwinProcessImagePathRejectsInvalidPID(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if image, err := darwinProcessImagePath(pid); err == nil {
			t.Fatalf("无效 PID %d 返回了镜像路径 %q", pid, image)
		}
	}
}

// 只校验 CLI 与 Provider 的 Team ID 相等是不够的：任何持有 Developer ID 证书的人都能
// 签出一对自洽的二进制。发行身份必须在编译期绑定，未注入时失败关闭。
func TestExpectedDarwinTeamIDRequiresEmbeddedReleaseIdentity(t *testing.T) {
	previous := releaseTeamID
	t.Cleanup(func() { releaseTeamID = previous })

	releaseTeamID = "ABCDE12345"
	value, err := expectedDarwinTeamID()
	if err != nil || value != "ABCDE12345" {
		t.Fatalf("合法 Team ID 未被接受：value=%q err=%v", value, err)
	}
	releaseTeamID = " abcde12345 "
	if value, err = expectedDarwinTeamID(); err != nil || value != "ABCDE12345" {
		t.Fatalf("Team ID 未按大写归一化：value=%q err=%v", value, err)
	}
	for _, invalid := range []string{"", "   ", "ABCDE1234", "ABCDE123456", "ABCDE-1234", "ABCDE 1234"} {
		releaseTeamID = invalid
		if _, err := expectedDarwinTeamID(); err == nil {
			t.Fatalf("未注入或格式错误的 Team ID %q 被接受", invalid)
		}
	}
	releaseTeamID = "ABCDE12345"
	if sameDarwinTeamID("", "ABCDE12345") || sameDarwinTeamID("ABCDE12345", "") {
		t.Fatal("空 Team ID 被判为匹配")
	}
	if !sameDarwinTeamID("abcde12345", "ABCDE12345") {
		t.Fatal("大小写不同的同一 Team ID 被判为不匹配")
	}
}
