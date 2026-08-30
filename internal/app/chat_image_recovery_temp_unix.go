//go:build !windows

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type chatImageRecoveryOutputDirectory struct {
	fd int
}

func openChatImageRecoveryOutputDirectory(path string) (*chatImageRecoveryOutputDirectory, string, error) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return nil, "", os.ErrInvalid
	}
	current, err := unix.Open(clean, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	var info unix.Stat_t
	if err := unix.Fstat(current, &info); err != nil {
		_ = unix.Close(current)
		return nil, "", err
	}
	identity := fmt.Sprintf("unix:%d:%d", info.Dev, info.Ino)
	return &chatImageRecoveryOutputDirectory{fd: current}, identity, nil
}

func (directory *chatImageRecoveryOutputDirectory) Close() error {
	if directory == nil || directory.fd < 0 {
		return nil
	}
	err := unix.Close(directory.fd)
	directory.fd = -1
	return err
}

func (directory *chatImageRecoveryOutputDirectory) CreateTemporary(name string) (*os.File, error) {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return nil, os.ErrInvalid
	}
	fd, err := unix.Openat(directory.fd, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, os.ErrInvalid
	}
	return file, nil
}

func (directory *chatImageRecoveryOutputDirectory) LinkTemporary(_ *os.File, temporaryName, targetName string) error {
	return unix.Linkat(directory.fd, temporaryName, directory.fd, targetName, 0)
}

func (directory *chatImageRecoveryOutputDirectory) RemoveTemporary(_ *os.File, name string) error {
	err := unix.Unlinkat(directory.fd, name, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}

func chatImageRecoveryTemporaryExists(err error) bool {
	return errors.Is(err, unix.EEXIST)
}
