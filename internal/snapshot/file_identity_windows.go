//go:build windows

package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/text/unicode/norm"
)

func sourceFileIdentity(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return sourceOpenFileIdentity(file)
}

func sourceOpenFileIdentity(file *os.File) (string, error) {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("windows:%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)))
	return hex.EncodeToString(digest[:]), nil
}

func platformPathKey(path string) string {
	return strings.ToLower(norm.NFC.String(filepath.ToSlash(filepath.Clean(path))))
}
