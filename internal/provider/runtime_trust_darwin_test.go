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
