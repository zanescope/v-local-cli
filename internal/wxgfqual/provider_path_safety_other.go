//go:build !windows

package wxgfqual

import "io/fs"

func providerPathEntryIsLinkOrReparse(_ string, mode fs.FileMode) (bool, error) {
	return mode&fs.ModeSymlink != 0, nil
}
