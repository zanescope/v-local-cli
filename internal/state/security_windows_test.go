//go:build windows

package state

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestValidatePrivateDirectorySecurityRejectsWorldAccess(t *testing.T) {
	path := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", path)
	if err := securePrivateDirectory(path); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivateDirectorySecurity(path); err != nil {
		t.Fatalf("当前用户专属目录被拒绝：%v", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivateDirectorySecurity(path); err == nil {
		t.Fatal("向 Everyone 开放的私有目录未被拒绝")
	}
}
