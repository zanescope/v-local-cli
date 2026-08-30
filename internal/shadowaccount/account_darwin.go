//go:build darwin

package shadowaccount

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

type lookupUser func(string) (*user.User, error)

func canonicalHome(path string, expectedUID uint32) (string, uint64, uint64, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", 0, 0, errors.New("account database returned a non-canonical home")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", 0, 0, errors.New("account database home is not an ordinary directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", 0, 0, errors.New("account database home contains a symlink")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != expectedUID || stat.Dev == 0 || stat.Ino == 0 {
		return "", 0, 0, errors.New("account database home identity is invalid")
	}
	return path, uint64(stat.Dev), uint64(stat.Ino), nil
}

func bindingID(uid uint32, device, inode uint64) string {
	payload := make([]byte, 20)
	binary.BigEndian.PutUint32(payload[0:4], uid)
	binary.BigEndian.PutUint64(payload[4:12], device)
	binary.BigEndian.PutUint64(payload[12:20], inode)
	digest := sha256.Sum256(append([]byte("v-local-shadow-account/v1\x00"), payload...))
	return hex.EncodeToString(digest[:16])
}

func resolveCurrent(lookup lookupUser, effectiveUID int) (Record, error) {
	if lookup == nil || effectiveUID < 0 {
		return Record{}, errors.New("Shadow account resolver is unavailable")
	}
	entry, err := lookup(strconv.Itoa(effectiveUID))
	if err != nil || entry == nil {
		return Record{}, errors.New("effective user is absent from the account database")
	}
	parsedUID, err := strconv.ParseUint(entry.Uid, 10, 32)
	if err != nil || uint32(parsedUID) != uint32(effectiveUID) {
		return Record{}, errors.New("account database UID does not match the effective user")
	}
	home, device, inode, err := canonicalHome(entry.HomeDir, uint32(effectiveUID))
	if err != nil {
		return Record{}, err
	}
	result := Record{
		UID: uint32(effectiveUID), Home: home, HomeDevice: device, HomeInode: inode,
		SecurityRoot:   filepath.Join(home, "Library", applicationSupportLeaf, productRootLeaf, runtimeRootLeaf),
		ContainersRoot: filepath.Join(home, "Library", "Containers"),
		BindingID:      bindingID(uint32(effectiveUID), device, inode),
	}
	if err := result.Validate(); err != nil {
		return Record{}, err
	}
	return result, nil
}

func ResolveCurrent() (Record, error) {
	return resolveCurrent(user.LookupId, os.Geteuid())
}

func Revalidate(value Record) error {
	if err := value.Validate(); err != nil || value.UID != uint32(os.Geteuid()) {
		return errors.New("Shadow account binding no longer matches the effective user")
	}
	home, device, inode, err := canonicalHome(value.Home, value.UID)
	if err != nil || home != value.Home || device != value.HomeDevice || inode != value.HomeInode ||
		bindingID(value.UID, device, inode) != value.BindingID {
		return errors.New("Shadow account binding drifted")
	}
	return nil
}
