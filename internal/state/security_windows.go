//go:build windows

package state

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func validatePrivatePath(path string, allowDirectory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if allowDirectory && !info.IsDir() {
		return errors.New("v-local-cli 私有路径不是目录")
	}
	if !allowDirectory && !info.Mode().IsRegular() {
		return errors.New("v-local-cli 状态文件不是普通文件")
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("v-local-cli 私有路径不能是重解析点")
	}
	return nil
}

func securePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if err := validatePrivateHierarchy(path); err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)",
	)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}

func validatePrivateHierarchy(path string) error {
	paths, err := privateHierarchy(path)
	if err != nil {
		return err
	}
	for _, current := range paths {
		if err := validatePrivatePath(current, true); err != nil {
			return err
		}
	}
	return nil
}
