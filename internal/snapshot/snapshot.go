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
	Database      string             `json:"database"`
	Status        string             `json:"status"`
	Reason        string             `json:"reason,omitempty"`
	WAL           cryptoutil.WALInfo `json:"wal"`
	SourceSize    int64              `json:"source_size,omitempty"`
	SourceModTime int64              `json:"source_mtime_ns,omitempty"`
	PlainSize     int64              `json:"plain_size,omitempty"`
	PlainSHA256   string             `json:"plain_sha256,omitempty"`
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
}

type Manifest struct {
	SchemaVersion  int                   `json:"schema_version"`
	GenerationID   string                `json:"generation_id"`
	CreatedAt      string                `json:"created_at"`
	CreatorVersion string                `json:"creator_version"`
	Summary        state.DatabaseSummary `json:"summary"`
	Databases      []DatabaseResult      `json:"databases"`
}

type Generation struct {
	ID             string `json:"id"`
	Path           string `json:"-"`
	ManifestSHA256 string `json:"manifest_sha256"`
	CreatedAt      string `json:"created_at"`
}

const (
	maxStableDatabaseBytes = int64(16 * 1024 * 1024 * 1024)
	maxStableWALBytes      = int64(8 * 1024 * 1024 * 1024)
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
	var values []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".db") {
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
		if strings.EqualFold(filepath.ToSlash(name), filepath.ToSlash(relative)) || strings.EqualFold(name, filepath.Base(relative)) {
			return value
		}
	}
	return ""
}

func fileSignature(path string) (int64, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	return info.Size(), info.ModTime().UnixNano(), nil
}

func stableCopy(path, destination string, optional bool, maxBytes int64) (int64, int64, bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		sizeBefore, timeBefore, err := fileSignature(path)
		if optional && os.IsNotExist(err) {
			return 0, 0, false, nil
		}
		if err != nil {
			return 0, 0, false, err
		}
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
		sizeAfter, timeAfter, err := fileSignature(path)
		if err == nil && sizeBefore == sizeAfter && timeBefore == timeAfter && written == sizeAfter {
			return sizeAfter, timeAfter, true, nil
		}
		_ = os.Remove(destination)
		time.Sleep(20 * time.Millisecond)
	}
	return 0, 0, false, errors.New("数据库或 WAL 在读取期间持续变化")
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

func pathsOverlap(left, right string) bool {
	leftAbs, _ := filepath.Abs(left)
	rightAbs, _ := filepath.Abs(right)
	leftAbs = filepath.Clean(leftAbs)
	rightAbs = filepath.Clean(rightAbs)
	if strings.EqualFold(leftAbs, rightAbs) {
		return true
	}
	leftPrefix := strings.ToLower(leftAbs + string(os.PathSeparator))
	rightPrefix := strings.ToLower(rightAbs + string(os.PathSeparator))
	return strings.HasPrefix(strings.ToLower(rightAbs)+string(os.PathSeparator), leftPrefix) ||
		strings.HasPrefix(strings.ToLower(leftAbs)+string(os.PathSeparator), rightPrefix)
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
		result[strings.ToLower(filepath.ToSlash(relative))] = true
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
			candidate[strings.ToLower(filepath.ToSlash(result.Database))] = true
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
		if key == "" {
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
		target := filepath.Join(stage, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return report, err
		}
		walInfo, plainSize, decryptErr := cryptoutil.DecryptSQLCipherSnapshotFiles(databaseCopy, walCopy, target, key)
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
