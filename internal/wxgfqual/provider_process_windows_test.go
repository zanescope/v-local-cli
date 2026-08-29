//go:build windows

package wxgfqual

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsProviderJobConfiguresHardLimits(t *testing.T) {
	job, err := createProviderJob()
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
		t.Fatal(err)
	}
	wantedFlags := uint32(windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS |
		windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY |
		windows.JOB_OBJECT_LIMIT_JOB_MEMORY |
		windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION)
	if limits.BasicLimitInformation.LimitFlags&wantedFlags != wantedFlags ||
		limits.BasicLimitInformation.ActiveProcessLimit != providerActiveProcessLimit ||
		limits.ProcessMemoryLimit != providerProcessMemoryLimitBytes ||
		limits.JobMemoryLimit != providerJobMemoryLimitBytes {
		t.Fatalf("Windows provider Job Object 限制异常：%+v", limits)
	}
}

func TestWindowsProviderJobKillsDescendantOnClose(t *testing.T) {
	root := t.TempDir()
	controlMarker := filepath.Join(root, "control.txt")
	control := exec.Command(os.Args[0], "-test.run=^TestImageDecoderProviderHelperProcess$", "--", "escape_child", controlMarker)
	if err := control.Run(); err != nil {
		t.Fatalf("provider 子进程测试的正向对照无法运行：%v", err)
	}
	if info, err := os.Lstat(controlMarker); err != nil || !info.Mode().IsRegular() {
		t.Fatal("provider 子进程测试的正向对照没有写入标记")
	}

	marker := filepath.Join(root, "escaped.txt")
	previous := newImageDecoderProviderCommand
	newImageDecoderProviderCommand = func(ctx context.Context, _ string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestImageDecoderProviderHelperProcess$", "--", "spawn_child", marker)
	}
	t.Cleanup(func() { newImageDecoderProviderCommand = previous })
	result, err := RunProviderTrial(context.Background(), singlePictureFixture(), ProviderOptions{
		Executable: os.Args[0], TemporaryRoot: t.TempDir(), Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Isolation.CreateProcessTreeContained || !result.Isolation.JobMemberMemoryLimited {
		t.Fatalf("Windows provider 未声明已建立 Job Object：%+v", result.Isolation)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatal("provider 子进程逃逸了 Job Object 清理边界")
	}
}
