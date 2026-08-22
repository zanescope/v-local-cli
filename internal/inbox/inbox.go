package inbox

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zanescope/v-local-cli/internal/messageindex"
	"github.com/zanescope/v-local-cli/internal/state"
)

const SchemaVersion = 1

var consumerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type PendingBatch struct {
	ID        string                `json:"id"`
	Limit     int                   `json:"limit"`
	Next      messageindex.Position `json:"next_position"`
	HasMore   bool                  `json:"has_more"`
	CreatedAt string                `json:"created_at"`
}

type Cursor struct {
	SchemaVersion                int                   `json:"schema_version"`
	AccountID                    string                `json:"account_id"`
	Consumer                     string                `json:"consumer"`
	BaseGeneration               string                `json:"base_generation,omitempty"`
	BaseSnapshotManifestSHA256   string                `json:"base_snapshot_manifest_sha256,omitempty"`
	TargetGeneration             string                `json:"target_generation,omitempty"`
	TargetSnapshotManifestSHA256 string                `json:"target_snapshot_manifest_sha256,omitempty"`
	Position                     messageindex.Position `json:"position"`
	Pending                      *PendingBatch         `json:"pending,omitempty"`
	CreatedAt                    string                `json:"created_at"`
	UpdatedAt                    string                `json:"updated_at"`
}

type PollResult struct {
	Consumer                     string                `json:"consumer"`
	BaseGeneration               string                `json:"base_generation,omitempty"`
	BaseSnapshotManifestSHA256   string                `json:"base_snapshot_manifest_sha256,omitempty"`
	TargetGeneration             string                `json:"target_generation,omitempty"`
	TargetSnapshotManifestSHA256 string                `json:"target_snapshot_manifest_sha256,omitempty"`
	BatchID                      string                `json:"batch_id,omitempty"`
	AckRequired                  bool                  `json:"ack_required"`
	Items                        []messageindex.Change `json:"items"`
	Count                        int                   `json:"count"`
	HasMore                      bool                  `json:"has_more"`
	Replayed                     bool                  `json:"replayed_pending_batch"`
}

type IndexUnavailableError struct {
	Generation string
	Reason     string
	Err        error
}

func (err *IndexUnavailableError) Error() string {
	if err.Err != nil {
		return fmt.Sprintf("generation %s 的完整消息索引不可用：%v", err.Generation, err.Err)
	}
	return fmt.Sprintf("generation %s 的完整消息索引不可用：%s", err.Generation, err.Reason)
}

func (err *IndexUnavailableError) Unwrap() error { return err.Err }

func validateConsumer(value string) error {
	if !consumerPattern.MatchString(value) {
		return errors.New("consumer 必须为 1-64 位字母、数字、点、下划线或连字符，并以字母或数字开头")
	}
	return nil
}

func cursorPath(accountID, consumer string) (string, error) {
	if err := validateConsumer(consumer); err != nil {
		return "", err
	}
	root, err := state.InboxRoot(accountID)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, consumer+".json"), nil
}

func decodeCursor(path, accountID, consumer string) (Cursor, error) {
	if err := state.ValidatePrivateTarget(path, false); err != nil {
		return Cursor{}, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return Cursor{}, err
	}
	var cursor Cursor
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return Cursor{}, fmt.Errorf("增量游标无效：%w", err)
	}
	if cursor.SchemaVersion != SchemaVersion || cursor.AccountID != accountID || cursor.Consumer != consumer {
		return Cursor{}, errors.New("增量游标版本、账号或 consumer 不匹配")
	}
	if (cursor.BaseGeneration == "") != (cursor.BaseSnapshotManifestSHA256 == "") ||
		(cursor.TargetGeneration == "") != (cursor.TargetSnapshotManifestSHA256 == "") {
		return Cursor{}, errors.New("增量游标的 generation 与 snapshot manifest 绑定不完整")
	}
	return cursor, nil
}

func load(accountID, consumer string) (Cursor, error) {
	path, err := cursorPath(accountID, consumer)
	if err != nil {
		return Cursor{}, err
	}
	cursor, currentErr := decodeCursor(path, accountID, consumer)
	if currentErr == nil {
		return cursor, nil
	}
	// Windows 不能用 rename 原子替换已有文件。与 account state 相同，若进程
	// 在旧文件改名后中断，就从同版本 .old 只读恢复，绝不创建一个新游标跳过进度。
	cursor, backupErr := decodeCursor(path+".old", accountID, consumer)
	if backupErr == nil {
		return cursor, nil
	}
	return Cursor{}, currentErr
}

func save(cursor Cursor) error {
	cursor.SchemaVersion = SchemaVersion
	cursor.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	path, err := cursorPath(cursor.AccountID, cursor.Consumer)
	if err != nil {
		return err
	}
	if err := state.ValidatePrivateTarget(path, false); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(cursor, "", "  ")
	if err != nil {
		return err
	}
	temporaryFile, err := os.CreateTemp(filepath.Dir(path), ".cursor-*.tmp")
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
	backup := path + ".old"
	if _, primaryErr := os.Lstat(path); os.IsNotExist(primaryErr) {
		if _, backupErr := os.Lstat(backup); backupErr == nil {
			if err := state.ValidatePrivateTarget(backup, false); err != nil {
				return err
			}
			if err := os.Rename(backup, path); err != nil {
				return err
			}
		} else if !os.IsNotExist(backupErr) {
			return backupErr
		}
	} else if primaryErr != nil {
		return primaryErr
	}
	_ = os.Remove(backup)
	moved := false
	if _, err := os.Lstat(path); err == nil {
		if err := os.Rename(path, backup); err != nil {
			return err
		}
		moved = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		if moved {
			_ = os.Rename(backup, path)
		}
		return err
	}
	removeTemporary = false
	if moved {
		_ = os.Remove(backup)
	}
	return nil
}

func randomBatchID() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "batch-" + hex.EncodeToString(value), nil
}

func withLock(accountID string, operation func() error) error {
	lock, err := state.AcquireAccountLock(accountID)
	if err != nil {
		return err
	}
	defer lock.Release()
	return operation()
}

func createUnlocked(account state.AccountState, consumer, start string) (Cursor, error) {
	if account.GenerationID == "" || account.SnapshotManifestSHA256 == "" {
		return Cursor{}, errors.New("当前账号状态缺少 generation 或 snapshot manifest 绑定")
	}
	path, err := cursorPath(account.AccountID, consumer)
	if err != nil {
		return Cursor{}, err
	}
	if _, err := os.Lstat(path); err == nil {
		return Cursor{}, errors.New("consumer 已经存在")
	} else if !os.IsNotExist(err) {
		return Cursor{}, err
	}
	if _, err := os.Lstat(path + ".old"); err == nil {
		return Cursor{}, errors.New("consumer 存在待恢复的原子备份")
	} else if !os.IsNotExist(err) {
		return Cursor{}, err
	}
	if start != "now" && start != "beginning" {
		return Cursor{}, errors.New("start 只能为 now 或 beginning")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	created := Cursor{
		SchemaVersion: SchemaVersion, AccountID: account.AccountID, Consumer: consumer,
		CreatedAt: now, UpdatedAt: now,
	}
	if start == "now" {
		created.BaseGeneration = account.GenerationID
		created.BaseSnapshotManifestSHA256 = account.SnapshotManifestSHA256
	}
	if err := save(created); err != nil {
		return Cursor{}, err
	}
	return created, nil
}

func Create(account state.AccountState, consumer, start string) (Cursor, error) {
	var created Cursor
	err := withLock(account.AccountID, func() error {
		var err error
		created, err = createUnlocked(account, consumer, start)
		return err
	})
	return created, err
}

func Get(accountID, consumer string) (Cursor, error) {
	return load(accountID, consumer)
}

func ensureIndexPath(accountID, generationID, expectedSnapshotSHA256 string) (string, error) {
	if generationID == "" {
		return "", nil
	}
	var status messageindex.Status
	var err error
	if expectedSnapshotSHA256 != "" {
		status, err = messageindex.Inspect(state.AccountState{
			AccountID: accountID, GenerationID: generationID, SnapshotManifestSHA256: expectedSnapshotSHA256,
		})
	} else {
		status, err = messageindex.InspectGeneration(accountID, generationID)
	}
	if err != nil {
		return "", &IndexUnavailableError{Generation: generationID, Err: err}
	}
	if !status.Valid || status.Manifest == nil || !status.Manifest.Coverage.Complete {
		reason := status.Reason
		if reason == "" && status.Manifest != nil && !status.Manifest.Coverage.Complete {
			reason = "coverage_incomplete"
		}
		return "", &IndexUnavailableError{Generation: generationID, Reason: reason}
	}
	path, err := messageindex.DatabasePath(accountID, generationID)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", &IndexUnavailableError{Generation: generationID, Err: err}
	}
	return path, nil
}

func pollUnlocked(account state.AccountState, consumer string, limit int) (PollResult, error) {
	output := PollResult{}
	if account.GenerationID == "" || account.SnapshotManifestSHA256 == "" {
		return output, errors.New("当前账号状态缺少 generation 或 snapshot manifest 绑定")
	}
	cursor, err := load(account.AccountID, consumer)
	if err != nil {
		return output, err
	}
	replayed := cursor.Pending != nil
	if cursor.TargetGeneration == "" {
		if cursor.BaseGeneration == account.GenerationID {
			if cursor.BaseSnapshotManifestSHA256 != account.SnapshotManifestSHA256 {
				return output, errors.New("增量游标 base generation 的 snapshot manifest 绑定不匹配")
			}
			output = PollResult{
				Consumer: consumer, BaseGeneration: cursor.BaseGeneration, BaseSnapshotManifestSHA256: cursor.BaseSnapshotManifestSHA256,
				Items: []messageindex.Change{},
			}
			return output, nil
		}
		cursor.TargetGeneration = account.GenerationID
		cursor.TargetSnapshotManifestSHA256 = account.SnapshotManifestSHA256
		cursor.Position = messageindex.Position{}
	}
	effectiveLimit := limit
	if cursor.Pending != nil {
		effectiveLimit = cursor.Pending.Limit
	}
	currentPath, err := ensureIndexPath(account.AccountID, cursor.TargetGeneration, cursor.TargetSnapshotManifestSHA256)
	if err != nil {
		return output, err
	}
	basePath, err := ensureIndexPath(account.AccountID, cursor.BaseGeneration, cursor.BaseSnapshotManifestSHA256)
	if err != nil {
		return output, err
	}
	diff, err := messageindex.Diff(currentPath, basePath, cursor.Position, effectiveLimit)
	if err != nil {
		return output, err
	}
	if len(diff.Items) == 0 {
		if cursor.Pending != nil {
			return output, errors.New("待确认批次无法从 immutable 索引中重放")
		}
		cursor.BaseGeneration = cursor.TargetGeneration
		cursor.BaseSnapshotManifestSHA256 = cursor.TargetSnapshotManifestSHA256
		cursor.TargetGeneration = ""
		cursor.TargetSnapshotManifestSHA256 = ""
		cursor.Position = messageindex.Position{}
		if err := save(cursor); err != nil {
			return output, err
		}
		output = PollResult{
			Consumer: consumer, BaseGeneration: cursor.BaseGeneration, BaseSnapshotManifestSHA256: cursor.BaseSnapshotManifestSHA256,
			Items: []messageindex.Change{},
		}
		return output, nil
	}
	if cursor.Pending == nil {
		batchID, err := randomBatchID()
		if err != nil {
			return output, err
		}
		cursor.Pending = &PendingBatch{
			ID: batchID, Limit: effectiveLimit, Next: diff.Next, HasMore: diff.HasMore,
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		if err := save(cursor); err != nil {
			return output, err
		}
	} else if cursor.Pending.Next != diff.Next || cursor.Pending.HasMore != diff.HasMore {
		return output, errors.New("待确认批次与 immutable 索引不一致")
	}
	output = PollResult{
		Consumer: consumer, BaseGeneration: cursor.BaseGeneration, BaseSnapshotManifestSHA256: cursor.BaseSnapshotManifestSHA256,
		TargetGeneration: cursor.TargetGeneration, TargetSnapshotManifestSHA256: cursor.TargetSnapshotManifestSHA256,
		BatchID: cursor.Pending.ID, AckRequired: true, Items: diff.Items, Count: len(diff.Items),
		HasMore: diff.HasMore, Replayed: replayed,
	}
	return output, nil
}

func Poll(account state.AccountState, consumer string, limit int) (PollResult, error) {
	var output PollResult
	err := withLock(account.AccountID, func() error {
		var err error
		output, err = pollUnlocked(account, consumer, limit)
		return err
	})
	return output, err
}

// PollOrCreate 在同一账号锁内重新读取已提交 state、创建缺失 consumer 并 poll，
// 避免 refresh 在 create 与 poll 之间切换 generation 或清理尚未固定的基线索引。
func PollOrCreate(accountID, consumer, start string, limit int) (PollResult, state.AccountState, string, error) {
	var output PollResult
	var current state.AccountState
	stage := "state"
	err := withLock(accountID, func() error {
		var err error
		current, err = state.Load(accountID)
		if err != nil {
			return err
		}
		if _, err = load(accountID, consumer); os.IsNotExist(err) {
			stage = "create"
			if _, err = createUnlocked(current, consumer, start); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		stage = "poll"
		output, err = pollUnlocked(current, consumer, limit)
		return err
	})
	return output, current, stage, err
}

func Ack(accountID, consumer, batchID string) (Cursor, error) {
	var result Cursor
	err := withLock(accountID, func() error {
		cursor, err := load(accountID, consumer)
		if err != nil {
			return err
		}
		if cursor.Pending == nil {
			return errors.New("当前没有待确认批次")
		}
		if cursor.Pending.ID != batchID {
			return errors.New("batch_id 与待确认批次不匹配")
		}
		cursor.Position = cursor.Pending.Next
		hasMore := cursor.Pending.HasMore
		cursor.Pending = nil
		if !hasMore {
			cursor.BaseGeneration = cursor.TargetGeneration
			cursor.BaseSnapshotManifestSHA256 = cursor.TargetSnapshotManifestSHA256
			cursor.TargetGeneration = ""
			cursor.TargetSnapshotManifestSHA256 = ""
			cursor.Position = messageindex.Position{}
		}
		if err := save(cursor); err != nil {
			return err
		}
		result = cursor
		return nil
	})
	return result, err
}

func Delete(accountID, consumer string) error {
	return withLock(accountID, func() error {
		path, err := cursorPath(accountID, consumer)
		if err != nil {
			return err
		}
		if _, err := load(accountID, consumer); err != nil {
			return err
		}
		primaryErr := os.Remove(path)
		backupErr := os.Remove(path + ".old")
		if primaryErr != nil && !os.IsNotExist(primaryErr) {
			return primaryErr
		}
		if backupErr != nil && !os.IsNotExist(backupErr) {
			return backupErr
		}
		return nil
	})
}

// PinnedGenerations 返回仍被 consumer 作为 base/target 使用的派生索引代际。
// GC 可以删除对应原始快照，但不得删除这些索引，否则尚未 ack 的批次无法重放。
func PinnedGenerations(accountID string) (map[string][]string, error) {
	root, err := state.InboxRoot(accountID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	result := map[string][]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		consumer := strings.TrimSuffix(entry.Name(), ".json")
		cursor, loadErr := load(accountID, consumer)
		if loadErr != nil {
			continue
		}
		for _, generation := range []string{cursor.BaseGeneration, cursor.TargetGeneration} {
			if generation == "" {
				continue
			}
			reason := "inbox:" + consumer
			found := false
			for _, existing := range result[generation] {
				if existing == reason {
					found = true
				}
			}
			if !found {
				result[generation] = append(result[generation], reason)
			}
		}
	}
	return result, nil
}
