// Package shadowinventory creates a normalized, content-bound App inventory
// without following paths outside the exact source root.
package shadowinventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxFileBytes  = int64(1024 * 1024 * 1024)
	maxTotalBytes = int64(8 * 1024 * 1024 * 1024)
	maxEntries    = 16 * 1024
)

type Entry struct {
	Path       string `json:"path"`
	Type       string `json:"type"`
	Mode       uint32 `json:"mode"`
	Size       int64  `json:"size,omitempty"`
	Digest     string `json:"digest,omitempty"`
	LinkTarget string `json:"link_target,omitempty"`
}

func safeRelative(value string) bool {
	return value != "" && !path.IsAbs(value) && !strings.ContainsAny(value, "\\:") &&
		path.Clean(value) == value && value != ".." && !strings.HasPrefix(value, "../")
}

func WithinRoot(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	return target == root || strings.HasPrefix(target, root+string(filepath.Separator))
}

func TrustedRoot(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", errors.New("App inventory root must be absolute")
	}
	root = filepath.Clean(root)
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("App inventory root is not an ordinary directory")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return "", errors.New("App inventory root contains a symlink")
	}
	return root, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (value contextReader) Read(payload []byte) (int, error) {
	select {
	case <-value.ctx.Done():
		return 0, value.ctx.Err()
	default:
		return value.reader.Read(payload)
	}
}

func Scan(root string) ([]Entry, string, error) {
	return ScanContext(context.Background(), root)
}

func ScanContext(ctx context.Context, root string) ([]Entry, string, error) {
	if ctx == nil {
		return nil, "", errors.New("App inventory context is missing")
	}
	root, err := TrustedRoot(root)
	if err != nil {
		return nil, "", err
	}
	entries := []Entry{}
	var totalBytes int64
	err = filepath.WalkDir(root, func(path string, directoryEntry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if len(entries) >= maxEntries {
			return errors.New("App inventory exceeds the entry bound")
		}
		relative, err := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		if err != nil || !safeRelative(relative) {
			return errors.New("App inventory escaped its root")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		value := Entry{Path: relative, Mode: uint32(info.Mode().Perm())}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			value.Type = "symlink"
			target, readErr := os.Readlink(path)
			lexicalTarget := filepath.Clean(filepath.Join(filepath.Dir(path), target))
			resolvedTarget, resolveErr := filepath.EvalSymlinks(path)
			if readErr != nil || filepath.IsAbs(target) || !WithinRoot(root, lexicalTarget) ||
				resolveErr != nil || !WithinRoot(root, resolvedTarget) {
				return errors.New("App inventory contains an unsafe symlink")
			}
			value.LinkTarget = target
		case info.IsDir():
			value.Type = "directory"
		case info.Mode().IsRegular():
			value.Type = "file"
			links, linkErr := regularFileLinkCount(info)
			if linkErr != nil || links != 1 {
				return errors.New("App inventory contains a hard-linked or unbound file")
			}
			if info.Size() < 0 || info.Size() > maxFileBytes || totalBytes > maxTotalBytes-info.Size() {
				return errors.New("App inventory exceeds the content bound")
			}
			file, openErr := os.Open(path)
			if openErr != nil {
				return openErr
			}
			digest := sha256.New()
			read, copyErr := io.Copy(digest, io.LimitReader(contextReader{ctx: ctx, reader: file}, maxFileBytes+1))
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil || read != info.Size() {
				return errors.New("App inventory could not hash a file")
			}
			value.Size = info.Size()
			value.Digest = hex.EncodeToString(digest.Sum(nil))
			totalBytes += info.Size()
		default:
			return errors.New("App inventory contains a special file")
		}
		entries = append(entries, value)
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Path < entries[right].Path })
	payload, err := json.Marshal(entries)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return entries, hex.EncodeToString(digest[:]), nil
}
