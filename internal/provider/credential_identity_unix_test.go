//go:build !windows

package provider

import "testing"

func TestNormalizeDarwinCredentialSystemAlias(t *testing.T) {
	if got := normalizeDarwinCredentialSystemAlias("/private/var/folders/test/database.db"); got != "/var/folders/test/database.db" {
		t.Fatalf("unexpected system alias normalization: %q", got)
	}
	if got := normalizeDarwinCredentialSystemAlias("/private/variable/database.db"); got != "/private/variable/database.db" {
		t.Fatalf("non-system prefix was normalized: %q", got)
	}
}
