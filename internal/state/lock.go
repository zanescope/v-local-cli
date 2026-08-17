package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrAccountBusy = errors.New("账号快照事务正在运行")

type AccountLock struct {
	file *os.File
}

func validAccountID(value string) bool {
	if len(value) != 16 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

// AcquireAccountLock 使用操作系统文件锁串行化同一账号的 setup 与 refresh。
func AcquireAccountLock(accountID string) (*AccountLock, error) {
	if !validAccountID(accountID) {
		return nil, errors.New("账号标识无效")
	}
	root, err := Home()
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(root, "locks")
	if err := securePrivateDirectory(directory); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(directory, accountID+".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		if isLockBusy(err) {
			return nil, ErrAccountBusy
		}
		return nil, fmt.Errorf("无法取得账号快照锁：%w", err)
	}
	return &AccountLock{file: file}, nil
}

func (lock *AccountLock) Release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	return errors.Join(unlockFile(file), file.Close())
}
