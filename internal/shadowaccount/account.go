// Package shadowaccount resolves the effective macOS user from the account
// database. Shadow runtime paths must never be derived from HOME or TMPDIR.
package shadowaccount

import (
	"errors"
	"path/filepath"
	"strings"
)

const (
	applicationSupportLeaf = "Application Support"
	productRootLeaf        = "v-local"
	runtimeRootLeaf        = "shadow-runtime"
)

type Record struct {
	UID            uint32
	Home           string
	HomeDevice     uint64
	HomeInode      uint64
	SecurityRoot   string
	ContainersRoot string
	BindingID      string
}

func (value Record) Validate() error {
	if value.HomeDevice == 0 || value.HomeInode == 0 || value.BindingID == "" ||
		!filepath.IsAbs(value.Home) || filepath.Clean(value.Home) != value.Home ||
		value.SecurityRoot != filepath.Join(value.Home, "Library", applicationSupportLeaf, productRootLeaf, runtimeRootLeaf) ||
		value.ContainersRoot != filepath.Join(value.Home, "Library", "Containers") ||
		len(value.BindingID) != 32 || value.BindingID != strings.ToLower(value.BindingID) {
		return errors.New("Shadow account binding is invalid")
	}
	for _, character := range value.BindingID {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return errors.New("Shadow account binding is invalid")
		}
	}
	return nil
}
