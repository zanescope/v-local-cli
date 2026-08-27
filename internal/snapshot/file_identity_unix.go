//go:build !windows

package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func platformPathKey(path string) string {
	value := filepath.ToSlash(filepath.Clean(path))
	if runtime.GOOS == "darwin" {
		value = normalizeDarwinSnapshotSystemAlias(value)
	}
	return norm.NFC.String(value)
}

func normalizeDarwinSnapshotSystemAlias(value string) string {
	for _, prefix := range []string{"/private/etc", "/private/tmp", "/private/var"} {
		if value == prefix || strings.HasPrefix(value, prefix+"/") {
			return strings.TrimPrefix(value, "/private")
		}
	}
	return value
}
