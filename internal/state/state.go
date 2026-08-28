package state

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
	localplatform "github.com/zanescope/v-local-cli/internal/platform"
	"github.com/zanescope/v-local-cli/internal/provider"
)

const stateVersion = 1
const keyringService = "v-local-cli"
const savedSecretsSchemaVersion = 1
const savedSecretsEncoding = "gzip+base64"
const savedSecretsChunkEncoding = "gzip+base64-chunks"
const maxSavedSecretsBytes = 8 * 1024 * 1024
const credentialBlobMaxBytes = 5 * 512
const savedSecretsChunkSize = 2000
const maxSavedSecretsChunks = 64

var ErrSavedSecretsInvalid = errors.New("系统凭据库中的密钥数据无效")

type credentialStore interface {
	Set(service, user, password string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

type systemCredentialStore struct{}

func (systemCredentialStore) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (systemCredentialStore) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (systemCredentialStore) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

var savedSecretsStore credentialStore = systemCredentialStore{}

type savedSecretsCleanupError struct {
	cause error
}

func (value *savedSecretsCleanupError) Error() string {
	return "新凭据已提交，但旧凭据分片清理未完成"
}

func (value *savedSecretsCleanupError) Unwrap() error {
	return value.cause
}

// SavedSecretsCommitted 判断错误是否发生在新 manifest 已经提交之后；调用方必须把
// 新凭据视为可读，同时明确报告旧分片仍需清理，不能降级成“完全没有写入”。
func SavedSecretsCommitted(err error) bool {
	var cleanup *savedSecretsCleanupError
	return errors.As(err, &cleanup)
}

type stateVersionMismatchError struct {
	found    int
	required int
}

func (value *stateVersionMismatchError) Error() string {
	return fmt.Sprintf("账号状态文件版本为 %d，当前要求 %d；重新运行 v-local-cli setup 重建账号状态", value.found, value.required)
}

type DatabaseSummary struct {
	Discovered int `json:"discovered"`
	Decrypted  int `json:"decrypted"`
	Skipped    int `json:"skipped"`
	Failed     int `json:"failed"`
	WALFiles   int `json:"wal_files"`
	WALFrames  int `json:"wal_frames"`
	WALPages   int `json:"wal_pages"`
}

type MediaSummary struct {
	Status         string `json:"status"`
	SamplesScanned int    `json:"samples_scanned"`
	SamplesValid   int    `json:"samples_valid"`
	AESVerified    bool   `json:"aes_verified"`
	XORVerified    bool   `json:"xor_verified"`
}

type AccountState struct {
	Version                int             `json:"version"`
	AccountID              string          `json:"account_id"`
	AccountName            string          `json:"account_name"`
	AccountPath            string          `json:"account_path"`
	SnapshotPath           string          `json:"snapshot_path"`
	GenerationID           string          `json:"generation_id,omitempty"`
	SnapshotManifestSHA256 string          `json:"snapshot_manifest_sha256,omitempty"`
	SnapshotCreatedAt      string          `json:"snapshot_created_at,omitempty"`
	UpdatedAt              string          `json:"updated_at"`
	Storage                string          `json:"storage"`
	Database               DatabaseSummary `json:"database"`
	Media                  MediaSummary    `json:"media"`
}

type savedSecretsEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	Encoding      string `json:"encoding"`
	Payload       string `json:"payload,omitempty"`
	ChunkSlot     string `json:"chunk_slot,omitempty"`
	ChunkCount    int    `json:"chunk_count,omitempty"`
	PayloadSHA256 string `json:"payload_sha256,omitempty"`
}

func Home() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("V_LOCAL_CLI_HOME")); configured != "" {
		return filepath.Abs(configured)
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("无法确定 v-local-cli 数据目录：%w", err)
	}
	return filepath.Join(root, "v-local-cli"), nil
}

// AccountID 必须与 provider 的 acquisition 目录标识使用同一套路径规范化，否则同一个
// 账号会共用一个 acquisition endpoint，却因为账号标识不同而拿不到同一把账号锁。
func AccountID(accountPath string) string {
	absolute, _ := filepath.Abs(accountPath)
	canonical := localplatform.CanonicalSystemPath(absolute)
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(canonical))))
	return hex.EncodeToString(sum[:8])
}

// DaemonControlLockID 是查询 daemon 单实例锁的固定标识。它不能经过 AccountID：后者
// 会先做 filepath.Abs，把伪路径喂进去会让锁标识随当前工作目录变化，于是从不同目录
// 启动的两个 daemon 各自拿到一把锁，单实例保护退化为只靠 endpoint 文件与 ping，留下
// 两个 daemon 同时监听、后者覆盖 endpoint 而前者变成占用端口的孤儿进程的窗口。
func DaemonControlLockID() string {
	sum := sha256.Sum256([]byte("v-local-cli/daemon-control/v1"))
	return hex.EncodeToString(sum[:8])
}

func AccountDir(accountID string) (string, error) {
	if !validAccountID(accountID) {
		return "", errors.New("账号标识无效")
	}
	root, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "accounts", accountID), nil
}

func StatePath(accountID string) (string, error) {
	directory, err := AccountDir(accountID)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "state.json"), nil
}

func GenerationsPath(accountID string) (string, error) {
	directory, err := AccountDir(accountID)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "snapshots"), nil
}

func EnsureGenerationsPath(accountID string) (string, error) {
	directory, err := AccountDir(accountID)
	if err != nil {
		return "", err
	}
	if err := securePrivateDirectory(directory); err != nil {
		return "", err
	}
	generations := filepath.Join(directory, "snapshots")
	if err := securePrivateDirectory(generations); err != nil {
		return "", err
	}
	return generations, nil
}

func EnsureExportTempPath(accountID string) (string, error) {
	directory, err := AccountDir(accountID)
	if err != nil {
		return "", err
	}
	if err := securePrivateDirectory(directory); err != nil {
		return "", err
	}
	temporary := filepath.Join(directory, "tmp")
	if err := securePrivateDirectory(temporary); err != nil {
		return "", err
	}
	return temporary, nil
}

// VoiceTranscriptPath 返回当前账号的私有语音转写缓存路径。
// 缓存位于账号私有目录中，因此 forget 会和快照、凭据一起清理它。
func VoiceTranscriptPath(accountID string) (string, error) {
	return privateCachePath(accountID, "voice-transcripts.db")
}

// OCRTextPath 返回当前账号的私有图片文字缓存路径。
// 缓存只保存已识别文字和证据元数据，不保存原始图片。
func OCRTextPath(accountID string) (string, error) {
	return privateCachePath(accountID, "ocr-texts.db")
}

// DerivedRoot 保存绑定不可变 generation 的派生索引。目录继承账号私有 ACL，
// 但不属于快照本身；索引失败不会修改已经发布的 generation。
func DerivedRoot(accountID string) (string, error) {
	directory, err := AccountDir(accountID)
	if err != nil {
		return "", err
	}
	if err := securePrivateDirectory(directory); err != nil {
		return "", err
	}
	derived := filepath.Join(directory, "derived")
	if err := securePrivateDirectory(derived); err != nil {
		return "", err
	}
	return derived, nil
}

// InboxRoot 保存各 consumer 的原子增量游标；其中不保存消息正文。
func InboxRoot(accountID string) (string, error) {
	directory, err := AccountDir(accountID)
	if err != nil {
		return "", err
	}
	if err := securePrivateDirectory(directory); err != nil {
		return "", err
	}
	inbox := filepath.Join(directory, "inbox")
	if err := securePrivateDirectory(inbox); err != nil {
		return "", err
	}
	return inbox, nil
}

// DaemonRoot 保存当前用户查询 daemon 的认证端点信息。daemon 只监听回环地址，
// 随机令牌文件依赖此目录的当前用户专属权限。
func DaemonRoot() (string, error) {
	root, err := Home()
	if err != nil {
		return "", err
	}
	if err := securePrivateDirectory(root); err != nil {
		return "", err
	}
	directory := filepath.Join(root, "daemon")
	if err := securePrivateDirectory(directory); err != nil {
		return "", err
	}
	return directory, nil
}

// AcquisitionRoot 保存密钥获取 daemon 的认证端点、不含凭据的 session resume 元数据，
// 以及只用于生成 opaque catalog 标识的机器随机密钥。目录使用与凭据状态相同的当前用户专属 ACL。
func AcquisitionRoot() (string, error) {
	root, err := Home()
	if err != nil {
		return "", err
	}
	if err := securePrivateDirectory(root); err != nil {
		return "", err
	}
	directory := filepath.Join(root, "acquisition")
	if err := securePrivateDirectory(directory); err != nil {
		return "", err
	}
	return directory, nil
}

// ValidatePrivateTarget 拒绝私有目录层级或最终目标中的符号链接、junction、
// 重解析点和特殊文件。目标尚不存在时只验证其父目录，供原子发布前使用。
func ValidatePrivateTarget(path string, allowDirectory bool) error {
	if err := validatePrivateHierarchy(filepath.Dir(path)); err != nil {
		return err
	}
	if err := validatePrivatePath(path, allowDirectory); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func privateCachePath(accountID, name string) (string, error) {
	directory, err := AccountDir(accountID)
	if err != nil {
		return "", err
	}
	if err := securePrivateDirectory(directory); err != nil {
		return "", err
	}
	if err := validatePrivateHierarchy(directory); err != nil {
		return "", err
	}
	path := filepath.Join(directory, name)
	if _, err := os.Lstat(path); err == nil {
		if err := validatePrivatePath(path, false); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return path, nil
}

func Save(value *AccountState) error {
	value.Version = stateVersion
	value.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	path, err := StatePath(value.AccountID)
	if err != nil {
		return err
	}
	if err := securePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporaryFile, err := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return err
	}
	temporary := temporaryFile.Name()
	removeTemporary := true
	defer func() {
		_ = temporaryFile.Close()
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()
	if err := temporaryFile.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporaryFile.Write(append(payload, '\n')); err != nil {
		return err
	}
	if err := temporaryFile.Sync(); err != nil {
		return err
	}
	if err := temporaryFile.Close(); err != nil {
		return err
	}
	if err := commitStateFile(path, temporary, nil); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}

func commitStateFile(path, temporary string, afterBackup func()) error {
	backup := path + ".old"
	_ = os.Remove(backup)
	movedOld := false
	if _, statErr := os.Stat(path); statErr == nil {
		if err := os.Rename(path, backup); err != nil {
			return err
		}
		movedOld = true
		if afterBackup != nil {
			afterBackup()
		}
	}
	if err := os.Rename(temporary, path); err != nil {
		if movedOld {
			if rollbackErr := os.Rename(backup, path); rollbackErr != nil {
				return fmt.Errorf("publish state failed: %w; rollback failed: %v", err, rollbackErr)
			}
		}
		return err
	}
	if movedOld {
		_ = os.Remove(backup)
	}
	return nil
}

func decodeState(path, accountID string) (AccountState, error) {
	if err := validatePrivateHierarchy(filepath.Dir(path)); err != nil {
		return AccountState{}, err
	}
	if err := validatePrivatePath(path, false); err != nil {
		return AccountState{}, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return AccountState{}, err
	}
	var value AccountState
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return AccountState{}, fmt.Errorf("状态文件无效：%w", err)
	}
	// 即使版本不匹配，也先完成账号和路径边界验证；setup 的替换基线只能接受通过这些
	// 安全检查的旧状态，绝不能让旧版本成为绕过账号绑定或目录边界的理由。
	versionMismatch := value.Version != stateVersion
	if value.AccountID != accountID {
		return AccountState{}, errors.New("账号状态文件的账号标识与所在目录不一致")
	}
	accountDirectory, err := AccountDir(accountID)
	if err != nil {
		return AccountState{}, err
	}
	relative, err := filepath.Rel(accountDirectory, value.SnapshotPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return AccountState{}, errors.New("状态文件中的快照路径越界")
	}
	if err := validatePrivatePath(value.SnapshotPath, true); err != nil && !os.IsNotExist(err) {
		return AccountState{}, err
	}
	if err := validatePrivateHierarchy(filepath.Dir(value.SnapshotPath)); err != nil {
		return AccountState{}, err
	}
	if versionMismatch {
		return value, &stateVersionMismatchError{found: value.Version, required: stateVersion}
	}
	return value, nil
}

func Load(accountID string) (AccountState, error) {
	path, err := StatePath(accountID)
	if err != nil {
		return AccountState{}, err
	}
	value, currentErr := decodeState(path, accountID)
	if currentErr == nil {
		return value, nil
	}
	backup := path + ".old"
	value, backupErr := decodeState(backup, accountID)
	if backupErr != nil {
		return AccountState{}, currentErr
	}
	return value, nil
}

// LoadReplacementBaseline 只供 setup 在原子发布新 v1 状态前读取覆盖率基线。版本不匹配
// 的状态仍不会被 List、Select 或 refresh 视为已初始化；只有完整通过 JSON、账号绑定、
// 私有路径和目录边界校验后，才可暂时提供旧快照路径。
func LoadReplacementBaseline(accountID string) (AccountState, error) {
	path, err := StatePath(accountID)
	if err != nil {
		return AccountState{}, err
	}
	current, currentErr := decodeState(path, accountID)
	if currentErr == nil {
		return current, nil
	}
	backup, backupErr := decodeState(path+".old", accountID)
	if backupErr == nil {
		return backup, nil
	}
	var currentVersion *stateVersionMismatchError
	if errors.As(currentErr, &currentVersion) {
		return current, nil
	}
	var backupVersion *stateVersionMismatchError
	if errors.As(backupErr, &backupVersion) {
		return backup, nil
	}
	return AccountState{}, currentErr
}

func List() ([]AccountState, error) {
	values, _, err := ListWithUnreadable()
	return values, err
}

// ListWithUnreadable 额外返回无法读取的账号标识。List 会静默跳过它们，但状态文件因
// 版本不符或损坏读不出来时，账号目录、快照和系统凭据其实都还在，只是当前构建读不了。
// 如果这里也沉默，doctor 就只能看到「零个已初始化账号」，并据此断言状态可读——用来
// 诊断这件事的工具反而给不出任何信号。
func ListWithUnreadable() ([]AccountState, []string, error) {
	root, err := Home()
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "accounts"))
	if os.IsNotExist(err) {
		return []AccountState{}, []string{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	values := make([]AccountState, 0, len(entries))
	unreadable := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		value, loadErr := Load(entry.Name())
		if loadErr == nil {
			values = append(values, value)
			continue
		}
		// 只把形如账号标识的目录算作不可读账号，避免把无关目录报成故障。
		if validAccountID(entry.Name()) {
			unreadable = append(unreadable, entry.Name())
		}
	}
	sort.Slice(values, func(left, right int) bool { return values[left].UpdatedAt > values[right].UpdatedAt })
	sort.Strings(unreadable)
	return values, unreadable, nil
}

func Select(selector string) (AccountState, error) {
	values, err := List()
	if err != nil {
		return AccountState{}, err
	}
	if selector == "" {
		if len(values) == 1 {
			return values[0], nil
		}
		if len(values) == 0 {
			return AccountState{}, errors.New("not_initialized")
		}
		return AccountState{}, errors.New("need_account")
	}
	var matches []AccountState
	needle := strings.ToLower(selector)
	for _, value := range values {
		if strings.EqualFold(value.AccountID, selector) || strings.EqualFold(value.AccountName, selector) {
			return value, nil
		}
		if strings.Contains(strings.ToLower(value.AccountName), needle) {
			matches = append(matches, value)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return AccountState{}, errors.New("need_account")
	}
	return AccountState{}, errors.New("not_initialized")
}

func SaveSecrets(accountID string, bundle provider.CandidateBundle) error {
	minimal := provider.CandidateBundle{
		CatalogID:          bundle.CatalogID,
		DatabaseKeys:       bundle.DatabaseKeys,
		DatabaseProfiles:   bundle.DatabaseProfiles,
		DatabaseCredential: bundle.DatabaseCredential,
		ImageKeys:          bundle.ImageKeys,
		Profiles:           bundle.Profiles,
	}
	// 结构化 credential 已能针对当前数据库重新派生并验证 key；重复保存逐库派生结果会
	// 触发 Windows Credential Manager 的 2560-byte 单条上限，也增加凭据暴露面。
	if minimal.DatabaseCredential != nil {
		minimal.DatabaseKeys = nil
		minimal.DatabaseProfiles = nil
	}
	encoded, err := encodeSavedSecrets(minimal)
	if err != nil {
		return err
	}
	oldSlot, _, _, err := currentSavedSecretsChunkMetadata(accountID)
	if err != nil {
		return err
	}
	// 新写入统一采用 manifest 与双槽分片；单条格式仅保留读取兼容。这样无论 payload
	// 变大或缩小都只会原子切换 manifest，不存在分片退回单条后的旧凭据残留窗口。
	if len(encoded) == 0 || len(encoded) > savedSecretsChunkSize*maxSavedSecretsChunks {
		return keyring.ErrSetDataTooBig
	}
	newSlot := "a"
	if oldSlot == "a" {
		newSlot = "b"
	}
	// inactive slot 可能来自上次在 manifest 提交前中断的写入；必须在复用前完整清理。
	cleanupErr := cleanupSavedSecretChunks(accountID, newSlot, 0, maxSavedSecretsChunks)
	// 没有可用 slot 元数据时，另一个槽位也必须清理。主 manifest 可能已被 forget
	// 删除，而上次 best-effort 清理失败的孤儿分片仍留在任一槽位。
	if oldSlot == "" {
		otherSlot := "b"
		if newSlot == "b" {
			otherSlot = "a"
		}
		cleanupErr = errors.Join(cleanupErr, cleanupSavedSecretChunks(accountID, otherSlot, 0, maxSavedSecretsChunks))
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	chunkCount := (len(encoded) + savedSecretsChunkSize - 1) / savedSecretsChunkSize
	written := 0
	for index := 0; index < chunkCount; index++ {
		end := (index + 1) * savedSecretsChunkSize
		if end > len(encoded) {
			end = len(encoded)
		}
		if err := savedSecretsStore.Set(keyringService, savedSecretsChunkAccount(accountID, newSlot, index), encoded[index*savedSecretsChunkSize:end]); err != nil {
			return errors.Join(err, cleanupSavedSecretChunks(accountID, newSlot, 0, written))
		}
		written++
	}
	digest := sha256.Sum256([]byte(encoded))
	manifest, err := json.Marshal(savedSecretsEnvelope{
		SchemaVersion: savedSecretsSchemaVersion, Encoding: savedSecretsChunkEncoding,
		ChunkSlot: newSlot, ChunkCount: chunkCount, PayloadSHA256: hex.EncodeToString(digest[:]),
	})
	if err != nil {
		return errors.Join(err, cleanupSavedSecretChunks(accountID, newSlot, 0, written))
	}
	if len(manifest) > credentialBlobMaxBytes {
		clearSecretBytes(manifest)
		return errors.Join(keyring.ErrSetDataTooBig, cleanupSavedSecretChunks(accountID, newSlot, 0, written))
	}
	if err := savedSecretsStore.Set(keyringService, accountID, string(manifest)); err != nil {
		clearSecretBytes(manifest)
		return errors.Join(err, cleanupSavedSecretChunks(accountID, newSlot, 0, written))
	}
	clearSecretBytes(manifest)
	if oldSlot != "" && oldSlot != newSlot {
		// manifest 只描述当前有效分片数，旧槽位还可能带有更早失败写入留下的尾部分片。
		if err := cleanupSavedSecretChunks(accountID, oldSlot, 0, maxSavedSecretsChunks); err != nil {
			return &savedSecretsCleanupError{cause: err}
		}
	}
	return nil
}

func clearSecretBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func encodeSavedSecrets(bundle provider.CandidateBundle) (string, error) {
	plain, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}
	defer clearSecretBytes(plain)
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := writer.Write(plain); err != nil {
		_ = writer.Close()
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	compressedBytes := compressed.Bytes()
	defer clearSecretBytes(compressedBytes)
	return base64.StdEncoding.EncodeToString(compressedBytes), nil
}

func marshalSavedSecrets(bundle provider.CandidateBundle) ([]byte, error) {
	encoded, err := encodeSavedSecrets(bundle)
	if err != nil {
		return nil, err
	}
	return json.Marshal(savedSecretsEnvelope{
		SchemaVersion: savedSecretsSchemaVersion, Encoding: savedSecretsEncoding, Payload: encoded,
	})
}

func decodeStrictJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("JSON 包含多余数据")
	}
	return nil
}

func savedSecretsChunkAccount(accountID, slot string, index int) string {
	return fmt.Sprintf("%s.v1.%s.%02d", accountID, slot, index)
}

func cleanupSavedSecretChunks(accountID, slot string, start, end int) error {
	if slot != "a" && slot != "b" {
		return nil
	}
	if start < 0 {
		start = 0
	}
	if end > maxSavedSecretsChunks {
		end = maxSavedSecretsChunks
	}
	var cleanupErrors []error
	for index := start; index < end; index++ {
		err := savedSecretsStore.Delete(keyringService, savedSecretsChunkAccount(accountID, slot, index))
		if err != nil && !errors.Is(err, keyring.ErrNotFound) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func currentSavedSecretsChunkMetadata(accountID string) (string, int, bool, error) {
	payload, err := savedSecretsStore.Get(keyringService, accountID)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	encoded := []byte(payload)
	defer clearSecretBytes(encoded)
	var envelope savedSecretsEnvelope
	if decodeStrictJSON(encoded, &envelope) != nil || envelope.SchemaVersion != savedSecretsSchemaVersion ||
		envelope.Encoding != savedSecretsChunkEncoding || (envelope.ChunkSlot != "a" && envelope.ChunkSlot != "b") ||
		envelope.ChunkCount < 1 || envelope.ChunkCount > maxSavedSecretsChunks {
		return "", 0, true, nil
	}
	return envelope.ChunkSlot, envelope.ChunkCount, true, nil
}

func decodeSavedSecretsEncoding(encoded string) (provider.CandidateBundle, error) {
	if len(encoded) == 0 || len(encoded) > savedSecretsChunkSize*maxSavedSecretsChunks {
		return provider.CandidateBundle{}, ErrSavedSecretsInvalid
	}
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return provider.CandidateBundle{}, ErrSavedSecretsInvalid
	}
	defer clearSecretBytes(compressed)
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return provider.CandidateBundle{}, ErrSavedSecretsInvalid
	}
	decoded, readErr := io.ReadAll(io.LimitReader(reader, maxSavedSecretsBytes+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || len(decoded) > maxSavedSecretsBytes {
		clearSecretBytes(decoded)
		return provider.CandidateBundle{}, ErrSavedSecretsInvalid
	}
	defer clearSecretBytes(decoded)
	var bundle provider.CandidateBundle
	if decodeStrictJSON(decoded, &bundle) != nil {
		return provider.CandidateBundle{}, ErrSavedSecretsInvalid
	}
	return bundle, nil
}

func decodeSavedSecretsForAccount(accountID string, payload []byte) (provider.CandidateBundle, error) {
	var marker struct {
		SchemaVersion *int `json:"schema_version"`
	}
	if err := json.Unmarshal(payload, &marker); err != nil {
		return provider.CandidateBundle{}, ErrSavedSecretsInvalid
	}
	if marker.SchemaVersion != nil {
		var envelope savedSecretsEnvelope
		if decodeStrictJSON(payload, &envelope) != nil || envelope.SchemaVersion != savedSecretsSchemaVersion {
			return provider.CandidateBundle{}, ErrSavedSecretsInvalid
		}
		switch envelope.Encoding {
		case savedSecretsEncoding:
			if envelope.Payload == "" || envelope.ChunkSlot != "" || envelope.ChunkCount != 0 || envelope.PayloadSHA256 != "" {
				return provider.CandidateBundle{}, ErrSavedSecretsInvalid
			}
			return decodeSavedSecretsEncoding(envelope.Payload)
		case savedSecretsChunkEncoding:
			if accountID == "" || envelope.Payload != "" || (envelope.ChunkSlot != "a" && envelope.ChunkSlot != "b") ||
				envelope.ChunkCount < 1 || envelope.ChunkCount > maxSavedSecretsChunks || len(envelope.PayloadSHA256) != 64 {
				return provider.CandidateBundle{}, ErrSavedSecretsInvalid
			}
			encoded := make([]byte, 0, envelope.ChunkCount*savedSecretsChunkSize)
			for index := 0; index < envelope.ChunkCount; index++ {
				chunk, err := savedSecretsStore.Get(keyringService, savedSecretsChunkAccount(accountID, envelope.ChunkSlot, index))
				if err != nil || len(chunk) == 0 || len(chunk) > savedSecretsChunkSize {
					clearSecretBytes(encoded)
					return provider.CandidateBundle{}, ErrSavedSecretsInvalid
				}
				encoded = append(encoded, chunk...)
			}
			defer clearSecretBytes(encoded)
			digest := sha256.Sum256(encoded)
			actual := hex.EncodeToString(digest[:])
			if subtle.ConstantTimeCompare([]byte(actual), []byte(strings.ToLower(envelope.PayloadSHA256))) != 1 {
				return provider.CandidateBundle{}, ErrSavedSecretsInvalid
			}
			return decodeSavedSecretsEncoding(string(encoded))
		default:
			return provider.CandidateBundle{}, ErrSavedSecretsInvalid
		}
	}
	var bundle provider.CandidateBundle
	if decodeStrictJSON(payload, &bundle) != nil {
		return provider.CandidateBundle{}, ErrSavedSecretsInvalid
	}
	return bundle, nil
}

func decodeSavedSecrets(payload []byte) (provider.CandidateBundle, error) {
	return decodeSavedSecretsForAccount("", payload)
}

func DeleteSecrets(accountID string) error {
	var deleteErrors []error
	if err := savedSecretsStore.Delete(keyringService, accountID); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		deleteErrors = append(deleteErrors, err)
	}
	for _, slot := range []string{"a", "b"} {
		for index := 0; index < maxSavedSecretsChunks; index++ {
			err := savedSecretsStore.Delete(keyringService, savedSecretsChunkAccount(accountID, slot, index))
			if err != nil && !errors.Is(err, keyring.ErrNotFound) {
				deleteErrors = append(deleteErrors, err)
			}
		}
	}
	return errors.Join(deleteErrors...)
}

func DeleteAccountData(accountID string) error {
	directory, err := AccountDir(accountID)
	if err != nil {
		return err
	}
	root, err := Home()
	if err != nil {
		return err
	}
	accountsRoot := filepath.Join(root, "accounts")
	relative, err := filepath.Rel(accountsRoot, directory)
	if err != nil || relative != accountID || filepath.Base(directory) != accountID {
		return errors.New("拒绝删除越界的账号目录")
	}
	if err := validatePrivatePath(directory, true); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := validatePrivateHierarchy(directory); err != nil {
		return err
	}
	deleting := filepath.Join(accountsRoot, ".deleting-"+accountID+"-"+fmt.Sprint(time.Now().UnixNano()))
	if err := os.Rename(directory, deleting); err != nil {
		return err
	}
	return os.RemoveAll(deleting)
}

func LoadSecrets(accountID string) (provider.CandidateBundle, error) {
	payload, err := savedSecretsStore.Get(keyringService, accountID)
	if err != nil {
		return provider.CandidateBundle{}, err
	}
	encoded := []byte(payload)
	defer clearSecretBytes(encoded)
	bundle, err := decodeSavedSecretsForAccount(accountID, encoded)
	if err != nil {
		return provider.CandidateBundle{}, err
	}
	// ValidateBundle 会就地归一化候选，校验不过时不返回半归一化的结果。
	if err := provider.ValidateBundle(&bundle); err != nil {
		return provider.CandidateBundle{}, ErrSavedSecretsInvalid
	}
	if bundle.DatabaseCredential != nil && bundle.DatabaseCredential.StorageAccountID != accountID {
		return provider.CandidateBundle{}, ErrSavedSecretsInvalid
	}
	return bundle, nil
}

func LoadSecretsOptional(accountID string) (provider.CandidateBundle, bool, error) {
	bundle, err := LoadSecrets(accountID)
	if errors.Is(err, keyring.ErrNotFound) {
		return provider.CandidateBundle{}, false, nil
	}
	if err != nil {
		return provider.CandidateBundle{}, false, err
	}
	return bundle, true, nil
}
