//go:build windows

package shadowsource

import "errors"

func sourceIdentity(string) (Identity, error) {
	return Identity{}, errors.New("macOS source identity is unavailable on Windows")
}
