//go:build !windows

package snapshot

// POSIX 的 rename 不会因为别的进程持有句柄而失败，因此没有需要重试的瞬时争用。
func transientRenameError(error) bool { return false }
