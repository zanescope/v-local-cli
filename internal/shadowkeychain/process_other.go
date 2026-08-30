//go:build !darwin

package shadowkeychain

import (
	"context"
	"errors"
	"io"
	"os"
)

type helperExecutableBinding struct {
	CodeHash string
}

func inspectHelperExecutable(string, string) (helperExecutableBinding, error) {
	return helperExecutableBinding{}, errors.New("Shadow Keychain helper executable is only available on macOS")
}

func openHelperExecutable(string, helperExecutableBinding) (*os.File, error) {
	return nil, errors.New("Shadow Keychain helper executable is only available on macOS")
}

func runHelperProcess(context.Context, string, string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("Shadow Keychain helper process is only available on macOS")
}
