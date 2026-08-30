//go:build !darwin && !windows

package shadowpublish

import "errors"

type FileStateStore struct{}

func NewFileStateStore(string, string) (*FileStateStore, error) {
	return nil, errors.New("Shadow generation state is only available on macOS")
}
