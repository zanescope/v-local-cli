package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
	"github.com/zanescope/v-local-cli/internal/provider"
)

type memoryCredentialStore struct {
	values          map[string]string
	failSetUsers    map[string]error
	failDeleteUsers map[string]error
	deleteAttempts  map[string]int
}

func newMemoryCredentialStore() *memoryCredentialStore {
	return &memoryCredentialStore{
		values: map[string]string{}, failSetUsers: map[string]error{},
		failDeleteUsers: map[string]error{}, deleteAttempts: map[string]int{},
	}
}

func credentialStoreKey(service, user string) string {
	return service + "\x00" + user
}

func (store *memoryCredentialStore) Set(service, user, password string) error {
	if err := store.failSetUsers[user]; err != nil {
		return err
	}
	store.values[credentialStoreKey(service, user)] = password
	return nil
}

func (store *memoryCredentialStore) Get(service, user string) (string, error) {
	value, found := store.values[credentialStoreKey(service, user)]
	if !found {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (store *memoryCredentialStore) Delete(service, user string) error {
	store.deleteAttempts[user]++
	if err := store.failDeleteUsers[user]; err != nil {
		return err
	}
	key := credentialStoreKey(service, user)
	if _, found := store.values[key]; !found {
		return keyring.ErrNotFound
	}
	delete(store.values, key)
	return nil
}

func useMemoryCredentialStore(t *testing.T) *memoryCredentialStore {
	t.Helper()
	previous := savedSecretsStore
	store := newMemoryCredentialStore()
	savedSecretsStore = store
	t.Cleanup(func() { savedSecretsStore = previous })
	return store
}

func perDatabaseCredentialBundle(accountID, epoch string, count int) provider.CandidateBundle {
	overrides := make(map[string]provider.CredentialOverride, count)
	for index := 0; index < count; index++ {
		databaseID := sha256.Sum256([]byte(fmt.Sprintf("chunk-database-%d", index)))
		secret := sha256.Sum256([]byte(fmt.Sprintf("chunk-secret-%d-%s", index, epoch)))
		processID := sha256.Sum256([]byte(fmt.Sprintf("chunk-process-%d", index)))
		overrides[hex.EncodeToString(databaseID[:])] = provider.CredentialOverride{
			Kind: "raw_enc_key", ProfileID: "wcdb-v4-sha512-256000-r80",
			Secret: hex.EncodeToString(secret[:]), RelativePath: fmt.Sprintf("message/msg%d.db", index),
			SourceEvidence:     []string{"generation_hmac_verified", hex.EncodeToString(databaseID[:])},
			ProcessInstanceIDs: []string{"windows-process:" + hex.EncodeToString(processID[:])},
		}
	}
	return provider.CandidateBundle{
		CatalogID: strings.Repeat("c", 64),
		DatabaseCredential: &provider.DatabaseCredential{
			Mode: "per_database", CredentialEpoch: epoch, AccountBindingID: "provider-account",
			StorageAccountID: accountID, Overrides: overrides,
		},
		ImageKeys: &provider.ImageKeys{AES: "0123456789abcdef", XOR: 90},
	}
}

func TestStructuredCredentialRoundTripsThroughCredentialStore(t *testing.T) {
	store := useMemoryCredentialStore(t)
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
	manifestPayload, err := store.Get(keyringService, accountID)
	if err != nil {
		t.Fatal(err)
	}
	var manifest savedSecretsEnvelope
	if err := decodeStrictJSON([]byte(manifestPayload), &manifest); err != nil ||
		manifest.Encoding != savedSecretsChunkEncoding || manifest.ChunkCount != 1 {
		t.Fatalf("小凭据未使用原子 manifest + 单分片格式：manifest=%+v err=%v", manifest, err)
	}
	_ = DeleteSecrets(accountID)
}

func TestStructuredCredentialEnvelopeFitsWindowsCredentialLimit(t *testing.T) {
	accountID := "a1b2c3d4e5f60708"
	databaseIDs := make([]string, 0, 19)
	processIDs := make([]string, 0, 5)
	keys := map[string]string{}
	profiles := map[string]string{}
	for index := 0; index < 19; index++ {
		digest := sha256.Sum256([]byte(fmt.Sprintf("database-%d", index)))
		databaseIDs = append(databaseIDs, hex.EncodeToString(digest[:]))
		path := fmt.Sprintf("message/msg%d.db", index)
		key := sha256.Sum256([]byte(fmt.Sprintf("key-%d", index)))
		keys[path] = hex.EncodeToString(key[:])
		profiles[path] = "wcdb-v4-sha512-256000-r80"
	}
	for index := 0; index < 5; index++ {
		digest := sha256.Sum256([]byte(fmt.Sprintf("process-%d", index)))
		processIDs = append(processIDs, "windows-process:"+hex.EncodeToString(digest[:]))
	}
	bundle := provider.CandidateBundle{
		CatalogID: strings.Repeat("c", 64), DatabaseKeys: keys, DatabaseProfiles: profiles,
		DatabaseCredential: &provider.DatabaseCredential{
			Mode: "global_passphrase", CredentialEpoch: "epoch-1", AccountBindingID: "provider-account", StorageAccountID: accountID,
			Roots: []provider.CredentialRoot{{
				CredentialID: "root-1", Kind: "global_passphrase", ProfileID: "wcdb-v4-sha512-256000-r80",
				Secret: strings.Repeat("7b", 32), Scope: "account", VerifiedCatalogID: strings.Repeat("c", 64),
				VerifiedDatabaseIDs: databaseIDs, SourceEvidence: []string{"multiple_salt_hmac", "macos_pbkdf_hook"},
				ProcessInstanceIDs: processIDs,
			}},
		},
		ImageKeys: &provider.ImageKeys{AES: "0123456789abcdef", XOR: 90},
		Profiles: []provider.ProfileSummary{{
			ID: "wcdb-v4-sha512-256000-r80", CipherAlgorithm: "aes-256-cbc", KeySize: 32, PageSize: 4096,
			PlaintextHeaderSize: 16, ReserveSize: 80, KDFAlgorithm: "pbkdf2", KDFPRF: "hmac-sha512",
			KDFIterations: 256000, HMACAlgorithm: "hmac-sha512", HMACKDFAlgorithm: "pbkdf2", HMACKDFIterations: 2,
			HMACInputLayout: "page_without_salt_and_hmac_then_page_number", PageNumberEndian: "little-endian",
		}},
	}
	minimal := bundle
	minimal.DatabaseKeys = nil
	minimal.DatabaseProfiles = nil
	payload, err := marshalSavedSecrets(minimal)
	if err != nil {
		t.Fatal(err)
	}
	defer clearSecretBytes(payload)
	if len(payload) > credentialBlobMaxBytes {
		t.Fatalf("v1 keychain envelope 超过 Windows 上限: %d", len(payload))
	}
	decoded, err := decodeSavedSecrets(payload)
	if err != nil || decoded.DatabaseCredential == nil || len(decoded.DatabaseCredential.Roots[0].VerifiedDatabaseIDs) != 19 || len(decoded.DatabaseCredential.Roots[0].ProcessInstanceIDs) != 5 {
		t.Fatalf("v1 keychain envelope 往返失败: err=%v", err)
	}
}

func TestPerDatabaseCredentialUsesChunkedKeyringEnvelope(t *testing.T) {
	store := useMemoryCredentialStore(t)
	accountID := "c1d2e3f405162738"
	bundle := perDatabaseCredentialBundle(accountID, "epoch-chunked", 19)
	encoded, err := encodeSavedSecrets(bundle)
	if err != nil {
		t.Fatal(err)
	}
	single, err := json.Marshal(savedSecretsEnvelope{
		SchemaVersion: savedSecretsSchemaVersion, Encoding: savedSecretsEncoding, Payload: encoded,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer clearSecretBytes(single)
	if len(single) <= credentialBlobMaxBytes {
		t.Fatalf("测试凭据未触发 Windows 分块路径: %d", len(single))
	}
	if err := SaveSecrets(accountID, bundle); err != nil {
		t.Fatal(err)
	}
	manifestPayload, err := store.Get(keyringService, accountID)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes := []byte(manifestPayload)
	defer clearSecretBytes(manifestBytes)
	var manifest savedSecretsEnvelope
	if err := decodeStrictJSON(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Encoding != savedSecretsChunkEncoding || manifest.ChunkCount < 2 {
		t.Fatalf("未写入分块 manifest: encoding=%q chunks=%d", manifest.Encoding, manifest.ChunkCount)
	}
	loaded, err := LoadSecrets(accountID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DatabaseCredential == nil || loaded.DatabaseCredential.CredentialEpoch != "epoch-chunked" ||
		len(loaded.DatabaseCredential.Overrides) != len(bundle.DatabaseCredential.Overrides) {
		t.Fatal("分块凭据往返不完整")
	}
	slot, count := manifest.ChunkSlot, manifest.ChunkCount
	if err := DeleteSecrets(accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(keyringService, accountID); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("分块 manifest 未删除: %v", err)
	}
	for index := 0; index < count; index++ {
		if _, err := store.Get(keyringService, savedSecretsChunkAccount(accountID, slot, index)); !errors.Is(err, keyring.ErrNotFound) {
			t.Fatalf("分块 %d 未删除: %v", index, err)
		}
	}
}

func TestStructuredCredentialRejectsCrossAccountLoad(t *testing.T) {
	useMemoryCredentialStore(t)
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
	if _, err := LoadSecrets(storedUnder); !errors.Is(err, ErrSavedSecretsInvalid) {
		t.Fatalf("cross-account credential binding error = %v", err)
	}
	_ = DeleteSecrets(storedUnder)
}

func TestSavedSecretsNeverExceedCredentialBlobByteLimit(t *testing.T) {
	store := useMemoryCredentialStore(t)
	accountID := "0011223344556677"
	if err := SaveSecrets(accountID, perDatabaseCredentialBundle(accountID, "epoch-limit", 19)); err != nil {
		t.Fatal(err)
	}
	for target, value := range store.values {
		if len([]byte(value)) > credentialBlobMaxBytes {
			t.Fatalf("Credential Manager 条目超过字节上限：target=%q bytes=%d", target, len([]byte(value)))
		}
	}
}

func TestSavedSecretsChunkFailureKeepsPreviousManifest(t *testing.T) {
	store := useMemoryCredentialStore(t)
	accountID := "1021324354657687"
	first := perDatabaseCredentialBundle(accountID, "epoch-first", 19)
	if err := SaveSecrets(accountID, first); err != nil {
		t.Fatal(err)
	}
	previousManifest, err := store.Get(keyringService, accountID)
	if err != nil {
		t.Fatal(err)
	}
	store.failSetUsers[savedSecretsChunkAccount(accountID, "b", 1)] = errors.New("injected chunk failure")
	if err := SaveSecrets(accountID, perDatabaseCredentialBundle(accountID, "epoch-second", 19)); err == nil || SavedSecretsCommitted(err) {
		t.Fatalf("分片写入失败没有在 manifest 提交前失败：%v", err)
	}
	currentManifest, err := store.Get(keyringService, accountID)
	if err != nil || currentManifest != previousManifest {
		t.Fatalf("分片失败改变了旧 manifest：err=%v", err)
	}
	loaded, err := LoadSecrets(accountID)
	if err != nil || loaded.DatabaseCredential == nil || loaded.DatabaseCredential.CredentialEpoch != "epoch-first" {
		t.Fatalf("分片失败后旧凭据不可读：epoch=%v err=%v", loaded.DatabaseCredential, err)
	}
}

func TestSavedSecretsManifestFailureKeepsPreviousCredential(t *testing.T) {
	store := useMemoryCredentialStore(t)
	accountID := "2031425364758697"
	if err := SaveSecrets(accountID, perDatabaseCredentialBundle(accountID, "epoch-first", 19)); err != nil {
		t.Fatal(err)
	}
	previousManifest, err := store.Get(keyringService, accountID)
	if err != nil {
		t.Fatal(err)
	}
	store.failSetUsers[accountID] = errors.New("injected manifest failure")
	if err := SaveSecrets(accountID, perDatabaseCredentialBundle(accountID, "epoch-second", 19)); err == nil || SavedSecretsCommitted(err) {
		t.Fatalf("manifest 写入失败没有保持未提交状态：%v", err)
	}
	currentManifest, err := store.Get(keyringService, accountID)
	if err != nil || currentManifest != previousManifest {
		t.Fatalf("manifest 失败改变了旧凭据：err=%v", err)
	}
	if _, err := store.Get(keyringService, savedSecretsChunkAccount(accountID, "b", 0)); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("manifest 失败后暂存分片未清理：%v", err)
	}
}

func TestSavedSecretsWithoutManifestCleansBothOrphanSlots(t *testing.T) {
	store := useMemoryCredentialStore(t)
	accountID := "2536475869708192"
	orphan := savedSecretsChunkAccount(accountID, "b", maxSavedSecretsChunks-1)
	store.values[credentialStoreKey(keyringService, orphan)] = "orphaned-secret-fragment"

	if err := SaveSecrets(accountID, perDatabaseCredentialBundle(accountID, "epoch-recovered", 19)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(keyringService, orphan); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("缺少 manifest 时另一槽位的孤儿分片未清理：%v", err)
	}
}

func TestSavedSecretsCommitCleansInactiveSlotTail(t *testing.T) {
	store := useMemoryCredentialStore(t)
	accountID := "2637485960718293"
	if err := SaveSecrets(accountID, perDatabaseCredentialBundle(accountID, "epoch-first", 19)); err != nil {
		t.Fatal(err)
	}
	stale := savedSecretsChunkAccount(accountID, "a", maxSavedSecretsChunks-1)
	store.values[credentialStoreKey(keyringService, stale)] = "stale-tail-fragment"

	if err := SaveSecrets(accountID, perDatabaseCredentialBundle(accountID, "epoch-second", 19)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(keyringService, stale); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("manifest 提交后旧槽位尾部分片未清理：%v", err)
	}
}

func TestSavedSecretsReportsPostCommitCleanupFailure(t *testing.T) {
	store := useMemoryCredentialStore(t)
	accountID := "3041526374859607"
	if err := SaveSecrets(accountID, perDatabaseCredentialBundle(accountID, "epoch-first", 19)); err != nil {
		t.Fatal(err)
	}
	oldChunk := savedSecretsChunkAccount(accountID, "a", 0)
	store.failDeleteUsers[oldChunk] = errors.New("injected cleanup failure")
	err := SaveSecrets(accountID, perDatabaseCredentialBundle(accountID, "epoch-second", 19))
	if err == nil || !SavedSecretsCommitted(err) {
		t.Fatalf("manifest 提交后的清理失败未被分类：%v", err)
	}
	loaded, loadErr := LoadSecrets(accountID)
	if loadErr != nil || loaded.DatabaseCredential == nil || loaded.DatabaseCredential.CredentialEpoch != "epoch-second" {
		t.Fatalf("清理失败后新 manifest 不可读：credential=%v err=%v", loaded.DatabaseCredential, loadErr)
	}
}

func TestSavedSecretsDigestMismatchFailsClosed(t *testing.T) {
	store := useMemoryCredentialStore(t)
	accountID := "4051627385960718"
	if err := SaveSecrets(accountID, perDatabaseCredentialBundle(accountID, "epoch-digest", 19)); err != nil {
		t.Fatal(err)
	}
	manifestPayload, err := store.Get(keyringService, accountID)
	if err != nil {
		t.Fatal(err)
	}
	var manifest savedSecretsEnvelope
	if err := decodeStrictJSON([]byte(manifestPayload), &manifest); err != nil {
		t.Fatal(err)
	}
	chunkTarget := savedSecretsChunkAccount(accountID, manifest.ChunkSlot, 0)
	store.values[credentialStoreKey(keyringService, chunkTarget)] += "A"
	if _, err := LoadSecrets(accountID); !errors.Is(err, ErrSavedSecretsInvalid) {
		t.Fatalf("摘要不匹配的分片仍被接受：%v", err)
	}
}

func TestDeleteSecretsAttemptsAllEntriesAfterOneFailure(t *testing.T) {
	store := useMemoryCredentialStore(t)
	accountID := "5061728396071829"
	mainKey := credentialStoreKey(keyringService, accountID)
	aChunk := savedSecretsChunkAccount(accountID, "a", 0)
	bChunk := savedSecretsChunkAccount(accountID, "b", 0)
	store.values[mainKey] = "manifest"
	store.values[credentialStoreKey(keyringService, aChunk)] = "a"
	store.values[credentialStoreKey(keyringService, bChunk)] = "b"
	store.failDeleteUsers[aChunk] = errors.New("injected delete failure")
	if err := DeleteSecrets(accountID); err == nil {
		t.Fatal("单个删除失败未向调用方报告")
	}
	if _, found := store.values[mainKey]; found {
		t.Fatal("主 manifest 在其他条目失败后未删除")
	}
	if _, found := store.values[credentialStoreKey(keyringService, bChunk)]; found || store.deleteAttempts[bChunk] == 0 {
		t.Fatal("单个失败阻止了其余分片的 best-effort 删除")
	}
}

func TestSavedSecretsApplicationChunkBudgetFailsClosed(t *testing.T) {
	store := useMemoryCredentialStore(t)
	accountID := "6071829307182930"
	err := SaveSecrets(accountID, perDatabaseCredentialBundle(accountID, "epoch-overflow", 4096))
	if !errors.Is(err, keyring.ErrSetDataTooBig) {
		t.Fatalf("超过应用分片预算的凭据未 fail closed：%v", err)
	}
	if len(store.values) != 0 {
		t.Fatal("超过分片预算后仍写入了 Credential Manager")
	}
}
