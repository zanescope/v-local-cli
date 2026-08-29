package app

import (
	"context"
	"crypto/md5"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zanescope/v-local-cli/internal/state"
	"github.com/zanescope/v-local-cli/internal/store"
)

const chatImageRecoveryConsentSchemaVersion = 1
const chatImageRecoveryConsentTTL = 5 * time.Minute
const chatImageRecoveryConsentScope = "single_account_message_image_candidate_attempt"
const maxChatImageRecoveryConsentPruneEntries = 256
const maxChatImageRecoveryPlainBytes = 64 * 1024 * 1024

var chatImageRecoveryNow = time.Now
var inspectChatImageRemoteRecovery = store.InspectChatImageRemoteRecovery
var recoverChatImageRemote = store.RecoverChatImageRemote
var removeChatImageRecoveryTemporary = os.Remove

type chatImageRecoveryConsentRecord struct {
	SchemaVersion          int    `json:"schema_version"`
	ChallengeID            string `json:"challenge_id"`
	Scope                  string `json:"scope"`
	AccountID              string `json:"account_id"`
	EvidenceID             string `json:"evidence_id"`
	GenerationID           string `json:"generation_id"`
	SnapshotManifestSHA256 string `json:"snapshot_manifest_sha256"`
	MessageBindingSHA256   string `json:"message_binding_sha256"`
	DescriptorSHA256       string `json:"descriptor_sha256"`
	CandidateTier          string `json:"candidate_tier"`
	LocalQualityTier       string `json:"local_quality_tier,omitempty"`
	OutputPathSHA256       string `json:"output_path_sha256"`
	ObservedAt             string `json:"observed_at"`
	IssuedAt               string `json:"issued_at"`
	ExpiresAt              string `json:"expires_at"`
}

type chatImageRecoveryConsentError struct {
	kind     string
	consumed bool
}

func (err *chatImageRecoveryConsentError) Error() string { return err.kind }

type chatImageRecoveryPublishError struct {
	kind            string
	outputCommitted bool
	cause           error
}

func (err *chatImageRecoveryPublishError) Error() string { return err.kind }
func (err *chatImageRecoveryPublishError) Unwrap() error { return err.cause }

func chatImageRecoveryErrorDetails(values map[string]any) map[string]any {
	if values == nil {
		values = map[string]any{}
	}
	values["source_original_quality_status"] = "unknown"
	return values
}

func chatImageRecoveryTemporalErrorDetails(values map[string]any, observedAt string) map[string]any {
	values = chatImageRecoveryErrorDetails(values)
	if strings.TrimSpace(observedAt) != "" {
		values["observed_at"] = observedAt
	}
	values["retrieved_at"] = nil
	return values
}

func validChatImageRecoveryChallengeID(value string) bool {
	if len(value) != 32 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16
}

func validChatImageRecoverySHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func newChatImageRecoveryChallengeID() (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(cryptorand.Reader, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func chatImageRecoveryOutputSHA256(path string) string {
	absolute, _ := filepath.Abs(path)
	canonical := filepath.Clean(absolute)
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

func chatImageRecoveryObservedAt(value state.AccountState) string {
	if strings.TrimSpace(value.SnapshotCreatedAt) != "" {
		return value.SnapshotCreatedAt
	}
	return value.UpdatedAt
}

func writeChatImageRecoveryConsent(path string, record chatImageRecoveryConsentRecord) (err error) {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			if cleanupErr := os.Remove(path); err == nil && cleanupErr != nil && !os.IsNotExist(cleanupErr) {
				err = cleanupErr
			}
		}
	}()
	if err = file.Chmod(0o600); err != nil {
		return err
	}
	if _, err = file.Write(payload); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	remove = false
	return nil
}

// pruneExpiredChatImageRecoveryConsents bounds persistent challenge state
// without trusting file timestamps. Only strictly decoded records whose
// filename binding and embedded expiry both match are eligible for deletion.
// The caller holds the account transaction lock; the scan is capped so a
// damaged directory cannot turn one preflight into an unbounded operation.
func pruneExpiredChatImageRecoveryConsents(directory string, now time.Time) error {
	opened, err := os.Open(directory)
	if err != nil {
		return err
	}
	entries, readErr := opened.ReadDir(maxChatImageRecoveryConsentPruneEntries)
	closeErr := opened.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	for _, entry := range entries {
		name := entry.Name()
		suffix := ""
		switch {
		case strings.HasSuffix(name, ".pending.json"):
			suffix = ".pending.json"
		case strings.HasSuffix(name, ".used.json"):
			suffix = ".used.json"
		default:
			continue
		}
		challengeID := strings.TrimSuffix(name, suffix)
		if !validChatImageRecoveryChallengeID(challengeID) {
			continue
		}
		path := filepath.Join(directory, name)
		record, decodeErr := decodeChatImageRecoveryConsent(path)
		if decodeErr != nil || record.ChallengeID != challengeID {
			continue
		}
		expiresAt, parseErr := time.Parse(time.RFC3339Nano, record.ExpiresAt)
		if parseErr != nil || expiresAt.After(now) {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func issueChatImageRecoveryConsent(
	value state.AccountState,
	inspection store.ChatImageRemoteRecoveryInspection,
	output string,
) (chatImageRecoveryConsentRecord, error) {
	observedAt := chatImageRecoveryObservedAt(value)
	if _, err := time.Parse(time.RFC3339Nano, observedAt); err != nil {
		return chatImageRecoveryConsentRecord{}, errors.New("恢复授权快照时间无效")
	}
	if value.AccountID == "" || value.GenerationID == "" || !validChatImageRecoverySHA256(value.SnapshotManifestSHA256) ||
		inspection.EvidenceID == "" || !validChatImageRecoverySHA256(inspection.MessageBindingSHA256) ||
		!validChatImageRecoverySHA256(inspection.CandidateDescriptorSHA256) || inspection.CandidateTier == "" || output == "" {
		return chatImageRecoveryConsentRecord{}, errors.New("恢复授权绑定信息不完整")
	}
	directory, err := state.EnsureRecoveryConsentPath(value.AccountID)
	if err != nil {
		return chatImageRecoveryConsentRecord{}, err
	}
	issuedAt := chatImageRecoveryNow().UTC()
	if err := pruneExpiredChatImageRecoveryConsents(directory, issuedAt); err != nil {
		return chatImageRecoveryConsentRecord{}, err
	}
	for attempt := 0; attempt < 4; attempt++ {
		challengeID, err := newChatImageRecoveryChallengeID()
		if err != nil {
			return chatImageRecoveryConsentRecord{}, err
		}
		record := chatImageRecoveryConsentRecord{
			SchemaVersion: chatImageRecoveryConsentSchemaVersion, ChallengeID: challengeID,
			Scope: chatImageRecoveryConsentScope, AccountID: value.AccountID, EvidenceID: inspection.EvidenceID,
			GenerationID: value.GenerationID, SnapshotManifestSHA256: value.SnapshotManifestSHA256,
			MessageBindingSHA256: inspection.MessageBindingSHA256, DescriptorSHA256: inspection.CandidateDescriptorSHA256,
			CandidateTier: inspection.CandidateTier, LocalQualityTier: inspection.LocalQualityTier,
			OutputPathSHA256: chatImageRecoveryOutputSHA256(output), ObservedAt: observedAt,
			IssuedAt: issuedAt.Format(time.RFC3339Nano), ExpiresAt: issuedAt.Add(chatImageRecoveryConsentTTL).Format(time.RFC3339Nano),
		}
		path := filepath.Join(directory, challengeID+".pending.json")
		if err := writeChatImageRecoveryConsent(path, record); err == nil {
			return record, nil
		} else if !os.IsExist(err) {
			return chatImageRecoveryConsentRecord{}, err
		}
	}
	return chatImageRecoveryConsentRecord{}, errors.New("无法生成唯一恢复授权 challenge")
}

func decodeChatImageRecoveryConsent(path string) (chatImageRecoveryConsentRecord, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 16*1024 {
		return chatImageRecoveryConsentRecord{}, &chatImageRecoveryConsentError{kind: "invalid"}
	}
	reparse, err := outputPathIsReparsePoint(path)
	if err != nil || reparse {
		return chatImageRecoveryConsentRecord{}, &chatImageRecoveryConsentError{kind: "invalid"}
	}
	file, err := os.Open(path)
	if err != nil {
		return chatImageRecoveryConsentRecord{}, &chatImageRecoveryConsentError{kind: "invalid"}
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, 16*1024+1))
	if err != nil || len(payload) > 16*1024 {
		return chatImageRecoveryConsentRecord{}, &chatImageRecoveryConsentError{kind: "invalid"}
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var record chatImageRecoveryConsentRecord
	if decoder.Decode(&record) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		record.SchemaVersion != chatImageRecoveryConsentSchemaVersion || !validChatImageRecoveryChallengeID(record.ChallengeID) ||
		record.Scope != chatImageRecoveryConsentScope || record.AccountID == "" || record.EvidenceID == "" ||
		record.GenerationID == "" || !validChatImageRecoverySHA256(record.SnapshotManifestSHA256) ||
		!validChatImageRecoverySHA256(record.MessageBindingSHA256) || !validChatImageRecoverySHA256(record.DescriptorSHA256) ||
		record.CandidateTier == "" || !validChatImageRecoverySHA256(record.OutputPathSHA256) ||
		record.ObservedAt == "" || record.IssuedAt == "" || record.ExpiresAt == "" {
		return chatImageRecoveryConsentRecord{}, &chatImageRecoveryConsentError{kind: "invalid"}
	}
	observedAt, observedErr := time.Parse(time.RFC3339Nano, record.ObservedAt)
	issuedAt, issuedErr := time.Parse(time.RFC3339Nano, record.IssuedAt)
	expiresAt, expiresErr := time.Parse(time.RFC3339Nano, record.ExpiresAt)
	if observedErr != nil || issuedErr != nil || expiresErr != nil || observedAt.IsZero() ||
		!expiresAt.After(issuedAt) || expiresAt.After(issuedAt.Add(chatImageRecoveryConsentTTL)) {
		return chatImageRecoveryConsentRecord{}, &chatImageRecoveryConsentError{kind: "invalid"}
	}
	return record, nil
}

func consumeChatImageRecoveryConsent(accountID, challengeID string) (chatImageRecoveryConsentRecord, error) {
	if !validChatImageRecoveryChallengeID(challengeID) {
		return chatImageRecoveryConsentRecord{}, &chatImageRecoveryConsentError{kind: "invalid"}
	}
	directory, err := state.EnsureRecoveryConsentPath(accountID)
	if err != nil {
		return chatImageRecoveryConsentRecord{}, err
	}
	pending := filepath.Join(directory, challengeID+".pending.json")
	used := filepath.Join(directory, challengeID+".used.json")
	if _, err := os.Lstat(used); err == nil {
		return chatImageRecoveryConsentRecord{}, &chatImageRecoveryConsentError{kind: "replayed", consumed: true}
	} else if !os.IsNotExist(err) {
		return chatImageRecoveryConsentRecord{}, err
	}
	if err := os.Rename(pending, used); err != nil {
		if _, usedErr := os.Lstat(used); usedErr == nil {
			return chatImageRecoveryConsentRecord{}, &chatImageRecoveryConsentError{kind: "replayed", consumed: true}
		}
		if os.IsNotExist(err) {
			return chatImageRecoveryConsentRecord{}, &chatImageRecoveryConsentError{kind: "not_found"}
		}
		return chatImageRecoveryConsentRecord{}, err
	}
	record, err := decodeChatImageRecoveryConsent(used)
	if err != nil {
		var consentErr *chatImageRecoveryConsentError
		if errors.As(err, &consentErr) {
			consentErr.consumed = true
		}
		return chatImageRecoveryConsentRecord{}, err
	}
	if record.ChallengeID != challengeID || record.AccountID != accountID {
		return chatImageRecoveryConsentRecord{}, &chatImageRecoveryConsentError{kind: "scope_mismatch", consumed: true}
	}
	return record, nil
}

func removeRecoveryTemporary(path string) error {
	if path == "" {
		return nil
	}
	err := removeChatImageRecoveryTemporary(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func publishChatImageRecoveryPayload(target string, payload []byte) (bool, error) {
	file, temporary, err := createTemporaryFileNear(target)
	if err != nil {
		return false, err
	}
	cleanup := func(cause error) (bool, error) {
		_ = file.Close()
		if cleanupErr := removeRecoveryTemporary(temporary); cleanupErr != nil {
			return false, &chatImageRecoveryPublishError{kind: "temporary_cleanup_failed", cause: errors.Join(cause, cleanupErr)}
		}
		return false, cause
	}
	if err := secureRecoveryTemporaryFile(temporary); err != nil {
		return cleanup(err)
	}
	if written, err := file.Write(payload); err != nil || written != len(payload) {
		if err == nil {
			err = io.ErrShortWrite
		}
		return cleanup(err)
	}
	if err := file.Sync(); err != nil {
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		return cleanup(err)
	}
	if err := os.Link(temporary, target); err != nil {
		return cleanup(err)
	}
	if err := removeRecoveryTemporary(temporary); err != nil {
		return true, &chatImageRecoveryPublishError{kind: "temporary_cleanup_failed", outputCommitted: true, cause: err}
	}
	return true, nil
}

func chatImageRecoveryArtifactValid(artifact store.ChatImageRemoteArtifact) bool {
	if len(artifact.Data) == 0 || len(artifact.Data) > maxChatImageRecoveryPlainBytes || artifact.Bytes != len(artifact.Data) ||
		artifact.Width <= 0 || artifact.Height <= 0 || artifact.SourceOriginalQualityStatus != "unknown" ||
		artifact.QualityBasis != "snapshot_remote_descriptor_variant" || artifact.ContainerValidation == "" ||
		artifact.MIMEStatus != "response_mime_and_decoded_structure_consistent" || !artifact.NetworkAccessPerformed ||
		!validChatImageRecoverySHA256(artifact.DescriptorSHA256) || !validChatImageRecoverySHA256(artifact.MessageBindingSHA256) {
		return false
	}
	if artifact.Format != "jpg" && artifact.Format != "png" && artifact.Format != "gif" {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, artifact.RetrievedAt); err != nil {
		return false
	}
	sha := sha256.Sum256(artifact.Data)
	md5Digest := md5.Sum(artifact.Data)
	return artifact.SHA256 == hex.EncodeToString(sha[:]) && artifact.ContentMD5 == hex.EncodeToString(md5Digest[:])
}

func chatImageRecoveryConsentCommandError(err error) error {
	var consentErr *chatImageRecoveryConsentError
	if !errors.As(err, &consentErr) {
		return &commandError{typeName: "chat_image_recovery_consent_state_failed", message: "无法读取或消费恢复授权 challenge", hint: "检查账号私有状态目录权限后重新生成 challenge。", details: chatImageRecoveryErrorDetails(map[string]any{"network_access_performed": false, "authorization_consumption_status": "unknown"}), code: 5}
	}
	details := func() map[string]any {
		return chatImageRecoveryErrorDetails(map[string]any{"network_access_performed": false, "authorization_consumed": consentErr.consumed, "new_authorization_required": true})
	}
	switch consentErr.kind {
	case "replayed":
		return &commandError{typeName: "chat_image_recovery_consent_replayed", message: "该恢复授权已经使用", hint: "重新运行不带 --consent 的 recover-chat-image，并为新 challenge 取得新的明确授权。", details: details(), code: 5}
	case "scope_mismatch":
		return &commandError{typeName: "chat_image_recovery_consent_scope_mismatch", message: "恢复授权与当前账号不匹配", hint: "为当前账号和图片重新生成 challenge。", details: details(), code: 5}
	case "not_found":
		return &commandError{typeName: "chat_image_recovery_consent_not_found", message: "恢复授权不存在或已清理", hint: "重新运行不带 --consent 的 recover-chat-image 生成 challenge。", details: details(), code: 5}
	default:
		return &commandError{typeName: "chat_image_recovery_consent_invalid", message: "恢复授权记录无效", hint: "不要手工编辑 challenge；重新生成并取得新的明确授权。", details: details(), code: 5}
	}
}

func chatImageRemoteRecoveryCommandError(err error, authorizationConsumed bool, observedAt string) error {
	var recoveryErr *store.ChatImageRemoteRecoveryError
	if !errors.As(err, &recoveryErr) {
		return &commandError{typeName: "chat_image_recovery_failed", message: "聊天图片远端恢复失败", hint: "没有生成输出；重新检查当前 generation 的恢复诊断。", details: chatImageRecoveryTemporalErrorDetails(map[string]any{"network_access_performed": false, "authorization_consumed": authorizationConsumed, "descriptor_expiry_known": false, "descriptor_expiry_status": "not_evaluated"}, observedAt), code: 5}
	}
	details := map[string]any{
		"network_access_performed": recoveryErr.NetworkAttempted,
		"descriptor_expiry_known":  false,
		"descriptor_expiry_status": recoveryErr.DescriptorExpiryStatus,
		"authorization_consumed":   authorizationConsumed,
	}
	chatImageRecoveryTemporalErrorDetails(details, observedAt)
	if authorizationConsumed {
		details["retry_policy"] = "new_challenge_and_new_explicit_authorization_required"
	} else {
		details["retry_policy"] = "refresh_snapshot_then_reinspect"
	}
	message := "聊天图片远端恢复失败"
	hint := "没有生成输出；重新生成 challenge 并取得新的单次授权。"
	switch recoveryErr.Kind {
	case "chat_image_remote_authorization_rejected", "chat_image_remote_resource_unavailable":
		message = "临时 CDN 资源在本次请求时不可用"
		hint = "URL 或鉴权参数可能已失效，但不能据此证明已过期；刷新快照取得新描述符，再生成新 challenge 并重新授权。"
	case "chat_image_remote_redirect_rejected":
		message = "CDN 返回了越界重定向"
		hint = "CLI 未跟随重定向；刷新快照取得新描述符并重新授权。"
	case "chat_image_remote_response_size_invalid":
		message = "CDN 响应为空或超过图片大小上限"
	case "chat_image_remote_response_read_failed":
		message = "CDN 下载中断或响应不完整"
	case "chat_image_remote_mime_invalid", "chat_image_remote_mime_mismatch":
		message = "CDN MIME 与实际图片结构不一致"
	case "chat_image_remote_descriptor_mismatch", "chat_image_remote_message_binding_mismatch", "chat_image_remote_candidate_unavailable":
		message = "恢复授权绑定的消息或候选描述符已变化"
		hint = "没有联网；使用当前 generation 重新生成 challenge 并取得新授权。"
	case "chat_image_remote_rate_limited":
		message = "CDN 暂时限流"
		hint = "限流不证明描述符已过期；稍后生成新 challenge 并重新取得单次授权。"
	case "chat_image_remote_decrypt_failed", "chat_image_remote_container_invalid",
		"chat_image_remote_descriptor_size_mismatch", "chat_image_remote_descriptor_dimensions_mismatch",
		"chat_image_remote_descriptor_md5_mismatch":
		message = "CDN 响应未通过解密、完整结构或消息归属验证"
		hint = "没有生成输出；不要回退到缩略图声称成功，刷新快照并复核协议后再授权。"
	}
	return &commandError{typeName: recoveryErr.Kind, message: message, hint: hint, details: details, code: 5}
}

func chatImageRecoveryUnavailableError(inspection store.ChatImageRemoteRecoveryInspection, observedAt string) error {
	expiryStatus := "not_applicable"
	if inspection.RemoteDescriptorStatus == "present_expiry_unknown" {
		expiryStatus = "unknown_without_verified_request"
	} else if inspection.RemoteDescriptorStatus == "unknown" {
		expiryStatus = "not_evaluated"
	}
	details := map[string]any{
		"remote_acquisition_status":      inspection.AcquisitionStatus,
		"remote_descriptor_status":       inspection.RemoteDescriptorStatus,
		"remote_descriptor_parse_status": inspection.RemoteDescriptorParseStatus,
		"remote_protocol_status":         inspection.RemoteProtocolStatus,
		"remote_descriptor_tiers":        inspection.RemoteDescriptorTiers,
		"source_original_quality_status": "unknown",
		"network_access_performed":       false,
		"descriptor_expiry_known":        false,
		"descriptor_expiry_status":       expiryStatus,
	}
	chatImageRecoveryTemporalErrorDetails(details, observedAt)
	switch inspection.AcquisitionStatus {
	case "unavailable_unverified_desktop_protocol":
		details["recovery_action"] = "ask_user_to_open_original_then_refresh_and_retry_once"
		return &commandError{typeName: "chat_image_recovery_protocol_unavailable", message: "当前描述符只有未验收的不透明桌面参数", hint: "不要把参数拼接到 iLink CDN。请用户手动在微信打开这一张原图；确认后由 Agent refresh --require-media，并对同一 evidence 只重试一次。", details: details, code: 5}
	case "only_lower_or_equal_remote_variant":
		details["recovery_action"] = "stop_only_lower_or_equal_variant_available"
		return &commandError{typeName: "chat_image_recovery_no_higher_variant", message: "只能定位到不高于当前本地缓存层级的远端候选", hint: "停止恢复；不要把同级或更低缓存当作更高质量成功。", details: details, code: 5}
	case "local_quality_tier_unknown_manual_review":
		details["recovery_action"] = "manual_review"
		return &commandError{typeName: "chat_image_recovery_local_quality_unknown", message: "当前本地候选层级未知", hint: "先人工复核候选归属；不要仅凭尺寸或文件大小升级质量状态。", details: details, code: 5}
	default:
		details["recovery_action"] = "refresh_snapshot_then_reinspect"
		return &commandError{typeName: "chat_image_recovery_descriptor_unavailable", message: "当前快照没有可安全尝试的更高层级 HTTPS 描述符", hint: "刷新快照后重新检查；不要猜测 URL 或静默回退到缩略图。", details: details, code: 5}
	}
}

func runRecoverChatImage(args []string) (any, error) {
	set := flag.NewFlagSet("recover-chat-image", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	output := set.String("output", "", "输出文件")
	consent := set.String("consent", "", "本次结构化恢复授权 challenge")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || strings.TrimSpace(*output) == "" {
		return nil, invalidArguments("用法：v-local-cli recover-chat-image --output FILE [--account NAME] [--consent CHALLENGE] <image_evidence_id>")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	lock, err := acquireSnapshotTransaction(value.AccountID)
	if err != nil {
		var commandErr *commandError
		if errors.As(err, &commandErr) && commandErr.typeName == "snapshot_busy" {
			return nil, &commandError{
				typeName: commandErr.typeName, message: commandErr.message, hint: commandErr.hint,
				details: chatImageRecoveryErrorDetails(map[string]any{
					"network_access_performed": false, "authorization_consumed": false,
					"descriptor_expiry_known": false, "descriptor_expiry_status": "not_evaluated",
					"retrieved_at": nil, "retry_policy": "wait_for_snapshot_transaction_then_retry_same_command",
				}),
				code: commandErr.code,
			}
		}
		return nil, err
	}
	defer func() { _ = lock.Release() }()
	value, err = resolveInitializedAccount(value.AccountID)
	if err != nil {
		return nil, err
	}
	target, err := prepareOutputTarget(*output, false)
	if err != nil {
		return nil, err
	}
	evidenceID := set.Args()[0]
	if strings.TrimSpace(*consent) == "" {
		bundle, _, err := state.LoadSecretsOptional(value.AccountID)
		if err != nil {
			return nil, err
		}
		aesKey, xorKey := "", 0
		if bundle.ImageKeys != nil {
			aesKey, xorKey = bundle.ImageKeys.AES, bundle.ImageKeys.XOR
		}
		localTier := ""
		if localImage, localErr := store.ResolveChatImage(value.SnapshotPath, value.AccountPath, evidenceID, aesKey, xorKey); localErr == nil {
			localTier = localImage.QualityTier
			clear(localImage.Data)
		} else {
			var resolutionErr *store.ChatImageResolutionError
			if !errors.As(localErr, &resolutionErr) ||
				(resolutionErr.Kind != "local_file_missing" && resolutionErr.Kind != "local_mapping_unavailable" &&
					resolutionErr.Kind != "resource_descriptor_unavailable" && resolutionErr.Kind != "decoder_unavailable") {
				return nil, errorWithGeneration(chatImageCommandError(localErr), value)
			}
			if resolutionErr.Kind == "decoder_unavailable" {
				localTier = resolutionErr.QualityTier
			}
		}
		inspection, err := inspectChatImageRemoteRecovery(value.SnapshotPath, evidenceID, localTier)
		if err != nil {
			return nil, errorWithGeneration(chatImageRemoteRecoveryCommandError(err, false, chatImageRecoveryObservedAt(value)), value)
		}
		if inspection.AcquisitionStatus != "direct_https_candidate_available" {
			return nil, errorWithGeneration(chatImageRecoveryUnavailableError(inspection, chatImageRecoveryObservedAt(value)), value)
		}
		record, err := issueChatImageRecoveryConsent(value, inspection, target)
		if err != nil {
			return nil, errorWithGeneration(&commandError{typeName: "chat_image_recovery_consent_issue_failed", message: "无法创建私有恢复授权 challenge", hint: "检查账号私有状态目录权限后重试。", details: chatImageRecoveryErrorDetails(map[string]any{"network_access_performed": false}), code: 5}, value)
		}
		localTierDisplay := record.LocalQualityTier
		if localTierDisplay == "" {
			localTierDisplay = "none_verified"
		}
		details := map[string]any{
			"consent_challenge": map[string]any{
				"id": record.ChallengeID, "scope": record.Scope, "account": value.AccountName, "account_id": record.AccountID,
				"evidence_id": record.EvidenceID, "message_binding_sha256": record.MessageBindingSHA256,
				"generation_id": record.GenerationID, "snapshot_manifest_sha256": record.SnapshotManifestSHA256,
				"candidate_descriptor_sha256": record.DescriptorSHA256, "candidate_tier": record.CandidateTier,
				"local_quality_tier": localTierDisplay,
				"observed_at":        record.ObservedAt, "authorization_expires_at": record.ExpiresAt,
				"descriptor_expiry_known": false, "descriptor_expiry_status": "unknown_without_verified_request",
				"network_attempts_authorized": 1, "network_destination": inspection.NetworkDestination,
				"wechat_ui_automation_authorized": false,
			},
			"network_access_performed": false, "source_original_quality_status": "unknown",
		}
		return nil, errorWithGeneration(&commandError{
			typeName: "chat_image_recovery_network_authorization_required",
			message:  "需要用户明确授权本次单一聊天图片 CDN 尝试",
			hint:     "说明 challenge 的账号、消息、图片候选、目标域名和单次范围；用户明确同意后，原样使用该 id 增加 --consent。不要把同意扩展到其他图片或微信 UI 自动化。",
			details:  details, code: 3,
		}, value)
	}
	record, err := consumeChatImageRecoveryConsent(value.AccountID, strings.TrimSpace(*consent))
	if err != nil {
		return nil, errorWithGeneration(chatImageRecoveryConsentCommandError(err), value)
	}
	issuedAt, issuedParseErr := time.Parse(time.RFC3339Nano, record.IssuedAt)
	expiresAt, expiresParseErr := time.Parse(time.RFC3339Nano, record.ExpiresAt)
	now := chatImageRecoveryNow().UTC()
	if issuedParseErr != nil || expiresParseErr != nil || now.Before(issuedAt) || !now.Before(expiresAt) {
		return nil, errorWithGeneration(&commandError{typeName: "chat_image_recovery_consent_expired", message: "恢复授权已过期", hint: "重新生成 challenge 并取得新的明确授权。", details: chatImageRecoveryTemporalErrorDetails(map[string]any{"network_access_performed": false, "authorization_consumed": true, "new_authorization_required": true, "descriptor_expiry_known": false, "descriptor_expiry_status": "not_evaluated"}, record.ObservedAt), code: 5}, value)
	}
	if record.EvidenceID != evidenceID || record.OutputPathSHA256 != chatImageRecoveryOutputSHA256(target) {
		return nil, errorWithGeneration(&commandError{typeName: "chat_image_recovery_consent_scope_mismatch", message: "恢复授权与当前消息、图片或输出目标不匹配", hint: "重新生成与当前操作严格绑定的 challenge。", details: chatImageRecoveryTemporalErrorDetails(map[string]any{"network_access_performed": false, "authorization_consumed": true, "new_authorization_required": true, "descriptor_expiry_known": false, "descriptor_expiry_status": "not_evaluated"}, record.ObservedAt), code: 5}, value)
	}
	if record.GenerationID != value.GenerationID || record.SnapshotManifestSHA256 != value.SnapshotManifestSHA256 {
		return nil, errorWithGeneration(&commandError{typeName: "chat_image_recovery_snapshot_changed", message: "快照代际在授权后发生变化", hint: "没有联网；在新 generation 重新检查描述符并取得新的单次授权。", details: chatImageRecoveryTemporalErrorDetails(map[string]any{"network_access_performed": false, "authorization_consumed": true, "new_authorization_required": true, "descriptor_expiry_known": false, "descriptor_expiry_status": "not_evaluated", "current_snapshot_observed_at": chatImageRecoveryObservedAt(value)}, record.ObservedAt), code: 5}, value)
	}
	inspection, err := inspectChatImageRemoteRecovery(value.SnapshotPath, evidenceID, record.LocalQualityTier)
	if err != nil || inspection.CandidateDescriptorSHA256 != record.DescriptorSHA256 ||
		inspection.MessageBindingSHA256 != record.MessageBindingSHA256 || inspection.CandidateTier != record.CandidateTier {
		return nil, errorWithGeneration(&commandError{typeName: "chat_image_recovery_descriptor_changed", message: "候选描述符或消息绑定在授权后发生变化", hint: "没有联网；重新生成 challenge 并取得新授权。", details: chatImageRecoveryTemporalErrorDetails(map[string]any{"network_access_performed": false, "authorization_consumed": true, "new_authorization_required": true, "descriptor_expiry_known": false, "descriptor_expiry_status": "not_evaluated", "current_snapshot_observed_at": chatImageRecoveryObservedAt(value)}, record.ObservedAt), code: 5}, value)
	}
	artifact, err := recoverChatImageRemote(context.Background(), value.SnapshotPath, evidenceID, record.LocalQualityTier, record.DescriptorSHA256)
	if err != nil {
		return nil, errorWithGeneration(chatImageRemoteRecoveryCommandError(err, true, record.ObservedAt), value)
	}
	defer clear(artifact.Data)
	if !chatImageRecoveryArtifactValid(artifact) || artifact.DescriptorSHA256 != record.DescriptorSHA256 || artifact.MessageBindingSHA256 != record.MessageBindingSHA256 ||
		artifact.EvidenceID != record.EvidenceID || artifact.QualityTier != record.CandidateTier {
		return nil, errorWithGeneration(&commandError{typeName: "chat_image_recovery_download_binding_mismatch", message: "下载结果与授权绑定不一致", hint: "没有生成输出；刷新快照并重新取证。", details: chatImageRecoveryTemporalErrorDetails(map[string]any{"network_access_performed": true, "authorization_consumed": true, "descriptor_expiry_known": false, "descriptor_expiry_status": "response_unverified"}, record.ObservedAt), code: 5}, value)
	}
	committed, err := publishChatImageRecoveryPayload(target, artifact.Data)
	if err != nil {
		var publishErr *chatImageRecoveryPublishError
		if errors.As(err, &publishErr) && publishErr.kind == "temporary_cleanup_failed" {
			return nil, errorWithGeneration(&commandError{typeName: "chat_image_recovery_temporary_cleanup_failed", message: "恢复图片临时文件清理失败", hint: "停止后续恢复并检查输出目录；错误详情会说明最终输出是否已提交。", details: chatImageRecoveryErrorDetails(map[string]any{"network_access_performed": true, "authorization_consumed": true, "output_committed": publishErr.outputCommitted, "remote_descriptor_status": "verified_at_request_time", "descriptor_expiry_known": false, "descriptor_expiry_status": "unknown_future", "observed_at": record.ObservedAt, "retrieved_at": artifact.RetrievedAt}), code: 5}, value)
		}
		return nil, errorWithGeneration(&commandError{typeName: "chat_image_recovery_output_failed", message: "无法安全发布恢复图片", hint: "没有复用授权；修复输出路径后生成新 challenge。", details: chatImageRecoveryErrorDetails(map[string]any{"network_access_performed": true, "authorization_consumed": true, "output_committed": committed, "remote_descriptor_status": "verified_at_request_time", "descriptor_expiry_known": false, "descriptor_expiry_status": "unknown_future", "observed_at": record.ObservedAt, "retrieved_at": artifact.RetrievedAt}), code: 5}, value)
	}
	return outputWithGeneration(map[string]any{
		"account": value.AccountName, "evidence_id": artifact.EvidenceID, "output": target,
		"chat": artifact.Chat, "local_id": artifact.LocalID, "server_id": artifact.ServerID,
		"timestamp": artifact.Timestamp, "sort_key": artifact.SortKey,
		"format": artifact.Format, "bytes": artifact.Bytes, "width": artifact.Width, "height": artifact.Height,
		"sha256": artifact.SHA256, "content_md5": artifact.ContentMD5,
		"source": "verified_remote_chat_image", "resolution_status": "verified_remote_download",
		"verified_by":          "snapshot_message_binding+descriptor_fingerprint+https_response+full_decode",
		"container_validation": artifact.ContainerValidation, "decryption_scope": artifact.DecryptionScope,
		"mime_status": artifact.MIMEStatus, "descriptor_bytes_status": artifact.DescriptorBytesStatus,
		"descriptor_dimensions_status": artifact.DescriptorDimensionsStatus, "descriptor_md5_status": artifact.DescriptorMD5Status,
		"quality_tier": artifact.QualityTier, "quality_basis": artifact.QualityBasis,
		"quality_claim_scope": "wechat_remote_variant_only", "source_original_quality_status": "unknown",
		"source_original_dimensions_known": false, "dimensions_role": "decoded_output_observation_not_quality_gate",
		"remote_descriptor_status": "verified_at_request_time", "descriptor_expiry_known": false,
		"descriptor_expiry_status": "unknown_future", "observed_at": record.ObservedAt, "retrieved_at": artifact.RetrievedAt,
		"network_access_performed": artifact.NetworkAccessPerformed, "network_attempts": 1,
		"authorization_scope": record.Scope, "authorization_consumed": true,
		"candidate_descriptor_sha256": artifact.DescriptorSHA256, "message_binding_sha256": artifact.MessageBindingSHA256,
	}, value), nil
}
