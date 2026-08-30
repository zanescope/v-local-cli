//go:build !darwin

package shadowsource

import (
	"context"
	"errors"
)

func systemVerifyStrict(context.Context, string) error {
	return errors.New("source strict verification is available only on macOS")
}

func systemCodeIdentity(context.Context, string) (CodeIdentity, error) {
	return CodeIdentity{}, errors.New("source code identity is available only on macOS")
}

func systemPlistString(context.Context, string, string) (string, error) {
	return "", errors.New("source plist inspection is available only on macOS")
}
