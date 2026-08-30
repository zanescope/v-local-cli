//go:build !darwin

package shadowpublish

import (
	"context"
	"errors"
)

type FileLocker struct{}

func NewFileLocker(string, string) (*FileLocker, error) {
	return nil, errors.New("Shadow publication file lock is only available on macOS")
}

func (*FileLocker) Acquire(context.Context, string) (func() error, error) {
	return nil, errors.New("Shadow publication file lock is unavailable")
}
