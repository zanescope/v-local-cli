//go:build !windows

package provider

import "io/fs"

func providerPathIsLinkOrReparse(_ string, mode fs.FileMode) (bool, error) {
	return mode&fs.ModeSymlink != 0, nil
}
