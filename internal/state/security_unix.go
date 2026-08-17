//go:build !windows

package state

import (
	"errors"
	"os"
)

func validatePrivatePath(path string, allowDirectory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("v-local-cli 私有路径不能是符号链接")
	}
	if allowDirectory && !info.IsDir() {
		return errors.New("v-local-cli 私有路径不是目录")
	}
	if !allowDirectory && !info.Mode().IsRegular() {
		return errors.New("v-local-cli 状态文件不是普通文件")
	}
	return nil
}

func securePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if err := validatePrivateHierarchy(path); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func validatePrivateHierarchy(path string) error {
	paths, err := privateHierarchy(path)
	if err != nil {
		return err
	}
	for _, current := range paths {
		if err := validatePrivatePath(current, true); err != nil {
			return err
		}
	}
	return nil
}
