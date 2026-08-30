//go:build !darwin || !cgo

package shadowkeychain

import "errors"

func newPlatformStore() (itemStore, error) {
	return nil, errors.New("Shadow Keychain helper requires a cgo-enabled macOS build")
}
