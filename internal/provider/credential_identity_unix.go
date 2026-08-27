//go:build !windows

package provider

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
	value := filepath.ToSlash(filepath.Clean(path))
	if runtime.GOOS == "darwin" {
		value = normalizeDarwinCredentialSystemAlias(value)
	}
	return norm.NFC.String(value)
}

func normalizeDarwinCredentialSystemAlias(value string) string {
	for _, prefix := range []string{"/private/etc", "/private/tmp", "/private/var"} {
		if value == prefix || strings.HasPrefix(value, prefix+"/") {
			return strings.TrimPrefix(value, "/private")
		}
	}
	return value
}
