package provider

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
)

func writeCredentialDatabase(t *testing.T, path string, passphrase, salt []byte) string {
	t.Helper()
	key, err := pbkdf2.Key(sha512.New, string(passphrase), salt, cryptoutil.SQLCipherKDFRuns, 32)
	if err != nil {
		t.Fatal(err)
	}
	page := make([]byte, cryptoutil.SQLCipherPageSize)
	copy(page[:16], salt)
	plain := make([]byte, cryptoutil.SQLCipherPageSize-16-cryptoutil.SQLCipherReserve)
	plain[0], plain[1] = 0x10, 0x00
	plain[4], plain[5], plain[6], plain[7] = cryptoutil.SQLCipherReserve, 64, 32, 32
	iv := bytes.Repeat([]byte{0x5a}, aes.BlockSize)
	copy(page[cryptoutil.SQLCipherPageSize-cryptoutil.SQLCipherReserve:], iv)
	block, _ := aes.NewCipher(key)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(page[16:cryptoutil.SQLCipherPageSize-cryptoutil.SQLCipherReserve], plain)
	hmacSalt := append([]byte(nil), salt...)
	for index := range hmacSalt {
		hmacSalt[index] ^= 0x3a
	}
	macKey, err := pbkdf2.Key(sha512.New, string(key), hmacSalt, 2, 32)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha512.New, macKey)
	_, _ = mac.Write(page[16 : cryptoutil.SQLCipherPageSize-sha512.Size])
	pageNumber := make([]byte, 4)
	binary.LittleEndian.PutUint32(pageNumber, 1)
	_, _ = mac.Write(pageNumber)
	copy(page[cryptoutil.SQLCipherPageSize-sha512.Size:], mac.Sum(nil))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, page, 0o600); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(key)
}

func TestExpandGlobalCredentialCoversNewDatabaseWithoutProcessAccess(t *testing.T) {
	root := t.TempDir()
	passphrase := bytes.Repeat([]byte{0x7b}, 32)
	wantFirst := writeCredentialDatabase(t, filepath.Join(root, "message", "message_0.db"), passphrase, bytes.Repeat([]byte{0x11}, 16))
	wantSecond := writeCredentialDatabase(t, filepath.Join(root, "contact", "contact.db"), passphrase, bytes.Repeat([]byte{0x22}, 16))
	bundle := CandidateBundle{DatabaseCredential: &DatabaseCredential{
		Mode: "global_passphrase", CredentialEpoch: "epoch-1", AccountBindingID: "account-1",
		Roots: []CredentialRoot{{
			CredentialID: "root-1", Kind: "global_passphrase", ProfileID: "wcdb-v4-sha512-256000-r80",
			Secret: hex.EncodeToString(passphrase), Scope: "account", VerifiedCatalogID: "catalog-1",
			VerifiedDatabaseIDs: []string{"db-1", "db-2"}, SourceEvidence: []string{"multiple_salt_hmac", "macos_pbkdf_hook"},
		}},
	}}
	expanded, coverage, err := ExpandCredential(bundle, root)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.DatabaseCount != 2 || coverage.MatchedDatabaseCount != 2 || len(coverage.MissingDatabases) != 0 {
		t.Fatalf("unexpected coverage: %+v", coverage)
	}
	if expanded.DatabaseKeys[filepath.Join("message", "message_0.db")] != wantFirst || expanded.DatabaseKeys[filepath.Join("contact", "contact.db")] != wantSecond || expanded.CatalogID == "" {
		t.Fatalf("global credential did not derive effective keys: %+v", expanded)
	}
}

func TestValidateCredentialRejectsPathTraversalOverride(t *testing.T) {
	bundle := CandidateBundle{
		DatabaseKeys: map[string]string{"db": strings.Repeat("a", 64)},
		DatabaseCredential: &DatabaseCredential{
			Mode: "per_database", CredentialEpoch: "epoch",
			Overrides: map[string]CredentialOverride{"id": {Kind: "raw_enc_key", ProfileID: "wcdb-v4-sha512-256000-r80", Secret: strings.Repeat("a", 64), RelativePath: "../outside.db"}},
		},
	}
	if err := ValidateBundle(&bundle); err == nil {
		t.Fatal("path traversal override should be rejected")
	}
}

func TestValidateCredentialRejectsModeContradictionAndDuplicatePaths(t *testing.T) {
	root := CredentialRoot{
		CredentialID: "root", Kind: "global_passphrase", ProfileID: cryptoutil.SQLCipherDefaultProfileID,
		Secret: strings.Repeat("7b", 32), Scope: "account", VerifiedCatalogID: "catalog",
		VerifiedDatabaseIDs: []string{"db-1", "db-2"}, SourceEvidence: []string{"multiple_salt_hmac", "macos_pbkdf_hook"},
	}
	override := CredentialOverride{
		Kind: "raw_enc_key", ProfileID: cryptoutil.SQLCipherDefaultProfileID,
		Secret: strings.Repeat("a", 64), RelativePath: "caf\u00e9.db",
	}
	contradictory := &DatabaseCredential{
		Mode: "per_database", CredentialEpoch: "epoch", AccountBindingID: "account",
		Roots: []CredentialRoot{root}, Overrides: map[string]CredentialOverride{"db": override},
	}
	if err := validateDatabaseCredential(contradictory); err == nil {
		t.Fatal("credential mode contradiction was accepted")
	}
	duplicate := &DatabaseCredential{
		Mode: "per_database", CredentialEpoch: "epoch", AccountBindingID: "account",
		Overrides: map[string]CredentialOverride{
			"db-1": override,
			"db-2": {Kind: "raw_enc_key", ProfileID: cryptoutil.SQLCipherDefaultProfileID, Secret: strings.Repeat("b", 64), RelativePath: "cafe\u0301.db"},
		},
	}
	if err := validateDatabaseCredential(duplicate); err == nil {
		t.Fatal("duplicate normalized credential override paths were accepted")
	}
}

func TestValidateCredentialRejectsUnprovenGlobalRoot(t *testing.T) {
	bundle := CandidateBundle{
		DatabaseKeys: map[string]string{"message.db": strings.Repeat("a", 64)},
		DatabaseCredential: &DatabaseCredential{
			Mode: "global_passphrase", CredentialEpoch: "epoch", AccountBindingID: "account",
			Roots: []CredentialRoot{{
				CredentialID: "root", Kind: "global_passphrase", ProfileID: cryptoutil.SQLCipherDefaultProfileID,
				Secret: strings.Repeat("7b", 32), Scope: "account", VerifiedCatalogID: "catalog",
				VerifiedDatabaseIDs: []string{"only-one"}, SourceEvidence: []string{"multiple_salt_hmac"},
			}},
		},
	}
	if err := ValidateBundle(&bundle); err == nil {
		t.Fatal("unproven global root should be rejected at the CLI trust boundary")
	}
}

func TestValidateCredentialRequiresOpaqueWindowsProcessProvenance(t *testing.T) {
	credential := &DatabaseCredential{
		Mode: "per_database", CredentialEpoch: "epoch", AccountBindingID: "account",
		Overrides: map[string]CredentialOverride{"db": {
			Kind: "raw_enc_key", ProfileID: cryptoutil.SQLCipherDefaultProfileID,
			Secret: strings.Repeat("a", 64), RelativePath: "message.db",
			ProcessInstanceIDs: []string{"windows-process:" + strings.Repeat("b", 64)},
		}},
	}
	if err := validateDatabaseCredential(credential); err != nil {
		t.Fatalf("valid opaque Windows process provenance was rejected: %v", err)
	}
	keys := map[string]string{"message.db": strings.Repeat("a", 64)}
	profiles := map[string]string{"message.db": cryptoutil.SQLCipherDefaultProfileID}
	if err := validateWindowsCredentialProvenance(credential, keys, profiles); err != nil {
		t.Fatalf("valid Windows credential provenance was rejected: %v", err)
	}
	override := credential.Overrides["db"]
	override.ProcessInstanceIDs = []string{"pid-1234"}
	credential.Overrides["db"] = override
	if err := validateDatabaseCredential(credential); err == nil {
		t.Fatal("PID-only credential provenance was accepted")
	}
	override.ProcessInstanceIDs = nil
	credential.Overrides["db"] = override
	if err := validateWindowsCredentialProvenance(credential, keys, profiles); err == nil {
		t.Fatal("Windows credential without process provenance was accepted")
	}
}

func TestBindPartialCredentialStripsRootAndPreservesVerifiedEffectiveKey(t *testing.T) {
	path := filepath.Join("message", "message.db")
	key := strings.Repeat("a", 64)
	credential := &DatabaseCredential{
		Mode: "global_passphrase", CredentialEpoch: "epoch-1", AccountBindingID: "provider-binding",
		Roots: []CredentialRoot{{
			CredentialID: "root-1", Kind: "global_passphrase", ProfileID: cryptoutil.SQLCipherDefaultProfileID,
			Secret: strings.Repeat("7b", 32), Scope: "account", VerifiedCatalogID: "catalog-1",
			VerifiedDatabaseIDs: []string{"db-1", "db-2"}, SourceEvidence: []string{"multiple_salt_hmac", "macos_pbkdf_hook"},
		}},
	}
	bound := BindPartialVerifiedCredential(
		credential, "account-1", map[string]string{path: key},
		map[string]string{path: cryptoutil.SQLCipherDefaultProfileID},
		[]CatalogEntry{{DatabaseID: "db-1", RelativePath: path}},
	)
	if bound == nil || bound.Mode != "per_database" || bound.AccountBindingID != "provider-binding" ||
		bound.StorageAccountID != "account-1" || len(bound.Roots) != 0 || len(bound.Overrides) != 1 {
		t.Fatalf("partial credential was not reduced to a verified override: %+v", bound)
	}
	if override := bound.Overrides["db-1"]; override.Secret != key || override.RelativePath != path {
		t.Fatalf("verified effective key was not preserved: %+v", override)
	}
}

func TestExpandCredentialCatalogIncludesPlaintextDatabase(t *testing.T) {
	root := t.TempDir()
	page := make([]byte, cryptoutil.SQLCipherPageSize)
	copy(page, []byte("SQLite format 3\x00"))
	if err := os.WriteFile(filepath.Join(root, "plain.db"), page, 0o600); err != nil {
		t.Fatal(err)
	}
	expanded, coverage, err := ExpandCredential(CandidateBundle{}, root)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.DatabaseCount != 0 || len(expanded.CatalogEntries) != 1 || expanded.CatalogEntries[0].Classification != "plaintext" || expanded.CatalogID == "" {
		t.Fatalf("plaintext database was omitted from refresh catalog: coverage=%+v bundle=%+v", coverage, expanded)
	}
}

func TestExpandCredentialUsesStableMachineKeyedCatalogIDs(t *testing.T) {
	databaseRoot := t.TempDir()
	page := make([]byte, cryptoutil.SQLCipherPageSize)
	copy(page, []byte("SQLite format 3\x00"))
	if err := os.WriteFile(filepath.Join(databaseRoot, "plain.db"), page, 0o600); err != nil {
		t.Fatal(err)
	}
	privateRoot := t.TempDir()
	first, _, err := ExpandCredentialWithPrivateRoot(CandidateBundle{}, databaseRoot, privateRoot)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := ExpandCredentialWithPrivateRoot(CandidateBundle{}, databaseRoot, privateRoot)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := ExpandCredentialWithPrivateRoot(CandidateBundle{}, databaseRoot, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if first.CatalogID != second.CatalogID || first.CatalogEntries[0].DatabaseID != second.CatalogEntries[0].DatabaseID {
		t.Fatal("catalog identifiers changed while using the same machine key")
	}
	if first.CatalogID == other.CatalogID || first.CatalogEntries[0].DatabaseID == other.CatalogEntries[0].DatabaseID {
		t.Fatal("catalog identifiers were not keyed by the private machine secret")
	}
}
