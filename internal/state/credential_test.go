package state

import (
	"strings"
	"testing"

	"github.com/zanescope/v-local-cli/internal/provider"
)

func TestStructuredCredentialRoundTripsThroughSystemKeyring(t *testing.T) {
	accountID := "a1b2c3d4e5f60708"
	bundle := provider.CandidateBundle{
		CatalogID:        "catalog-1",
		DatabaseKeys:     map[string]string{"message.db": strings.Repeat("a", 64)},
		DatabaseProfiles: map[string]string{"message.db": "wcdb-v4-sha512-256000-r80"},
		DatabaseCredential: &provider.DatabaseCredential{
			Mode: "global_passphrase", CredentialEpoch: "epoch-1", AccountBindingID: "provider-account", StorageAccountID: accountID,
			Roots: []provider.CredentialRoot{{
				CredentialID: "root-1", Kind: "global_passphrase", ProfileID: "wcdb-v4-sha512-256000-r80",
				Secret: strings.Repeat("7b", 32), Scope: "account", VerifiedCatalogID: "catalog-1",
				VerifiedDatabaseIDs: []string{"db-1", "db-2"}, SourceEvidence: []string{"multiple_salt_hmac", "macos_pbkdf_hook"},
			}},
		},
	}
	if err := SaveSecrets(accountID, bundle); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSecrets(accountID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DatabaseCredential == nil || loaded.DatabaseCredential.CredentialEpoch != "epoch-1" || loaded.CatalogID != "catalog-1" {
		t.Fatalf("structured credential did not round-trip: %+v", loaded)
	}
	_ = DeleteSecrets(accountID)
}

func TestStructuredCredentialRejectsCrossAccountLoad(t *testing.T) {
	storedUnder := "1111111111111111"
	bundle := provider.CandidateBundle{
		DatabaseKeys: map[string]string{"message.db": strings.Repeat("a", 64)},
		DatabaseCredential: &provider.DatabaseCredential{
			Mode: "global_passphrase", CredentialEpoch: "epoch-1", AccountBindingID: "provider-account", StorageAccountID: "2222222222222222",
			Roots: []provider.CredentialRoot{{
				CredentialID: "root-1", Kind: "global_passphrase", ProfileID: "wcdb-v4-sha512-256000-r80",
				Secret: strings.Repeat("7b", 32), Scope: "account", VerifiedCatalogID: "catalog-1",
				VerifiedDatabaseIDs: []string{"db-1", "db-2"}, SourceEvidence: []string{"multiple_salt_hmac", "macos_pbkdf_hook"},
			}},
		},
	}
	if err := SaveSecrets(storedUnder, bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecrets(storedUnder); err == nil {
		t.Fatal("cross-account credential binding should be rejected")
	}
	_ = DeleteSecrets(storedUnder)
}
