//go:build windows

package state

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ValidatePrivateDirectorySecurity 验证 DACL 已禁止继承，且只向当前用户、
// LocalSystem 和本机 Administrators 授予 allow 权限。提升权限创建的目录可能由
// Administrators 持有，因此 owner 也允许来自这三个受信任主体。
func ValidatePrivateDirectorySecurity(path string) error {
	if err := validatePrivatePath(path, true); err != nil {
		return err
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil || descriptor == nil || !descriptor.IsValid() {
		return errors.New("v-local-cli 私有目录安全描述符不可用")
	}
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	localSystem, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil ||
		(!owner.Equals(currentUser.User.Sid) && !owner.Equals(localSystem) && !owner.Equals(administrators)) {
		return errors.New("v-local-cli 私有目录 owner 不可信")
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("v-local-cli 私有目录 DACL 仍在继承")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("v-local-cli 私有目录 DACL 不可用")
	}
	currentUserAllowed := false
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil || ace == nil {
			return errors.New("v-local-cli 私有目录 DACL 无法检查")
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if sid.Equals(currentUser.User.Sid) {
				currentUserAllowed = true
				continue
			}
			if sid.Equals(localSystem) || sid.Equals(administrators) {
				continue
			}
			return errors.New("v-local-cli 私有目录向其他主体授予了访问权限")
		default:
			return errors.New("v-local-cli 私有目录包含不支持的 allow 规则")
		}
	}
	if !currentUserAllowed {
		return errors.New("v-local-cli 私有目录未向当前用户授予权限")
	}
	return nil
}

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
