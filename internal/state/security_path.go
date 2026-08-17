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
	volume := filepath.VolumeName(target)
	filesystemRoot := string(filepath.Separator)
	if volume != "" {
		filesystemRoot = volume + string(filepath.Separator)
	}
	fromFilesystemRoot, err := filepath.Rel(filesystemRoot, target)
	if err != nil || fromFilesystemRoot == ".." || strings.HasPrefix(fromFilesystemRoot, ".."+string(filepath.Separator)) || filepath.IsAbs(fromFilesystemRoot) {
		return nil, errors.New("v-local-cli 私有路径根无效")
	}
	paths := []string{filesystemRoot}
	if fromFilesystemRoot == "." {
		return paths, nil
	}
	current := filesystemRoot
	for _, component := range strings.Split(fromFilesystemRoot, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		paths = append(paths, current)
	}
	return paths, nil
}
