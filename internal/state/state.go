package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
	"github.com/zanescope/v-local-cli/internal/provider"
)

const stateVersion = 2
const keyringService = "v-local-cli"

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

func AccountID(accountPath string) string {
	absolute, _ := filepath.Abs(accountPath)
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(absolute))))
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
	if value.Version != stateVersion || value.AccountID != accountID {
		return AccountState{}, errors.New("状态文件版本或账号标识不匹配")
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

func List() ([]AccountState, error) {
	root, err := Home()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "accounts"))
	if os.IsNotExist(err) {
		return []AccountState{}, nil
	}
	if err != nil {
		return nil, err
	}
	values := make([]AccountState, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		value, loadErr := Load(entry.Name())
		if loadErr == nil {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(left, right int) bool { return values[left].UpdatedAt > values[right].UpdatedAt })
	return values, nil
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
		DatabaseKeys: bundle.DatabaseKeys,
		ImageKeys:    bundle.ImageKeys,
	}
	payload, err := json.Marshal(minimal)
	if err != nil {
		return err
	}
	return keyring.Set(keyringService, accountID, string(payload))
}

func DeleteSecrets(accountID string) error {
	err := keyring.Delete(keyringService, accountID)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
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
	payload, err := keyring.Get(keyringService, accountID)
	if err != nil {
		return provider.CandidateBundle{}, err
	}
	var bundle provider.CandidateBundle
	if err := json.Unmarshal([]byte(payload), &bundle); err != nil {
		return provider.CandidateBundle{}, errors.New("系统凭据库中的密钥数据无效")
	}
	// ValidateBundle 会就地归一化候选，校验不过时不返回半归一化的结果。
	if err := provider.ValidateBundle(&bundle); err != nil {
		return provider.CandidateBundle{}, err
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
