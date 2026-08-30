//go:build darwin

package shadowpublish

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// FileLocker places the cross-process transaction lock on the already-owned
// state directory inode. It therefore creates no persistent lock leaf.
type FileLocker struct {
	root      string
	accountID string
	uid       uint32
	device    uint64
	inode     uint64
}

func NewFileLocker(root, accountID string) (*FileLocker, error) {
	if (!lowerHex(accountID, 8) && !lowerHex(accountID, 16)) || !filepath.IsAbs(root) {
		return nil, errors.New("Shadow publication lock binding is invalid")
	}
	root = filepath.Clean(root)
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return nil, errors.New("Shadow publication lock root contains a symlink")
	}
	locker := &FileLocker{root: root, accountID: accountID, uid: uint32(os.Geteuid())}
	fd, err := locker.open()
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return nil, errors.New("Shadow publication lock root identity could not be bound")
	}
	locker.device = uint64(stat.Dev)
	locker.inode = uint64(stat.Ino)
	_ = unix.Close(fd)
	return locker, nil
}

func (value *FileLocker) open() (int, error) {
	fd, err := unix.Open(value.root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, errors.New("Shadow publication lock root could not be opened")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR ||
		stat.Mode&0o077 != 0 || stat.Uid != value.uid || value.inode != 0 &&
		(uint64(stat.Dev) != value.device || uint64(stat.Ino) != value.inode) {
		_ = unix.Close(fd)
		return -1, errors.New("Shadow publication lock root identity is invalid")
	}
	return fd, nil
}

func (value *FileLocker) Acquire(ctx context.Context, accountID string) (func() error, error) {
	if value == nil || accountID != value.accountID || ctx == nil || ctx.Err() != nil {
		return nil, errors.New("Shadow publication lock request is invalid")
	}
	fd, err := value.open()
	if err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = unix.Close(fd)
			return nil, errors.New("Shadow publication lock failed")
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = unix.Close(fd)
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			releaseErr = errors.Join(unix.Flock(fd, unix.LOCK_UN), unix.Close(fd))
		})
		return releaseErr
	}, nil
}
