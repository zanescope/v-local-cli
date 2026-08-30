//go:build !windows

package shadowinventory

import (
	"errors"
	"os"
	"syscall"
)

func regularFileLinkCount(info os.FileInfo) (uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("file identity is unavailable")
	}
	return uint64(stat.Nlink), nil
}
