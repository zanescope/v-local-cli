//go:build !windows

package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/text/unicode/norm"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
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
	value := localplatform.CanonicalSystemPath(filepath.ToSlash(filepath.Clean(path)))
	return norm.NFC.String(value)
}
