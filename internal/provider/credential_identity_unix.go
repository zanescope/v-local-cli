//go:build !windows

package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/text/unicode/norm"
)

func credentialFileIdentity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("platform_file_identity_unavailable")
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("unix:%d:%d", uint64(stat.Dev), uint64(stat.Ino))))
	return hex.EncodeToString(digest[:]), nil
}

func credentialPathKey(path string) string {
	return norm.NFC.String(filepath.ToSlash(filepath.Clean(path)))
}
