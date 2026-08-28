//go:build windows

package snapshot

import (
	"io/fs"

	"golang.org/x/sys/windows"
)

func snapshotPathIsLinkOrReparse(path string, mode fs.FileMode) (bool, error) {
	if mode&fs.ModeSymlink != 0 {
		return true, nil
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return false, err
	}
	return attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}
