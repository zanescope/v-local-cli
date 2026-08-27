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
	// 安全边界从配置的私有根目录开始。该根目录的祖先路径由操作系统或调用方管理；例如
	// macOS 会把 /var 暴露为指向 /private/var 的符号链接。如果从文件系统根目录开始一律
	// 拒绝符号链接，就会误拒绝 os.MkdirTemp 创建的安全私有目录。这里仍会用 Lstat 检查
	// 私有根目录本身及其下方的每一级路径，攻击者无法重定向 v-local-cli 控制的路径。
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
