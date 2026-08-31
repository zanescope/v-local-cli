//go:build windows

package shadowbuildset

import "os"

// Production build-set qualification is a POSIX-mode gate. Windows retains
// the common open/read/path identity checks for native synthetic tests.
func singleLinkArtifact(os.FileInfo) bool { return true }
