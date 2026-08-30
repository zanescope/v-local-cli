//go:build darwin

package shadowapproval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
	"golang.org/x/sys/unix"
)

const (
	challengeFileName     = "shadow-approval.json"
	challengeNextFileName = "shadow-approval.json.next"
	maxChallengeBytes     = 16 * 1024
)

// FileStore owns one exact approval leaf in an owner-only directory. Each
// operation reopens the directory with O_NOFOLLOW and then uses *at syscalls so
// a path rename after qualification cannot redirect the operation elsewhere.
type FileStore struct {
	root   string
	uid    uint32
	device uint64
	inode  uint64
}

func openChallengeRoot(root string, uid uint32, device, inode uint64, bound bool) (int, unix.Stat_t, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, unix.Stat_t{}, errors.New("Shadow approval root could not be opened")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Uid != uid || stat.Mode&unix.S_IFMT != unix.S_IFDIR ||
		stat.Mode&0o077 != 0 || bound && (uint64(stat.Dev) != device || uint64(stat.Ino) != inode) {
		_ = unix.Close(fd)
		return -1, unix.Stat_t{}, errors.New("Shadow approval root identity is invalid")
	}
	return fd, stat, nil
}

func NewFileStore(root string) (*FileStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("Shadow approval root must be absolute")
	}
	root = filepath.Clean(root)
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return nil, errors.New("Shadow approval root contains a symlink")
	}
	uid := uint32(os.Geteuid())
	fd, stat, err := openChallengeRoot(root, uid, 0, 0, false)
	if err != nil {
		return nil, err
	}
	_ = unix.Close(fd)
	return &FileStore{root: root, uid: uid, device: uint64(stat.Dev), inode: uint64(stat.Ino)}, nil
}

func challengeStat(rootFD int, leaf string, allowEmpty bool) (unix.Stat_t, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(rootFD, leaf, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return stat, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 || stat.Uid != uint32(os.Geteuid()) ||
		stat.Nlink != 1 || (!allowEmpty && stat.Size <= 0) || stat.Size > maxChallengeBytes {
		return stat, errors.New("Shadow approval file identity is invalid")
	}
	return stat, nil
}

func removeChallengeNext(rootFD int) error {
	if _, err := challengeStat(rootFD, challengeNextFileName, true); errors.Is(err, unix.ENOENT) {
		return nil
	} else if err != nil {
		return err
	}
	if err := unix.Unlinkat(rootFD, challengeNextFileName, 0); err != nil {
		return err
	}
	return unix.Fsync(rootFD)
}

func loadChallenge(rootFD int) (contract.Challenge, bool, error) {
	stat, err := challengeStat(rootFD, challengeFileName, false)
	if errors.Is(err, unix.ENOENT) {
		return contract.Challenge{}, false, nil
	}
	if err != nil {
		return contract.Challenge{}, false, err
	}
	fd, err := unix.Openat(rootFD, challengeFileName, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return contract.Challenge{}, false, err
	}
	file := os.NewFile(uintptr(fd), challengeFileName)
	payload, readErr := io.ReadAll(io.LimitReader(file, maxChallengeBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(payload) == 0 || int64(len(payload)) != stat.Size || len(payload) > maxChallengeBytes {
		return contract.Challenge{}, false, errors.New("Shadow approval file is unreadable")
	}
	var challenge contract.Challenge
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&challenge); err != nil || decoder.Decode(&struct{}{}) != io.EOF || challenge.Validate() != nil {
		return contract.Challenge{}, false, errors.New("Shadow approval file contract is invalid")
	}
	return challenge, true, nil
}

func (value *FileStore) withRoot(ctx context.Context, action func(int) error) (resultErr error) {
	if value == nil || ctx == nil {
		return errors.New("Shadow approval operation was cancelled or unconfigured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, _, err := openChallengeRoot(value.root, value.uid, value.device, value.inode, true)
	if err != nil {
		return err
	}
	locked := false
	defer func() {
		var unlockErr error
		if locked {
			unlockErr = unix.Flock(fd, unix.LOCK_UN)
		}
		closeErr := unix.Close(fd)
		if resultErr == nil {
			resultErr = errors.Join(unlockErr, closeErr)
		}
	}()
	for {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			locked = true
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return errors.New("Shadow approval root could not be locked")
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return action(fd)
}

func (value *FileStore) Load(ctx context.Context) (contract.Challenge, bool, error) {
	var challenge contract.Challenge
	var found bool
	err := value.withRoot(ctx, func(rootFD int) error {
		if err := removeChallengeNext(rootFD); err != nil {
			return err
		}
		var err error
		challenge, found, err = loadChallenge(rootFD)
		return err
	})
	return challenge, found, err
}

func (value *FileStore) Save(ctx context.Context, challenge contract.Challenge) error {
	if challenge.Validate() != nil {
		return errors.New("Shadow approval write is invalid")
	}
	payload, err := json.Marshal(challenge)
	if err != nil || len(payload) >= maxChallengeBytes {
		return errors.New("Shadow approval encoding is invalid")
	}
	payload = append(payload, '\n')
	return value.withRoot(ctx, func(rootFD int) error {
		if err := removeChallengeNext(rootFD); err != nil {
			return err
		}
		if _, err := challengeStat(rootFD, challengeFileName, false); err == nil {
			return errors.New("Shadow approval already exists")
		} else if !errors.Is(err, unix.ENOENT) {
			return err
		}
		fd, err := unix.Openat(rootFD, challengeNextFileName,
			unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
		if err != nil {
			return err
		}
		file := os.NewFile(uintptr(fd), challengeNextFileName)
		published := false
		defer func() {
			_ = file.Close()
			if !published {
				_ = unix.Unlinkat(rootFD, challengeNextFileName, 0)
			}
		}()
		if _, err := file.Write(payload); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := unix.RenameatxNp(rootFD, challengeNextFileName, rootFD, challengeFileName, unix.RENAME_EXCL); err != nil {
			return err
		}
		published = true
		return unix.Fsync(rootFD)
	})
}

func (value *FileStore) Remove(ctx context.Context, challengeID string) error {
	return value.withRoot(ctx, func(rootFD int) error {
		if err := removeChallengeNext(rootFD); err != nil {
			return err
		}
		challenge, found, err := loadChallenge(rootFD)
		if err != nil || !found || challenge.ChallengeID != challengeID {
			return errors.New("exact Shadow approval challenge is absent or mismatched")
		}
		if err := unix.Unlinkat(rootFD, challengeFileName, 0); err != nil {
			return errors.New("exact Shadow approval challenge could not be removed")
		}
		return unix.Fsync(rootFD)
	})
}
