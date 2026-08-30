//go:build !windows

package shadowsource

import (
	"errors"
	"os"
	"syscall"
)

func sourceIdentity(path string) (Identity, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Identity{}, errors.New("source identity is unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Identity{}, errors.New("source identity is unavailable")
	}
	return Identity{
		Device: uint64(stat.Dev), Inode: uint64(stat.Ino), UID: stat.Uid,
		Mode: uint32(info.Mode().Perm()), LinkCount: uint64(stat.Nlink),
	}, nil
}
