//go:build windows

package app

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestRecoveryTemporaryFileHasProtectedDACLAtCreation(t *testing.T) {
	targetPath, err := prepareOutputTarget(filepath.Join(t.TempDir(), "recovered.png"), false)
	if err != nil {
		t.Fatal(err)
	}
	target, err := openChatImageRecoveryOutputTarget(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()
	file, name, _, err := createChatImageRecoveryTemporaryFile(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = target.directory.RemoveTemporary(file, name)
		_ = file.Close()
	}()

	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(file.Fd()), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil || descriptor == nil || !descriptor.IsValid() {
		t.Fatalf("新建恢复临时文件的安全描述符不可用：%v", err)
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("新建恢复临时文件仍继承目录 DACL：control=%v err=%v", control, err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("新建恢复临时文件 DACL 不可用：%v", err)
	}
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	localSystem, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	currentUserAllowed, localSystemAllowed := false, false
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("新建恢复临时文件包含不支持的 ACE：index=%d err=%v", index, err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(currentUser.User.Sid):
			currentUserAllowed = true
		case sid.Equals(localSystem):
			localSystemAllowed = true
		default:
			t.Fatalf("新建恢复临时文件在首次可见时向非预期主体授权：%s", sid.String())
		}
	}
	if !currentUserAllowed || !localSystemAllowed {
		t.Fatalf("新建恢复临时文件缺少受信任主体：current_user=%v system=%v", currentUserAllowed, localSystemAllowed)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("检查临时 ACL 时意外发布了最终输出：%v", err)
	}
}
