//go:build !windows

package snapshot

import "io/fs"

func snapshotPathIsLinkOrReparse(_ string, mode fs.FileMode) (bool, error) {
	return mode&fs.ModeSymlink != 0, nil
}
