//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type chatImageRecoveryOutputDirectory struct {
	handle windows.Handle
}

type chatImageRecoveryFileLinkInformation struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [1]uint16
}

type chatImageRecoveryFileDispositionInformationEx struct {
	Flags uint32
}

type chatImageRecoveryFileDispositionInformation struct {
	DeleteFile byte
}

func chatImageRecoveryNTPath(path string) (string, error) {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	if !filepath.IsAbs(clean) || volume == "" || strings.HasPrefix(volume, `\\`) {
		return "", errors.New("恢复图片输出只允许本机绝对路径")
	}
	return `\??\` + clean, nil
}

func openChatImageRecoveryOutputDirectory(path string) (*chatImageRecoveryOutputDirectory, string, error) {
	ntPath, err := chatImageRecoveryNTPath(path)
	if err != nil {
		return nil, "", err
	}
	objectName, err := windows.NewNTUnicodeString(ntPath)
	if err != nil {
		return nil, "", err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		ObjectName: objectName,
		Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	attributes.Length = uint32(unsafe.Sizeof(*attributes))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	var allocationSize int64
	err = windows.NtCreateFile(
		&handle, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE,
		attributes, &status, &allocationSize, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		0, 0,
	)
	if err != nil {
		return nil, "", err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, "", err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return nil, "", errors.New("恢复图片输出父目录不是无重解析点的普通目录")
	}
	identity := fmt.Sprintf("windows:%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
	return &chatImageRecoveryOutputDirectory{handle: handle}, identity, nil
}

func (directory *chatImageRecoveryOutputDirectory) Close() error {
	if directory == nil || directory.handle == 0 || directory.handle == windows.InvalidHandle {
		return nil
	}
	err := windows.CloseHandle(directory.handle)
	directory.handle = 0
	return err
}

func chatImageRecoverySecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString(
		"D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)",
	)
}

func (directory *chatImageRecoveryOutputDirectory) CreateTemporary(name string) (*os.File, error) {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return nil, os.ErrInvalid
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	descriptor, err := chatImageRecoverySecurityDescriptor()
	if err != nil {
		return nil, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		RootDirectory: directory.handle, ObjectName: objectName,
		Attributes:         windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
		SecurityDescriptor: descriptor,
	}
	attributes.Length = uint32(unsafe.Sizeof(*attributes))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	var allocationSize int64
	err = windows.NtCreateFile(
		&handle, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE,
		attributes, &status, &allocationSize, windows.FILE_ATTRIBUTE_TEMPORARY,
		0, windows.FILE_CREATE,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		0, 0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), name)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, os.ErrInvalid
	}
	return file, nil
}

func (directory *chatImageRecoveryOutputDirectory) LinkTemporary(file *os.File, _ string, targetName string) error {
	encoded, err := windows.UTF16FromString(targetName)
	if err != nil || len(encoded) <= 1 {
		return os.ErrInvalid
	}
	nameLength := (len(encoded) - 1) * 2
	var layout chatImageRecoveryFileLinkInformation
	bufferSize := int(unsafe.Offsetof(layout.FileName)) + nameLength
	buffer := make([]byte, bufferSize)
	information := (*chatImageRecoveryFileLinkInformation)(unsafe.Pointer(&buffer[0]))
	information.RootDirectory = directory.handle
	information.FileNameLength = uint32(nameLength)
	copy(unsafe.Slice(&information.FileName[0], len(encoded)-1), encoded[:len(encoded)-1])
	var status windows.IO_STATUS_BLOCK
	return windows.NtSetInformationFile(
		windows.Handle(file.Fd()), &status, &buffer[0], uint32(len(buffer)), windows.FileLinkInformation,
	)
}

func (directory *chatImageRecoveryOutputDirectory) RemoveTemporary(file *os.File, _ string) error {
	information := chatImageRecoveryFileDispositionInformationEx{
		Flags: windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS | windows.FILE_DISPOSITION_IGNORE_READONLY_ATTRIBUTE,
	}
	var status windows.IO_STATUS_BLOCK
	err := windows.NtSetInformationFile(
		windows.Handle(file.Fd()), &status, (*byte)(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)), windows.FileDispositionInformationEx,
	)
	if err == nil {
		return nil
	}
	legacy := chatImageRecoveryFileDispositionInformation{DeleteFile: 1}
	return windows.NtSetInformationFile(
		windows.Handle(file.Fd()), &status, (*byte)(unsafe.Pointer(&legacy)),
		uint32(unsafe.Sizeof(legacy)), windows.FileDispositionInformation,
	)
}

func chatImageRecoveryTemporaryExists(err error) bool {
	return errors.Is(err, windows.STATUS_OBJECT_NAME_COLLISION) || errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS)
}
