//go:build darwin && !cgo

package shadowkeychain

import "errors"

func staticHelperCodeHash(string) (string, error) {
	return "", errors.New("Shadow Keychain helper code identity requires cgo")
}

func runningHelperCodeHash(int) (string, error) {
	return "", errors.New("Shadow Keychain helper code identity requires cgo")
}
