package app

import (
	"errors"
	"fmt"
	"os"
)

func removeTemporaryFiles(paths ...string) error {
	var failures []error
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			failures = append(failures, fmt.Errorf("remove temporary file: %w", err))
		}
	}
	return errors.Join(failures...)
}
