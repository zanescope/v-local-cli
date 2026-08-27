package snapshot

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
	"github.com/zanescope/v-local-cli/internal/state"
)

type DatabaseResult struct {
	Database              string             `json:"database"`
	DatabaseID            string             `json:"database_id,omitempty"`
	Status                string             `json:"status"`
	Reason                string             `json:"reason,omitempty"`
	WAL                   cryptoutil.WALInfo `json:"wal"`
	SourceSize            int64              `json:"source_size,omitempty"`
	SourceModTime         int64              `json:"source_mtime_ns,omitempty"`
	PlainSize             int64              `json:"plain_size,omitempty"`
	PlainSHA256           string             `json:"plain_sha256,omitempty"`
	SourceFirstPageSHA256 string             `json:"source_first_page_sha256,omitempty"`
	SourceCanonicalFileID string             `json:"source_canonical_file_id,omitempty"`
	SourceClassification  string             `json:"source_classification,omitempty"`
	ProfileID             string             `json:"profile_id,omitempty"`
}

type Report struct {
	Summary             state.DatabaseSummary `json:"summary"`
	Results             []DatabaseResult      `json:"results"`
	PublicationCoverage *CoverageComparison   `json:"publication_coverage,omitempty"`
}

type BuildOptions struct {
	PreventCoverageRegression bool
	PreviousSnapshot          string
	CreatorVersion            string
	CatalogID                 string
	CatalogEntries            []CatalogEntry
	DatabaseProfiles          map[string]string
}

type CatalogEntry struct {
	DatabaseID             string
	RelativePath           string
	CanonicalFileID        string
	Size                   int64
	MTimeNS                int64
	FirstPageSHA256        string
	Classification         string
	RequiredForKeyCoverage bool
	ProfileID              string
}

type Manifest struct {
	SchemaVersion  int                   `json:"schema_version"`
	GenerationID   string                `json:"generation_id"`
	CreatedAt      string                `json:"created_at"`
	CreatorVersion string                `json:"creator_version"`
	Summary        state.DatabaseSummary `json:"summary"`
	Databases      []DatabaseResult      `json:"databases"`
	CatalogID      string                `json:"catalog_id,omitempty"`
}

type Generation struct {
	ID             string `json:"id"`
	Path           string `json:"-"`
	ManifestSHA256 string `json:"manifest_sha256"`
	CreatedAt      string `json:"created_at"`
}

const (
	maxSnapshotDatabaseFiles = 4096
	maxStableDatabaseBytes   = int64(16 * 1024 * 1024 * 1024)
	maxStableWALBytes        = int64(8 * 1024 * 1024 * 1024)
)

type CoverageComparison struct {
	PreviousDatabases  int      `json:"previous_databases"`
	CandidateDatabases int      `json:"candidate_databases"`
	MissingPrevious    int      `json:"missing_previous"`
	MissingDatabases   []string `json:"missing_databases,omitempty"`
}

type CoverageRegressionError struct {
	Comparison CoverageComparison
}

type CatalogDriftError struct {
	Reason string
}

func (err *CatalogDriftError) Error() string {
	return "数据库已偏离 Provider catalog：" + err.Reason
}

func (err *CoverageRegressionError) Error() string {
	return fmt.Sprintf("候选快照缺少旧快照中的 %d 个数据库", err.Comparison.MissingPrevious)
}

// VerifiedKeys 将通过验真的候选展开为逐库映射，避免保存未命中的全局候选。
func VerifiedKeys(keys map[string]string, report Report) map[string]string {
	verified := make(map[string]string)
	for _, result := range report.Results {
		if result.Status != "decrypted" {
			continue
		}
		if value := keyFor(filepath.FromSlash(result.Database), keys); value != "" {
			verified[result.Database] = value
		}
	}
	return verified
}

func databaseFiles(root string) ([]string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(absoluteRoot)
	unsafeRoot := false
	if err == nil {
		unsafeRoot, err = snapshotPathIsLinkOrReparse(absoluteRoot, rootInfo.Mode())
	}
	if err != nil || !rootInfo.IsDir() || unsafeRoot {
		return nil, errors.New("数据库目录不是可信的普通目录")
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil || platformPathKey(resolvedRoot) != platformPathKey(absoluteRoot) {
		return nil, errors.New("数据库目录包含不允许的链接或 reparse point")
	}
	root = absoluteRoot
	var values []string
	seenPaths := map[string]string{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		unsafeEntry, safetyErr := snapshotPathIsLinkOrReparse(path, entry.Type())
		if safetyErr != nil || unsafeEntry {
			return errors.New("数据库目录包含不允许的链接或 reparse point")
		}
		if entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".db") {
			if len(values) >= maxSnapshotDatabaseFiles {
				return errors.New("数据库数量超过安全上限")
			}
			relative, relativeErr := filepath.Rel(root, path)
			if relativeErr != nil {
				return relativeErr
			}
			pathKey := platformPathKey(relative)
			if previous, duplicate := seenPaths[pathKey]; duplicate && previous != relative {
				return errors.New("数据库路径存在大小写或 Unicode 归一化碰撞")
			}
			seenPaths[pathKey] = relative
			values = append(values, path)
		}
		return nil
	})
	sort.Strings(values)
	return values, err
}

func keyFor(relative string, keys map[string]string) string {
	variants := []string{
		relative,
		filepath.ToSlash(relative),
		filepath.Base(relative),
		strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative)),
		"*", "default", "key", "_key",
	}
	for _, candidate := range variants {
		if value := keys[candidate]; value != "" {
			return value
		}
	}
	for name, value := range keys {
		if platformPathKey(name) == platformPathKey(relative) || platformPathKey(name) == platformPathKey(filepath.Base(relative)) {
			return value
		}
	}
	return ""
}

func plaintextSQLite(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	header := make([]byte, 16)
	_, err = io.ReadFull(file, header)
	return err == nil && string(header) == "SQLite format 3\x00"
}

func regularFileInfo(path string, optional bool) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	unsafePath, safetyErr := snapshotPathIsLinkOrReparse(path, info.Mode())
	if safetyErr != nil || unsafePath || !info.Mode().IsRegular() {
		if optional {
			return nil, errors.New("可选数据库伴随文件不是普通文件")
		}
		return nil, errors.New("数据库文件不是普通文件")
	}
	return info, nil
}

func stableCopy(path, destination string, optional bool, maxBytes int64) (int64, int64, bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		before, err := regularFileInfo(path, optional)
		if optional && os.IsNotExist(err) {
			return 0, 0, false, nil
		}
		if err != nil {
			return 0, 0, false, err
		}
		sizeBefore, timeBefore := before.Size(), before.ModTime().UnixNano()
		if optional && sizeBefore == 0 {
			return 0, timeBefore, false, nil
		}
		if sizeBefore <= 0 || sizeBefore > maxBytes {
			return 0, 0, false, errors.New("数据库或 WAL 超过安全大小上限")
		}
		input, err := os.Open(path)
		if err != nil {
			return 0, 0, false, err
		}
		opened, err := input.Stat()
		if err != nil || !os.SameFile(before, opened) {
			_ = input.Close()
			return 0, 0, false, errors.New("数据库文件身份在打开时发生变化")
		}
		_ = os.Remove(destination)
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = input.Close()
			return 0, 0, false, err
		}
		written, copyErr := io.Copy(output, io.LimitReader(input, maxBytes+1))
		closeInputErr := input.Close()
		syncErr := output.Sync()
		closeOutputErr := output.Close()
		if copyErr != nil || closeInputErr != nil || syncErr != nil || closeOutputErr != nil || written > maxBytes {
			_ = os.Remove(destination)
			if copyErr != nil {
				return 0, 0, false, copyErr
			}
			return 0, 0, false, errors.New("无法稳定复制数据库或 WAL")
		}
		after, statErr := regularFileInfo(path, optional)
		if statErr == nil && os.SameFile(opened, after) && sizeBefore == after.Size() && timeBefore == after.ModTime().UnixNano() && written == after.Size() {
			return after.Size(), after.ModTime().UnixNano(), true, nil
		}
		_ = os.Remove(destination)
		time.Sleep(20 * time.Millisecond)
	}
	return 0, 0, false, errors.New("数据库或 WAL 在读取期间持续变化")
}

func catalogEntriesByPath(entries []CatalogEntry) (map[string]CatalogEntry, error) {
	result := make(map[string]CatalogEntry, len(entries))
	for _, entry := range entries {
		clean := filepath.Clean(entry.RelativePath)
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, errors.New("catalog 文件证明包含越界路径")
		}
		key := platformPathKey(clean)
		if _, duplicate := result[key]; duplicate {
			return nil, errors.New("catalog 文件证明包含重复路径")
		}
		result[key] = entry
	}
	return result, nil
}

func validateCatalogProofNow(path string, entry CatalogEntry) error {
	before, err := regularFileInfo(path, false)
	if err != nil || before.Size() != entry.Size || before.ModTime().UnixNano() != entry.MTimeNS {
		return &CatalogDriftError{Reason: "size_or_mtime_changed"}
	}
	file, err := os.Open(path)
	if err != nil {
		return &CatalogDriftError{Reason: "database_open_failed"}
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		_ = file.Close()
		return &CatalogDriftError{Reason: "canonical_file_identity_changed"}
	}
	identity, err := sourceOpenFileIdentity(file)
	if err != nil || identity != entry.CanonicalFileID {
		_ = file.Close()
		return &CatalogDriftError{Reason: "canonical_file_identity_changed"}
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, cryptoutil.SQLCipherPageSize)); err != nil {
		_ = file.Close()
		return &CatalogDriftError{Reason: "first_page_read_failed"}
	}
	after, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil || !os.SameFile(opened, after) ||
		opened.Size() != after.Size() || opened.ModTime() != after.ModTime() {
		return &CatalogDriftError{Reason: "changed_during_catalog_recheck"}
	}
	if hex.EncodeToString(hash.Sum(nil)) != entry.FirstPageSHA256 {
		return &CatalogDriftError{Reason: "first_page_changed"}
	}
	return nil
}

func validateCatalogSource(path, copiedPath, relative string, size, mtime int64, entries map[string]CatalogEntry) error {
	entry, found := entries[platformPathKey(relative)]
	if !found {
		return &CatalogDriftError{Reason: "database_not_in_catalog"}
	}
	if entry.Size != size || entry.MTimeNS != mtime {
		return &CatalogDriftError{Reason: "size_or_mtime_changed"}
	}
	current, err := regularFileInfo(path, false)
	if err != nil || current.Size() != size || current.ModTime().UnixNano() != mtime {
		return &CatalogDriftError{Reason: "changed_during_stable_copy"}
	}
	identity, err := sourceFileIdentity(path)
	if err != nil || identity != entry.CanonicalFileID {
		return &CatalogDriftError{Reason: "canonical_file_identity_changed"}
	}
	digest, err := firstPageSHA256(copiedPath)
	if err != nil || digest != entry.FirstPageSHA256 {
		return &CatalogDriftError{Reason: "first_page_changed"}
	}
	return nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func firstPageSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, cryptoutil.SQLCipherPageSize))
	if err != nil {
		return "", err
	}
	if written < 16 {
		return "", errors.New("数据库首页不足 16 字节")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func pathsOverlap(left, right string) bool {
	leftAbs, _ := filepath.Abs(left)
	rightAbs, _ := filepath.Abs(right)
	leftAbs = platformPathKey(leftAbs)
	rightAbs = platformPathKey(rightAbs)
	if leftAbs == rightAbs {
		return true
	}
	leftPrefix := leftAbs + "/"
	rightPrefix := rightAbs + "/"
	return strings.HasPrefix(rightAbs+"/", leftPrefix) || strings.HasPrefix(leftAbs+"/", rightPrefix)
}

func normalizedDatabaseSet(root string) (map[string]bool, error) {
	files, err := databaseFiles(root)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(files))
	for _, path := range files {
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return nil, relativeErr
		}
		result[platformPathKey(relative)] = true
	}
	return result, nil
}

func comparePublishedCoverage(destination string, report Report) (CoverageComparison, error) {
	previous, err := normalizedDatabaseSet(destination)
	if err != nil {
		return CoverageComparison{}, err
	}
	candidate := make(map[string]bool)
	for _, result := range report.Results {
		if result.Status == "decrypted" {
			candidate[platformPathKey(result.Database)] = true
		}
	}
	comparison := CoverageComparison{PreviousDatabases: len(previous), CandidateDatabases: len(candidate)}
	for database := range previous {
		if !candidate[database] {
			comparison.MissingPrevious++
			comparison.MissingDatabases = append(comparison.MissingDatabases, database)
		}
	}
	sort.Strings(comparison.MissingDatabases)
	return comparison, nil
}

func buildStage(source, stage string, keys map[string]string, options BuildOptions) (Report, error) {
	if pathsOverlap(source, stage) {
		return Report{}, errors.New("快照目录不能与微信数据库目录重叠")
	}
	databases, err := databaseFiles(source)
	if err != nil {
		return Report{}, err
	}
	var catalogEntries map[string]CatalogEntry
	if options.CatalogID != "" {
		if len(options.CatalogEntries) == 0 {
			return Report{}, errors.New("Provider catalog 缺少逐文件证明")
		}
		catalogEntries, err = catalogEntriesByPath(options.CatalogEntries)
		if err != nil {
			return Report{}, err
		}
		if len(catalogEntries) != len(databases) {
			return Report{}, &CatalogDriftError{Reason: "database_set_changed"}
		}
		for _, database := range databases {
			relative, relativeErr := filepath.Rel(source, database)
			if relativeErr != nil {
				return Report{}, relativeErr
			}
			entry, found := catalogEntries[platformPathKey(relative)]
			if !found {
				return Report{}, &CatalogDriftError{Reason: "database_set_changed"}
			}
			if entry.CanonicalFileID != "" && entry.FirstPageSHA256 != "" {
				if proofErr := validateCatalogProofNow(database, entry); proofErr != nil {
					return Report{}, proofErr
				}
			}
		}
	}
	report := Report{}
	report.Summary.Discovered = len(databases)
	inputs, err := os.MkdirTemp(filepath.Dir(stage), ".snapshot-input-*.tmp")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(inputs)
	for index, database := range databases {
		relative, _ := filepath.Rel(source, database)
		key := keyFor(relative, keys)
		result := DatabaseResult{Database: filepath.ToSlash(relative), Status: "skipped", Reason: "no_key"}
		if options.CatalogID != "" {
			entry := catalogEntries[platformPathKey(relative)]
			result.DatabaseID = entry.DatabaseID
			result.SourceCanonicalFileID = entry.CanonicalFileID
			result.SourceClassification = entry.Classification
		}
		if key == "" && !plaintextSQLite(database) {
			report.Summary.Skipped++
			report.Results = append(report.Results, result)
			continue
		}
		databaseCopy := filepath.Join(inputs, fmt.Sprintf("%d.db", index))
		walCopy := filepath.Join(inputs, fmt.Sprintf("%d.wal", index))
		sourceSize, sourceModTime, _, readErr := stableCopy(database, databaseCopy, false, maxStableDatabaseBytes)
		if readErr != nil {
			result.Status, result.Reason = "failed", "stable_copy_database_failed"
			report.Summary.Failed++
			report.Results = append(report.Results, result)
			continue
		}
		if options.CatalogID != "" {
			if proofErr := validateCatalogSource(database, databaseCopy, relative, sourceSize, sourceModTime, catalogEntries); proofErr != nil {
				return report, proofErr
			}
		}
		_, _, walPresent, readErr := stableCopy(database+"-wal", walCopy, true, maxStableWALBytes)
		if readErr != nil {
			result.Status, result.Reason = "failed", "stable_copy_wal_failed"
			report.Summary.Failed++
			report.Results = append(report.Results, result)
			continue
		}
		if !walPresent {
			walCopy = ""
		}
		if options.CatalogID != "" {
			if proofErr := validateCatalogSource(database, databaseCopy, relative, sourceSize, sourceModTime, catalogEntries); proofErr != nil {
				return report, proofErr
			}
		}
		target := filepath.Join(stage, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return report, err
		}
		profileID := keyFor(relative, options.DatabaseProfiles)
		walInfo, plainSize, decryptErr := cryptoutil.DecryptSQLCipherSnapshotFilesWithProfile(databaseCopy, walCopy, target, key, profileID)
		result.WAL = walInfo
		if decryptErr != nil {
			result.Status, result.Reason = "failed", "decrypt_or_wal_validation_failed"
			report.Summary.Failed++
			report.Results = append(report.Results, result)
			continue
		}
		digest, digestErr := sha256File(target)
		if digestErr != nil {
			return report, digestErr
		}
		result.Status, result.Reason = "decrypted", ""
		result.SourceSize, result.SourceModTime = sourceSize, sourceModTime
		result.PlainSize, result.PlainSHA256 = plainSize, digest
		if sourceDigest, digestErr := firstPageSHA256(databaseCopy); digestErr == nil {
			result.SourceFirstPageSHA256 = sourceDigest
		}
		result.ProfileID = profileID
		report.Summary.Decrypted++
		if walInfo.Present {
			report.Summary.WALFiles++
			report.Summary.WALFrames += walInfo.CommittedFrames
			report.Summary.WALPages += walInfo.AppliedPages
		}
		report.Results = append(report.Results, result)
	}
	if report.Summary.Decrypted == 0 {
		return report, errors.New("没有数据库通过候选密钥验真")
	}
	previous := options.PreviousSnapshot
	if previous != "" && options.PreventCoverageRegression {
		comparison, err := comparePublishedCoverage(previous, report)
		if err != nil {
			return report, fmt.Errorf("无法比较新旧快照覆盖：%w", err)
		}
		report.PublicationCoverage = &comparison
		if comparison.MissingPrevious > 0 {
			return report, &CoverageRegressionError{Comparison: comparison}
		}
	}
	return report, nil
}

// publishDirectory 发布代际目录。目标路径带随机后缀、必然不存在，因此这里的失败
// 只可能来自文件系统争用；对可重试的争用做有界退避，其余错误立即上报。
func publishDirectory(source, destination string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = os.Rename(source, destination); err == nil {
			return nil
		}
		if !transientRenameError(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	return err
}

func newGenerationID() (string, error) {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(random), nil
}

// BuildGeneration 创建不可变快照代际；调用方在状态指针提交后再清理旧代际。
func BuildGeneration(source, generationsRoot string, keys map[string]string, options BuildOptions) (Report, Generation, error) {
	if pathsOverlap(source, generationsRoot) {
		return Report{}, Generation{}, errors.New("快照目录不能与微信数据库目录重叠")
	}
	if err := os.MkdirAll(generationsRoot, 0o700); err != nil {
		return Report{}, Generation{}, err
	}
	if err := CleanupStaging(generationsRoot); err != nil {
		return Report{}, Generation{}, err
	}
	id, err := newGenerationID()
	if err != nil {
		return Report{}, Generation{}, err
	}
	stage := filepath.Join(generationsRoot, ".stage-"+id)
	if err := os.Mkdir(stage, 0o700); err != nil {
		return Report{}, Generation{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	report, err := buildStage(source, stage, keys, options)
	if err != nil {
		return report, Generation{}, err
	}
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	manifest := Manifest{
		SchemaVersion: 1, GenerationID: id, CreatedAt: createdAt,
		CreatorVersion: options.CreatorVersion, Summary: report.Summary, Databases: report.Results,
		CatalogID: options.CatalogID,
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return report, Generation{}, err
	}
	payload = append(payload, '\n')
	manifestPath := filepath.Join(stage, "manifest.json")
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		return report, Generation{}, err
	}
	final := filepath.Join(generationsRoot, id)
	if err := publishDirectory(stage, final); err != nil {
		return report, Generation{}, fmt.Errorf("无法发布快照代际：%w", err)
	}
	published = true
	digest := sha256.Sum256(payload)
	return report, Generation{
		ID: id, Path: final, ManifestSHA256: hex.EncodeToString(digest[:]), CreatedAt: createdAt,
	}, nil
}

// CleanupStaging 清除未完成的代际；账号级文件锁保证没有并发发布者。
func CleanupStaging(generationsRoot string) error {
	entries, err := os.ReadDir(generationsRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && (strings.HasPrefix(entry.Name(), ".stage-") || strings.HasPrefix(entry.Name(), ".snapshot-input-")) {
			if err := os.RemoveAll(filepath.Join(generationsRoot, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

type GarbageCollectionReport struct {
	RemovedGenerations int   `json:"removed_generations"`
	RemovedStaging     int   `json:"removed_staging"`
	ReclaimedBytes     int64 `json:"reclaimed_bytes"`
	DryRun             bool  `json:"dry_run"`
}

func directoryBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !entry.IsDir() {
			if info, infoErr := entry.Info(); infoErr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// GarbageCollect 删除未完成目录和超出保留策略的不可变代际。
func GarbageCollect(generationsRoot, current string, retainPrevious int, dryRun bool) (GarbageCollectionReport, error) {
	report := GarbageCollectionReport{DryRun: dryRun}
	entries, err := os.ReadDir(generationsRoot)
	if os.IsNotExist(err) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	currentAbsolute, _ := filepath.Abs(current)
	type candidate struct {
		path    string
		staging bool
	}
	var stages []candidate
	var generations []candidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(generationsRoot, entry.Name())
		if strings.HasPrefix(entry.Name(), ".stage-") || strings.HasPrefix(entry.Name(), ".snapshot-input-") {
			stages = append(stages, candidate{path: path, staging: true})
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		absolute, _ := filepath.Abs(path)
		if strings.EqualFold(filepath.Clean(absolute), filepath.Clean(currentAbsolute)) {
			continue
		}
		generations = append(generations, candidate{path: path})
	}
	sort.Slice(generations, func(left, right int) bool { return generations[left].path > generations[right].path })
	if retainPrevious < 0 {
		retainPrevious = 0
	}
	if len(generations) > retainPrevious {
		generations = generations[retainPrevious:]
	} else {
		generations = nil
	}
	for _, value := range append(stages, generations...) {
		report.ReclaimedBytes += directoryBytes(value.path)
		if value.staging {
			report.RemovedStaging++
		} else {
			report.RemovedGenerations++
		}
		if !dryRun {
			if err := os.RemoveAll(value.path); err != nil {
				return report, err
			}
		}
	}
	return report, nil
}

// CleanupGenerations 仅保留调用方指定的当前代际和回滚代际。
func CleanupGenerations(generationsRoot string, keep ...string) error {
	allowed := make(map[string]bool, len(keep))
	for _, value := range keep {
		if value == "" {
			continue
		}
		absolute, err := filepath.Abs(value)
		if err == nil {
			allowed[strings.ToLower(filepath.Clean(absolute))] = true
		}
	}
	entries, err := os.ReadDir(generationsRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(generationsRoot, entry.Name())
		absolute, err := filepath.Abs(path)
		if err != nil || allowed[strings.ToLower(filepath.Clean(absolute))] {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}
