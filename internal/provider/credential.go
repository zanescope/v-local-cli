package provider

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
)

type DatabaseCredential struct {
	Mode             string                        `json:"mode"`
	CredentialEpoch  string                        `json:"credential_epoch"`
	AccountBindingID string                        `json:"account_binding_id"`
	StorageAccountID string                        `json:"storage_account_id,omitempty"`
	Roots            []CredentialRoot              `json:"roots,omitempty"`
	Overrides        map[string]CredentialOverride `json:"overrides,omitempty"`
}

type CredentialRoot struct {
	CredentialID        string   `json:"credential_id"`
	Kind                string   `json:"kind"`
	ProfileID           string   `json:"profile_id"`
	Secret              string   `json:"secret"`
	Scope               string   `json:"scope"`
	VerifiedCatalogID   string   `json:"verified_catalog_id"`
	VerifiedDatabaseIDs []string `json:"verified_database_ids"`
	SourceEvidence      []string `json:"source_evidence"`
	ProcessInstanceIDs  []string `json:"process_instance_ids,omitempty"`
}

type CredentialOverride struct {
	Kind               string   `json:"kind"`
	ProfileID          string   `json:"profile_id"`
	Secret             string   `json:"secret"`
	RelativePath       string   `json:"relative_path"`
	SourceEvidence     []string `json:"source_evidence"`
	ProcessInstanceIDs []string `json:"process_instance_ids,omitempty"`
}

type CredentialCoverage struct {
	DatabaseCount        int      `json:"database_count"`
	MatchedDatabaseCount int      `json:"matched_database_count"`
	MissingDatabases     []string `json:"missing_databases,omitempty"`
}

func validSecretHex(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validCredentialProcessInstances(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		digest := strings.TrimPrefix(value, "windows-process:")
		if !strings.HasPrefix(value, "windows-process:") || digest != strings.ToLower(digest) ||
			!validSecretHex(digest) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func validateDatabaseCredential(credential *DatabaseCredential) error {
	if credential == nil {
		return nil
	}
	switch credential.Mode {
	case "global_passphrase", "per_database", "mixed":
	default:
		return errors.New("database credential mode 无效")
	}
	if strings.TrimSpace(credential.CredentialEpoch) == "" {
		return errors.New("database credential epoch 为空")
	}
	if strings.TrimSpace(credential.AccountBindingID) == "" {
		return errors.New("database credential 缺少账号绑定")
	}
	for _, root := range credential.Roots {
		if root.Kind != "global_passphrase" || root.Scope != "account" || root.CredentialID == "" || root.ProfileID == "" || !validSecretHex(root.Secret) {
			return errors.New("global passphrase credential 无效")
		}
		if strings.TrimSpace(root.VerifiedCatalogID) == "" || len(root.VerifiedDatabaseIDs) < 2 {
			return errors.New("global passphrase credential 缺少多库验证绑定")
		}
		verifiedIDs := map[string]bool{}
		for _, databaseID := range root.VerifiedDatabaseIDs {
			if strings.TrimSpace(databaseID) == "" || verifiedIDs[databaseID] {
				return errors.New("global passphrase credential 的数据库验证绑定无效")
			}
			verifiedIDs[databaseID] = true
		}
		evidence := map[string]bool{}
		for _, source := range root.SourceEvidence {
			evidence[source] = true
		}
		if !evidence["multiple_salt_hmac"] || !evidence["macos_pbkdf_hook"] {
			return errors.New("global passphrase credential 缺少完整动态 KDF 证据")
		}
		if !validCredentialProcessInstances(root.ProcessInstanceIDs) {
			return errors.New("global passphrase credential 的进程实例 provenance 无效")
		}
	}
	overridePaths := map[string]bool{}
	for databaseID, override := range credential.Overrides {
		if databaseID == "" || override.Kind != "raw_enc_key" || override.ProfileID == "" || !validSecretHex(override.Secret) {
			return errors.New("per-database credential override 无效")
		}
		clean := filepath.Clean(override.RelativePath)
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return errors.New("credential override 路径越界")
		}
		pathKey := credentialPathKey(clean)
		if overridePaths[pathKey] {
			return errors.New("credential override 路径重复")
		}
		if !validCredentialProcessInstances(override.ProcessInstanceIDs) {
			return errors.New("per-database credential 的进程实例 provenance 无效")
		}
		overridePaths[pathKey] = true
	}
	if credential.Mode == "global_passphrase" && (len(credential.Roots) == 0 || len(credential.Overrides) != 0) ||
		credential.Mode == "per_database" && (len(credential.Roots) != 0 || len(credential.Overrides) == 0) ||
		credential.Mode == "mixed" && (len(credential.Roots) == 0 || len(credential.Overrides) == 0) {
		return errors.New("database credential mode 与内容不一致")
	}
	return nil
}

func validateWindowsCredentialProvenance(credential *DatabaseCredential, keys, profiles map[string]string) error {
	if credential == nil {
		return nil
	}
	if len(credential.Roots) != 0 {
		return errors.New("Windows 静态候选不能提升为账号级根凭据")
	}
	overrides := map[string]CredentialOverride{}
	for _, override := range credential.Overrides {
		if len(override.ProcessInstanceIDs) == 0 {
			return errors.New("Windows per-database credential 缺少进程实例 provenance")
		}
		overrides[credentialPathKey(override.RelativePath)] = override
	}
	if len(overrides) != len(keys) {
		return errors.New("Windows per-database credential 与已验证 key 集合不一致")
	}
	for path, key := range keys {
		override, found := overrides[credentialPathKey(path)]
		if !found || strings.ToLower(override.Secret) != key || override.ProfileID != profiles[path] {
			return errors.New("Windows per-database credential 没有逐库绑定已验证 key/profile")
		}
	}
	return nil
}

type credentialDatabase struct {
	Path           string
	RelativePath   string
	DatabaseID     string
	Identity       string
	Size           int64
	MTimeNS        int64
	FirstPage      string
	Classification string
	ProfileID      string
}

const maxCredentialDatabaseFiles = 4096

func credentialCatalogHMAC(key []byte, values ...string) string {
	mac := hmac.New(sha256.New, key)
	for _, value := range values {
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(value))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func credentialDatabases(root string, catalogKey []byte) ([]credentialDatabase, error) {
	if len(catalogKey) != 32 {
		return nil, errors.New("catalog 标识密钥无效")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(absoluteRoot)
	unsafeRoot := false
	if err == nil {
		unsafeRoot, err = providerPathIsLinkOrReparse(absoluteRoot, rootInfo.Mode())
	}
	if err != nil || !rootInfo.IsDir() || unsafeRoot {
		return nil, errors.New("数据库目录不是可信的普通目录")
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil || credentialPathKey(resolvedRoot) != credentialPathKey(absoluteRoot) {
		return nil, errors.New("数据库目录包含不允许的链接或 reparse point")
	}
	root = absoluteRoot
	values := []credentialDatabase{}
	seenPaths := map[string]string{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		unsafeEntry, safetyErr := providerPathIsLinkOrReparse(path, entry.Type())
		if safetyErr != nil || unsafeEntry {
			return errors.New("数据库目录包含不允许的链接或 reparse point")
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".db") {
			return nil
		}
		if len(values) >= maxCredentialDatabaseFiles {
			return errors.New("数据库数量超过安全上限")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("数据库路径越界")
		}
		pathKey := credentialPathKey(relative)
		if previous, duplicate := seenPaths[pathKey]; duplicate && previous != relative {
			return errors.New("数据库路径存在大小写或 Unicode 归一化碰撞")
		}
		seenPaths[pathKey] = relative
		info, err := entry.Info()
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		openedInfo, err := file.Stat()
		if err != nil || !os.SameFile(info, openedInfo) {
			_ = file.Close()
			return errors.New("数据库文件身份在打开时发生变化")
		}
		identity, err := credentialFileIdentity(file)
		if err != nil {
			_ = file.Close()
			return err
		}
		page := make([]byte, cryptoutil.SQLCipherPageSize)
		read, readErr := io.ReadFull(file, page)
		afterInfo, statErr := file.Stat()
		closeErr := file.Close()
		if closeErr != nil {
			return closeErr
		}
		if statErr != nil || openedInfo.Size() != afterInfo.Size() || openedInfo.ModTime() != afterInfo.ModTime() {
			return errors.New("数据库在凭据派生发现期间发生变化")
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return readErr
		}
		page = page[:read]
		digest := sha256.Sum256(page)
		classification := "encrypted_eligible"
		profileID := "wcdb-v4-sha512-256000-r80"
		if len(page) >= 16 && string(page[:16]) == "SQLite format 3\x00" {
			classification = "plaintext"
			profileID = ""
		} else if len(page) < cryptoutil.SQLCipherPageSize {
			classification = "truncated"
			profileID = ""
		}
		values = append(values, credentialDatabase{
			Path: path, RelativePath: filepath.Clean(relative), DatabaseID: credentialCatalogHMAC(catalogKey, filepath.Clean(relative)),
			Identity: identity, Size: afterInfo.Size(), MTimeNS: afterInfo.ModTime().UnixNano(),
			FirstPage: hex.EncodeToString(digest[:]), Classification: classification, ProfileID: profileID,
		})
		return nil
	})
	sort.Slice(values, func(left, right int) bool { return values[left].RelativePath < values[right].RelativePath })
	return values, err
}

func localCatalogID(catalogKey []byte, databases []credentialDatabase) string {
	type identity struct {
		DatabaseID      string `json:"database_id"`
		CanonicalFileID string `json:"canonical_file_id"`
		Size            int64  `json:"size"`
		MTimeNS         int64  `json:"mtime_ns"`
		FirstPage       string `json:"first_page"`
		Classification  string `json:"classification"`
		ProfileID       string `json:"profile_id"`
	}
	values := make([]identity, 0, len(databases))
	for _, database := range databases {
		values = append(values, identity{
			DatabaseID: database.DatabaseID, CanonicalFileID: database.Identity,
			Size: database.Size, MTimeNS: database.MTimeNS, FirstPage: database.FirstPage,
			Classification: database.Classification, ProfileID: database.ProfileID,
		})
	}
	payload, _ := json.Marshal(struct {
		Databases []identity `json:"databases"`
		Errors    []string   `json:"errors,omitempty"`
	}{Databases: values})
	return credentialCatalogHMAC(catalogKey, string(payload))
}

func overrideByPath(credential *DatabaseCredential) map[string]CredentialOverride {
	values := map[string]CredentialOverride{}
	for _, override := range credential.Overrides {
		values[credentialPathKey(override.RelativePath)] = override
	}
	return values
}

func candidateValueForPath(values map[string]string, relative string) string {
	for _, candidate := range []string{
		relative, filepath.ToSlash(relative), filepath.Base(relative),
		strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative)), "*", "default", "key", "_key",
	} {
		if value := values[candidate]; value != "" {
			return value
		}
	}
	for name, value := range values {
		if credentialPathKey(name) == credentialPathKey(relative) || credentialPathKey(name) == credentialPathKey(filepath.Base(relative)) {
			return value
		}
	}
	return ""
}

func expandCredential(bundle CandidateBundle, dbDir string, catalogKey []byte) (CandidateBundle, CredentialCoverage, error) {
	if bundle.DatabaseCredential != nil {
		if err := validateDatabaseCredential(bundle.DatabaseCredential); err != nil {
			return CandidateBundle{}, CredentialCoverage{}, err
		}
	}
	databases, err := credentialDatabases(dbDir, catalogKey)
	if err != nil {
		return CandidateBundle{}, CredentialCoverage{}, err
	}
	result := bundle
	if result.CatalogID == "" {
		result.CatalogID = localCatalogID(catalogKey, databases)
	}
	if len(result.CatalogEntries) == 0 {
		result.CatalogEntries = make([]CatalogEntry, 0, len(databases))
		for _, database := range databases {
			result.CatalogEntries = append(result.CatalogEntries, CatalogEntry{
				DatabaseID: database.DatabaseID, RelativePath: database.RelativePath,
				CanonicalFileID: database.Identity, Size: database.Size, MTimeNS: database.MTimeNS,
				FirstPageSHA256: database.FirstPage, Classification: database.Classification,
				RequiredForKeyCoverage: database.Classification != "plaintext", ProfileID: database.ProfileID,
			})
		}
	}
	coverage := CredentialCoverage{}
	for _, database := range databases {
		if database.Classification != "plaintext" {
			coverage.DatabaseCount++
		}
	}
	if bundle.DatabaseCredential == nil {
		result.DatabaseKeys = map[string]string{}
		result.DatabaseProfiles = map[string]string{}
		for _, database := range databases {
			if database.Classification == "plaintext" {
				continue
			}
			if database.Classification != "encrypted_eligible" {
				coverage.MissingDatabases = append(coverage.MissingDatabases, filepath.ToSlash(database.RelativePath))
				continue
			}
			secret := candidateValueForPath(bundle.DatabaseKeys, database.RelativePath)
			profileID := candidateValueForPath(bundle.DatabaseProfiles, database.RelativePath)
			if secret == "" {
				coverage.MissingDatabases = append(coverage.MissingDatabases, filepath.ToSlash(database.RelativePath))
				continue
			}
			if profileID == "" {
				profileID = cryptoutil.SQLCipherDefaultProfileID
			}
			key, resolveErr := cryptoutil.ResolveCredentialFile(database.Path, secret, "raw_enc_key", profileID)
			if resolveErr != nil {
				coverage.MissingDatabases = append(coverage.MissingDatabases, filepath.ToSlash(database.RelativePath))
				continue
			}
			result.DatabaseKeys[database.RelativePath] = key
			result.DatabaseProfiles[database.RelativePath] = profileID
			coverage.MatchedDatabaseCount++
		}
		return result, coverage, nil
	}
	result.DatabaseKeys = map[string]string{}
	result.DatabaseProfiles = map[string]string{}
	overrides := overrideByPath(bundle.DatabaseCredential)
	for _, database := range databases {
		if database.Classification != "encrypted_eligible" {
			if database.Classification != "plaintext" {
				coverage.MissingDatabases = append(coverage.MissingDatabases, filepath.ToSlash(database.RelativePath))
			}
			continue
		}
		pathKey := credentialPathKey(database.RelativePath)
		matched := false
		if override, found := overrides[pathKey]; found {
			key, resolveErr := cryptoutil.ResolveCredentialFile(database.Path, override.Secret, override.Kind, override.ProfileID)
			if resolveErr == nil {
				result.DatabaseKeys[database.RelativePath] = key
				result.DatabaseProfiles[database.RelativePath] = override.ProfileID
				matched = true
			}
		}
		if !matched {
			for _, root := range bundle.DatabaseCredential.Roots {
				key, resolveErr := cryptoutil.ResolveCredentialFile(database.Path, root.Secret, root.Kind, root.ProfileID)
				if resolveErr != nil {
					continue
				}
				result.DatabaseKeys[database.RelativePath] = key
				result.DatabaseProfiles[database.RelativePath] = root.ProfileID
				matched = true
				break
			}
		}
		if matched {
			coverage.MatchedDatabaseCount++
		} else {
			coverage.MissingDatabases = append(coverage.MissingDatabases, filepath.ToSlash(database.RelativePath))
		}
	}
	return result, coverage, nil
}

// ExpandCredentialWithPrivateRoot 在不访问微信进程的前提下，为当前发现的每个加密
// 数据库派生并验证实际密钥，并使用 acquisition 私有目录中的机器密钥生成稳定、不可由
// 相对路径直接推断的 catalog/database ID。
func ExpandCredentialWithPrivateRoot(bundle CandidateBundle, dbDir, privateRoot string) (CandidateBundle, CredentialCoverage, error) {
	encoded, err := catalogKeyForPrivateRoot(privateRoot)
	if err != nil {
		return CandidateBundle{}, CredentialCoverage{}, err
	}
	key, err := hex.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return CandidateBundle{}, CredentialCoverage{}, errors.New("catalog 标识密钥无效")
	}
	defer func() {
		for index := range key {
			key[index] = 0
		}
	}()
	return expandCredential(bundle, dbDir, key)
}

// ExpandCredential 保留给不持久化 catalog 标识的库内调用；生产 CLI 使用
// ExpandCredentialWithPrivateRoot 以保证跨进程刷新时 ID 稳定。
func ExpandCredential(bundle CandidateBundle, dbDir string) (CandidateBundle, CredentialCoverage, error) {
	return ExpandCredentialWithPrivateRoot(bundle, dbDir, "")
}

func BindVerifiedCredential(credential *DatabaseCredential, accountID string, verifiedKeys map[string]string) *DatabaseCredential {
	if credential == nil {
		return nil
	}
	copyValue := *credential
	copyValue.StorageAccountID = accountID
	copyValue.Roots = append([]CredentialRoot(nil), credential.Roots...)
	copyValue.Overrides = map[string]CredentialOverride{}
	verified := map[string]bool{}
	for path := range verifiedKeys {
		verified[credentialPathKey(path)] = true
	}
	for databaseID, override := range credential.Overrides {
		if verified[credentialPathKey(override.RelativePath)] {
			copyValue.Overrides[databaseID] = override
		}
	}
	switch {
	case len(copyValue.Roots) > 0 && len(copyValue.Overrides) > 0:
		copyValue.Mode = "mixed"
	case len(copyValue.Roots) > 0:
		copyValue.Mode = "global_passphrase"
	default:
		copyValue.Mode = "per_database"
	}
	return &copyValue
}

func BindPartialVerifiedCredential(credential *DatabaseCredential, accountID string, verifiedKeys, profiles map[string]string, entries []CatalogEntry) *DatabaseCredential {
	if credential == nil || len(verifiedKeys) == 0 {
		return nil
	}
	copyValue := &DatabaseCredential{
		Mode: "per_database", CredentialEpoch: credential.CredentialEpoch,
		AccountBindingID: credential.AccountBindingID, StorageAccountID: accountID,
		Overrides: map[string]CredentialOverride{},
	}
	databaseIDs := map[string]string{}
	for _, entry := range entries {
		databaseIDs[credentialPathKey(entry.RelativePath)] = entry.DatabaseID
	}
	existing := overrideByPath(credential)
	for path, key := range verifiedKeys {
		pathKey := credentialPathKey(path)
		if override, found := existing[pathKey]; found {
			databaseID := databaseIDs[pathKey]
			if databaseID == "" {
				digest := sha256.Sum256([]byte(filepath.ToSlash(filepath.Clean(path))))
				databaseID = hex.EncodeToString(digest[:])
			}
			copyValue.Overrides[databaseID] = override
			continue
		}
		databaseID := databaseIDs[pathKey]
		if databaseID == "" {
			digest := sha256.Sum256([]byte(filepath.ToSlash(filepath.Clean(path))))
			databaseID = hex.EncodeToString(digest[:])
		}
		profileID := profiles[path]
		if profileID == "" {
			profileID = "wcdb-v4-sha512-256000-r80"
		}
		copyValue.Overrides[databaseID] = CredentialOverride{
			Kind: "raw_enc_key", ProfileID: profileID, Secret: key,
			RelativePath: filepath.Clean(path), SourceEvidence: []string{"generation_hmac_verified"},
		}
	}
	if len(copyValue.Overrides) == 0 {
		return nil
	}
	return copyValue
}
