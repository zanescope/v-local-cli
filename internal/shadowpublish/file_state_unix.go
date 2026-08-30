//go:build darwin

package shadowpublish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const (
	maxGenerationStateBytes = 16 * 1024
	readyStateLeaf          = "shadow-generation.ready.json"
	pendingStateLeaf        = "shadow-generation.pending.json"
)

// FileStateStore binds every operation to the same owner-only directory inode
// and uses no-follow, directory-relative syscalls for its fixed state leaves.
type FileStateStore struct {
	root      string
	accountID string
	uid       uint32
	device    uint64
	inode     uint64
}

func openStateRoot(root string, uid uint32, device, inode uint64, bound bool) (int, unix.Stat_t, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, unix.Stat_t{}, errors.New("Shadow generation state root could not be opened")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR ||
		stat.Mode&0o077 != 0 || stat.Uid != uid || bound &&
		(uint64(stat.Dev) != device || uint64(stat.Ino) != inode) {
		_ = unix.Close(fd)
		return -1, unix.Stat_t{}, errors.New("Shadow generation state root identity is invalid")
	}
	return fd, stat, nil
}

func NewFileStateStore(root, accountID string) (*FileStateStore, error) {
	if (!lowerHex(accountID, 8) && !lowerHex(accountID, 16)) || !filepath.IsAbs(root) {
		return nil, errors.New("Shadow generation state root is invalid")
	}
	root = filepath.Clean(root)
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return nil, errors.New("Shadow generation state root contains a symlink")
	}
	uid := uint32(os.Geteuid())
	fd, stat, err := openStateRoot(root, uid, 0, 0, false)
	if err != nil {
		return nil, err
	}
	_ = unix.Close(fd)
	return &FileStateStore{
		root: root, accountID: accountID, uid: uid,
		device: uint64(stat.Dev), inode: uint64(stat.Ino),
	}, nil
}

func stateLeaf(status string) (string, error) {
	switch status {
	case "ready":
		return readyStateLeaf, nil
	case "pending":
		return pendingStateLeaf, nil
	default:
		return "", errors.New("Shadow generation state status is invalid")
	}
}

func stateFileStat(rootFD int, leaf string, allowEmpty bool) (unix.Stat_t, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(rootFD, leaf, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return stat, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 ||
		stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 ||
		(!allowEmpty && stat.Size <= 0) || stat.Size > maxGenerationStateBytes {
		return stat, errors.New("Shadow generation state file identity is invalid")
	}
	return stat, nil
}

func sameStateFile(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Uid == right.Uid &&
		left.Mode == right.Mode && left.Nlink == right.Nlink && left.Size == right.Size
}

func removeStateLeaf(rootFD int, leaf string, allowEmpty bool) error {
	stat, err := stateFileStat(rootFD, leaf, allowEmpty)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	current, err := stateFileStat(rootFD, leaf, allowEmpty)
	if err != nil || !sameStateFile(stat, current) {
		return errors.New("Shadow generation state removal target drifted")
	}
	if err := unix.Unlinkat(rootFD, leaf, 0); err != nil {
		return err
	}
	return unix.Fsync(rootFD)
}

func readState(rootFD int, leaf, accountID, status string) (GenerationState, bool, unix.Stat_t, error) {
	stat, err := stateFileStat(rootFD, leaf, false)
	if errors.Is(err, unix.ENOENT) {
		return GenerationState{}, false, stat, nil
	}
	if err != nil {
		return GenerationState{}, false, stat, err
	}
	fd, err := unix.Openat(rootFD, leaf, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return GenerationState{}, false, stat, err
	}
	file := os.NewFile(uintptr(fd), leaf)
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || !sameStateFile(stat, opened) {
		_ = file.Close()
		return GenerationState{}, false, stat, errors.New("Shadow generation state file drifted while opening")
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, maxGenerationStateBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(payload) == 0 || int64(len(payload)) != stat.Size ||
		len(payload) > maxGenerationStateBytes {
		return GenerationState{}, false, stat, errors.New("Shadow generation state file is unreadable")
	}
	var state GenerationState
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		state.Validate() != nil || state.AccountBindingID != accountID || state.Status != status {
		return GenerationState{}, false, stat, errors.New("Shadow generation state binding is invalid")
	}
	return state, true, stat, nil
}

func (value *FileStateStore) withRoot(ctx context.Context, action func(int) error) error {
	if value == nil || ctx == nil {
		return errors.New("Shadow generation state operation was cancelled or unconfigured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, _, err := openStateRoot(value.root, value.uid, value.device, value.inode, true)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return action(fd)
}

func (value *FileStateStore) load(ctx context.Context, status, accountID string) (GenerationState, bool, error) {
	if value == nil || accountID != value.accountID {
		return GenerationState{}, false, errors.New("Shadow generation state lookup is not account-bound")
	}
	leaf, err := stateLeaf(status)
	if err != nil {
		return GenerationState{}, false, err
	}
	var state GenerationState
	var found bool
	err = value.withRoot(ctx, func(rootFD int) error {
		if err := removeStateLeaf(rootFD, leaf+".next", true); err != nil {
			return err
		}
		var err error
		state, found, _, err = readState(rootFD, leaf, accountID, status)
		return err
	})
	return state, found, err
}

func (value *FileStateStore) save(ctx context.Context, state GenerationState) error {
	if value == nil || state.Validate() != nil || state.AccountBindingID != value.accountID {
		return errors.New("Shadow generation state write is invalid")
	}
	leaf, err := stateLeaf(state.Status)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(state)
	if err != nil || len(payload) > maxGenerationStateBytes-1 {
		return errors.New("Shadow generation state encoding is invalid")
	}
	payload = append(payload, '\n')
	return value.withRoot(ctx, func(rootFD int) error {
		next := leaf + ".next"
		if err := removeStateLeaf(rootFD, next, true); err != nil {
			return err
		}
		current, currentErr := stateFileStat(rootFD, leaf, false)
		if currentErr != nil && !errors.Is(currentErr, unix.ENOENT) {
			return currentErr
		}
		if currentErr == nil && state.Status != "ready" {
			return errors.New("pending Shadow generation state already exists")
		}
		fd, err := unix.Openat(rootFD, next,
			unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
		if err != nil {
			return err
		}
		file := os.NewFile(uintptr(fd), next)
		published := false
		defer func() {
			_ = file.Close()
			if !published {
				_ = unix.Unlinkat(rootFD, next, 0)
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
		if errors.Is(currentErr, unix.ENOENT) {
			if err := unix.RenameatxNp(rootFD, next, rootFD, leaf, unix.RENAME_EXCL); err != nil {
				return err
			}
			published = true
			return unix.Fsync(rootFD)
		}
		if err := unix.RenameatxNp(rootFD, next, rootFD, leaf, unix.RENAME_SWAP); err != nil {
			return err
		}
		published = true
		swapped, err := stateFileStat(rootFD, next, false)
		if err != nil || !sameStateFile(current, swapped) {
			return errors.New("replaced Shadow generation state identity drifted")
		}
		if err := unix.Unlinkat(rootFD, next, 0); err != nil {
			return err
		}
		return unix.Fsync(rootFD)
	})
}

func (value *FileStateStore) LoadReady(ctx context.Context, accountID string) (GenerationState, bool, error) {
	return value.load(ctx, "ready", accountID)
}

func (value *FileStateStore) LoadPending(ctx context.Context, accountID string) (GenerationState, bool, error) {
	return value.load(ctx, "pending", accountID)
}

func (value *FileStateStore) SaveReady(ctx context.Context, state GenerationState) error {
	if state.Status != "ready" {
		return errors.New("ready Shadow generation store received another state")
	}
	return value.save(ctx, state)
}

func (value *FileStateStore) SavePending(ctx context.Context, state GenerationState) error {
	if state.Status != "pending" {
		return errors.New("pending Shadow generation store received another state")
	}
	return value.save(ctx, state)
}

func (value *FileStateStore) RemovePending(ctx context.Context, accountID string) error {
	if value == nil || accountID != value.accountID {
		return errors.New("Shadow pending generation removal is not account-bound")
	}
	return value.withRoot(ctx, func(rootFD int) error {
		if err := removeStateLeaf(rootFD, pendingStateLeaf+".next", true); err != nil {
			return err
		}
		_, found, stat, err := readState(rootFD, pendingStateLeaf, accountID, "pending")
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		current, err := stateFileStat(rootFD, pendingStateLeaf, false)
		if err != nil || !sameStateFile(stat, current) {
			return errors.New("pending Shadow generation removal target drifted")
		}
		if err := unix.Unlinkat(rootFD, pendingStateLeaf, 0); err != nil {
			return err
		}
		return unix.Fsync(rootFD)
	})
}
