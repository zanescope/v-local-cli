//go:build !windows

package shadowbuildset

import (
	"os"
	"syscall"
)

func singleLinkArtifact(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Dev != 0 && stat.Ino != 0 && stat.Nlink == 1
}
