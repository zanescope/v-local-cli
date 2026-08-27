//go:build windows

package app

import (
	"crypto/sha256"
	"fmt"
	"os"
	"syscall"
)

func testCatalogFileIdentity(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("windows:%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)))
	return fmt.Sprintf("%x", digest[:]), nil
}
