//go:build windows

package snapshot

import (
	"errors"
	"io/fs"
	"syscall"
)

// Windows 没有 POSIX 的 rename 语义：杀毒软件、搜索索引器或资源管理器可能在刚写完
// 的目录上短暂持有句柄，使 os.Rename 返回 Access denied、Sharing violation 或
// Lock violation。这类争用是瞬时的，短暂退避后重试即可通过。
//
// 只有 ERROR_ACCESS_DENIED 会被标准库映射到 fs.ErrPermission，另外两个必须按错误码判定。
func transientRenameError(err error) bool {
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		const sharingViolation, lockViolation = 32, 33
		return errno == sharingViolation || errno == lockViolation
	}
	return false
}
