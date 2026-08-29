//go:build !windows

package app

import (
	"errors"
	"os"
	"syscall"
)

func secureRecoveryTemporaryFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("恢复临时文件不是普通文件")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	info, err = os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 {
		return errors.New("恢复临时文件权限不安全")
	}
	return nil
}
