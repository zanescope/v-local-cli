//go:build !darwin

package shadowapproval

import (
	"context"
	"errors"

	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
)

type FileStore struct{}

func NewFileStore(string) (*FileStore, error) {
	return nil, errors.New("Shadow approval file store is only available on macOS")
}

func (*FileStore) Load(context.Context) (contract.Challenge, bool, error) {
	return contract.Challenge{}, false, errors.New("Shadow approval file store is unavailable")
}

func (*FileStore) Save(context.Context, contract.Challenge) error {
	return errors.New("Shadow approval file store is unavailable")
}

func (*FileStore) Remove(context.Context, string) error {
	return errors.New("Shadow approval file store is unavailable")
}
