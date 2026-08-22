package state

import (
	"errors"
	"path/filepath"
	"strings"
)

func privateHierarchy(path string) ([]string, error) {
	root, err := Home()
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return nil, errors.New("v-local-cli 私有路径越界")
	}
	// The security boundary starts at the configured private root. Ancestors of
	// that root belong to the operating system or the caller; macOS, for
	// example, exposes /var as a symlink to /private/var. Rejecting symlinks all
	// the way from the filesystem root therefore rejects otherwise safe private
	// directories created by os.MkdirTemp. The root itself and every component
	// below it are still checked with Lstat, so an attacker cannot redirect any
	// path controlled by v-local-cli.
	paths := []string{root}
	if relative == "." {
		return paths, nil
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		paths = append(paths, current)
	}
	return paths, nil
}
