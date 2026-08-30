//go:build !darwin

package shadowaccount

import "errors"

func ResolveCurrent() (Record, error) {
	return Record{}, errors.New("Shadow account resolution is available only on macOS")
}

func Revalidate(Record) error {
	return errors.New("Shadow account revalidation is available only on macOS")
}
