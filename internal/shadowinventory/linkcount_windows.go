//go:build windows

package shadowinventory

import (
	"errors"
	"os"
)

func regularFileLinkCount(os.FileInfo) (uint64, error) {
	return 0, errors.New("macOS App inventory identity is unavailable on Windows")
}
