package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
)

// 4096 个 catalog proof 加上逐库 key/profile 可能合理地超过 1 MiB；仍以固定上限
// 拒绝失控输出，避免 Provider 响应造成无界内存增长。
const maxResponseBytes = 8 * 1024 * 1024

const (
	maxCatalogEntries   = 4096
	maxResponseProfiles = 64
)

// acquireTimeout 是硬性上限，到点直接杀进程；acquireBudget 是告知提供器的软截止时限。
// 两者之间留出的余量用于进程启动、收尾和响应编码，确保提供器有机会
// 在被杀之前把已经验证出的密钥写回来。
const (
	acquireTimeout = 90 * time.Second
	acquireBudget  = 75 * time.Second
)

// ErrComponentMissing 表示 PATH 上没有找到密钥获取组件，用户尚未单独安装它；
// 调用方据此区分「组件未安装」与「组件已安装但取证失败」两种情况。
var ErrComponentMissing = errors.New("未找到 v-local-key-provider")

// ProtocolContractError 表示结构无效或内部不一致的 Provider 响应。不得用从无效
// payload 子集推断出的采集结果替代它。
type ProtocolContractError struct {
	Cause error
	Stage string
}

func (value *ProtocolContractError) Error() string {
	if value == nil || value.Cause == nil {
		return "Provider 响应违反协议契约"
	}
	return "Provider 响应违反协议契约: " + value.Cause.Error()
}

func (value *ProtocolContractError) Unwrap() error {
	if value == nil {
		return nil
	}
	return value.Cause
}

// AcquisitionError 只包含提供器返回的状态枚举，绝不携带候选值、路径、标准错误输出或
// 进程内存内容。
type AcquisitionError struct {
	Reason                      string
	ResultCode                  string
	WorkflowStatus              string
	RequestedScopes             []string
	DatabaseTargetStatus        string
	DatabaseCoverageStatus      string
	MediaCoverageStatus         string
	SecurityPostureStatus       string
	ShadowRouteStatus           string
	RoutePriority               []string
	NextAction                  string
	TargetBindingStatus         string
	SessionAccountStatus        string
	BlockingReasons             []string
	Platform                    string
	ProcessAccessStatus         string
	ProcessAccessError          string
	ProcessDiscoveryMethod      string
	HelperStatus                string
	VersionSupport              string
	WeChatVersion               string
	WeChatBuild                 string
	ExecutableSHA256            string
	BinaryFingerprintStatus     string
	BinarySigningStatus         string
	BinarySignerSHA256          string
	BinaryProductIdentity       string
	SigningTeamID               string
	DesignatedRequirementSHA256 string
	ProcessArchitecture         string
	ProcessArchitectureStatus   string
	ProcessTranslationStatus    string
	MacOSVersion                string
	CompatibilityRegistryStatus string
	StandardRouteStatus         string
	StandardRouteEvidence       []string
	ConfigCipherRouteStatus     string
	WindowsRouteEvidence        []string
	ProcessCount                int
	SelectedProcessCount        int
	TargetBoundProcessCount     int
	OtherAccountProcessCount    int
	UnknownAccountProcessCount  int
	OpenedProcessCount          int
	AccessDeniedCount           int
	PerProcessCollectorCount    int
	ConfigCipherStructureCount  int
	ConfigCipherInvalidCount    int
	ConfigCipherCandidateCount  int
	ConfigCipherVerifiedCount   int
	FallbackCandidateCount      int
	FallbackStageCounts         map[string]int
	SessionID                   string
	CatalogID                   string
	ProcessInstanceID           string
	ActionStage                 string
	RouteSelected               string
	ExternalCheckpointStatus    string
	ExternalWorkflowID          string
}

func (err *AcquisitionError) Error() string {
	return "密钥提供器没有完成当前请求的全部密钥范围"
}

var hexKeyPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}(?:[0-9a-fA-F]{32})?$`)

type ImageKeys struct {
	AES string `json:"aes"`
	XOR int    `json:"xor"`
}

type CatalogEntry struct {
	DatabaseID             string `json:"database_id"`
	RelativePath           string `json:"relative_path"`
	CanonicalFileID        string `json:"canonical_file_id"`
	Size                   int64  `json:"size"`
	MTimeNS                int64  `json:"mtime_ns"`
	FirstPageSHA256        string `json:"first_page_sha256,omitempty"`
	Classification         string `json:"classification"`
	RequiredForKeyCoverage bool   `json:"required_for_key_coverage"`
	ProfileID              string `json:"profile_id,omitempty"`
	Reason                 string `json:"reason,omitempty"`
}

type CandidateBundle struct {
	Protocol           string              `json:"protocol,omitempty"`
	RequestID          string              `json:"request_id,omitempty"`
	CatalogID          string              `json:"catalog_id,omitempty"`
	CatalogEntries     []CatalogEntry      `json:"catalog_entries,omitempty"`
	DatabaseKeys       map[string]string   `json:"database_keys"`
	DatabaseProfiles   map[string]string   `json:"database_profiles,omitempty"`
	DatabaseCredential *DatabaseCredential `json:"database_credential,omitempty"`
	ImageKeys          *ImageKeys          `json:"image_keys,omitempty"`
	Profiles           []ProfileSummary    `json:"profiles,omitempty"`
	Diagnostics        map[string]any      `json:"diagnostics,omitempty"`
}

type ProfileSummary struct {
	ID                  string `json:"profile_id"`
	CipherAlgorithm     string `json:"cipher_algorithm"`
	KeySize             int    `json:"key_size"`
	PageSize            int    `json:"page_size"`
	PlaintextHeaderSize int    `json:"plaintext_header_size"`
	ReserveSize         int    `json:"reserve_size"`
	KDFAlgorithm        string `json:"kdf_algorithm"`
	KDFPRF              string `json:"kdf_prf"`
	KDFIterations       int    `json:"kdf_iterations"`
	HMACAlgorithm       string `json:"hmac_algorithm"`
	HMACKDFAlgorithm    string `json:"hmac_kdf_algorithm"`
	HMACKDFIterations   int    `json:"hmac_kdf_iterations"`
	HMACInputLayout     string `json:"hmac_input_layout"`
	PageNumberEndian    string `json:"page_number_endian"`
}

func supportedProfileSummary(profile ProfileSummary) bool {
	return profile.ID == "wcdb-v4-sha512-256000-r80" && profile.CipherAlgorithm == "aes-256-cbc" &&
		profile.KeySize == 32 && profile.PageSize == 4096 && profile.PlaintextHeaderSize == 16 && profile.ReserveSize == 80 &&
		profile.KDFAlgorithm == "pbkdf2" && profile.KDFPRF == "hmac-sha512" && profile.KDFIterations == 256000 &&
		profile.HMACAlgorithm == "hmac-sha512" && profile.HMACKDFAlgorithm == "pbkdf2" && profile.HMACKDFIterations == 2 &&
		profile.HMACInputLayout == "page_without_salt_and_hmac_then_page_number" && profile.PageNumberEndian == "little-endian"
}

type workflowRequest struct {
	Operation         string         `json:"operation"`
	SessionID         string         `json:"session_id,omitempty"`
	ExpectedCatalogID string         `json:"expected_catalog_id,omitempty"`
	ActionReceipt     *actionReceipt `json:"action_receipt,omitempty"`
}

type actionReceipt struct {
	Action                    string `json:"action"`
	UserConfirmed             bool   `json:"user_confirmed"`
	ObservedProcessTransition string `json:"observed_process_transition,omitempty"`
	ProcessInstanceID         string `json:"process_instance_id,omitempty"`
	Route                     string `json:"route,omitempty"`
	ActionStage               string `json:"action_stage,omitempty"`
}

type acquireRequest struct {
	Protocol   string          `json:"protocol"`
	RequestID  string          `json:"request_id"`
	Action     string          `json:"action"`
	CatalogKey string          `json:"catalog_key,omitempty"`
	AccountDir string          `json:"account_dir"`
	DBDir      string          `json:"db_dir"`
	Scopes     []string        `json:"scopes"`
	DeadlineMS int64           `json:"deadline_ms"`
	Workflow   workflowRequest `json:"workflow"`
}

type limitedBuffer struct {
	buffer    []byte
	sensitive []byte
	limit     int
	over      bool
}

// ExecutionError 只暴露 Provider 子进程失败阶段和退出码，不携带路径、stderr 或请求内容。
type ExecutionError struct {
	Stage    string
	ExitCode int
}

func (err *ExecutionError) Error() string {
	return "密钥提供器子进程失败"
}

func (writer *limitedBuffer) Write(data []byte) (int, error) {
	var over bool
	writer.buffer, over = appendSensitiveBytesLimited(writer.buffer, data, writer.limit)
	writer.sensitive = writer.buffer
	writer.over = writer.over || over
	return len(data), nil
}

func (writer *limitedBuffer) Bytes() []byte { return writer.buffer }

func (writer *limitedBuffer) Clear() {
	clearSensitiveBytes(writer.buffer)
	writer.buffer = nil
	writer.sensitive = nil
	writer.over = false
}

func newRequestID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// Acquire 在受限协议上调用独立密钥提供器，并请求完整的数据库和图片候选。
// 密钥只经标准输入和标准输出传递。
func Acquire(parent context.Context, explicit string, account localplatform.Account) (CandidateBundle, error) {
	return AcquireScopes(parent, explicit, account, []string{"database", "media"})
}

// AcquireScopes 允许调用方为明确的“仅数据库”流程缩小请求范围。
func AcquireScopes(parent context.Context, explicit string, account localplatform.Account, scopes []string) (CandidateBundle, error) {
	return acquireScopes(parent, explicit, account, scopes, "", "")
}

// AcquireScopesWithRoot 使用当前用户私有目录保存密钥获取守护进程的认证端点和不含
// 凭据的续接元数据。实际候选只经受控进程间通信返回，不写入该目录。
func AcquireScopesWithRoot(parent context.Context, explicit string, account localplatform.Account, scopes []string, privateRoot string) (CandidateBundle, error) {
	return acquireScopes(parent, explicit, account, scopes, privateRoot, "")
}

// AcquireScopesWithRootAndAction 仅当调用方传入用户明确确认的同一操作时，才续接待处理的
// 密钥获取操作。一般性的进程访问授权不能视为对具体操作的同意。
func AcquireScopesWithRootAndAction(parent context.Context, explicit string, account localplatform.Account, scopes []string, privateRoot, confirmedAction string) (CandidateBundle, error) {
	return acquireScopes(parent, explicit, account, scopes, privateRoot, confirmedAction)
}

func acquireScopes(parent context.Context, explicit string, account localplatform.Account, scopes []string, privateRoot, confirmedAction string) (CandidateBundle, error) {
	normalizedScopes, err := normalizeRequestedScopes(scopes)
	if err != nil {
		return CandidateBundle{}, err
	}
	scopes = normalizedScopes
	path, source := Resolve(explicit)
	if path == "" {
		if source == "override_rejected" || strings.HasPrefix(source, "untrusted_") {
			return CandidateBundle{}, ErrComponentUntrusted
		}
		return CandidateBundle{}, ErrComponentMissing
	}
	catalogKey, err := catalogKeyForPrivateRoot(privateRoot)
	if err != nil {
		return CandidateBundle{}, err
	}
	disableCheckpointPending := false
	if privateRoot != "" {
		checkpoint, pending, checkpointErr := pendingSecurityRestorationCheckpoint(privateRoot, path, account)
		if checkpointErr != nil {
			return CandidateBundle{}, checkpointErr
		}
		if pending {
			if confirmedAction != "" {
				return CandidateBundle{}, &AcquisitionError{
					Reason: "action_confirmation_mismatch", ResultCode: "action_required", WorkflowStatus: "blocked",
					RequestedScopes: append([]string(nil), checkpoint.Scopes...), SecurityPostureStatus: "restoration_required",
					NextAction: "reenable_sip", BlockingReasons: []string{"action_receipt_rejected"},
					ExternalCheckpointStatus: "persisted", ExternalWorkflowID: checkpoint.WorkflowID,
				}
			}
			bundle, revalidationErr := revalidateSecurityPostureOneShot(parent, path, account, checkpoint.Scopes, catalogKey)
			return bundle, reconcileExternalCheckpoint(privateRoot, path, account, checkpoint.Scopes, bundle, revalidationErr)
		}
		externalCheckpoint, pendingExternalChange, externalCheckpointErr := pendingExternalChangeCheckpoint(privateRoot, path, account, scopes)
		if externalCheckpointErr != nil {
			return CandidateBundle{}, externalCheckpointErr
		}
		disableCheckpointPending = pendingExternalChange && externalCheckpoint.PriorRequestedAction == "disable_sip"
	}
	if privateRoot != "" {
		bundle, used, err := acquireViaDaemon(parent, path, account, scopes, privateRoot, confirmedAction, catalogKey)
		if used {
			if runtime.GOOS == "darwin" && shouldRetryWithDarwinOneShot(err) {
				if confirmedAction != "" {
					return CandidateBundle{}, daemonRequiredForConfirmedAction(confirmedAction)
				}
				bundle, err = acquireOneShot(parent, path, account, scopes, catalogKey)
			}
			return reconcileNormalAcquisition(parent, privateRoot, path, account, scopes, catalogKey, disableCheckpointPending, bundle, err)
		}
	}
	if confirmedAction != "" {
		return CandidateBundle{}, daemonRequiredForConfirmedAction(confirmedAction)
	}
	bundle, err := acquireOneShot(parent, path, account, scopes, catalogKey)
	return reconcileNormalAcquisition(parent, privateRoot, path, account, scopes, catalogKey, disableCheckpointPending, bundle, err)
}

func reconcileNormalAcquisition(parent context.Context, privateRoot, providerPath string, account localplatform.Account, scopes []string,
	catalogKey string, disableCheckpointPending bool, bundle CandidateBundle, acquisitionErr error,
) (CandidateBundle, error) {
	if disableCheckpointPending && acquisitionErr != nil {
		// 普通采集可能尚未生成 SIP 诊断，就先发生致命的 catalog、路径或 Provider
		// 错误。仅在这一特定场景下，使用不涉及凭据的 posture RPC 判断用户是否执行了
		// 外部变更。disabled 结果优先，以免恢复流程搁置；enabled 或 unknown 结果则保留
		// 原始错误和 checkpoint。
		postureBundle, postureErr := revalidateSecurityPostureOneShot(parent, providerPath, account, scopes, catalogKey)
		var postureFailure *AcquisitionError
		if errors.As(postureErr, &postureFailure) && postureFailure.NextAction == "reenable_sip" &&
			postureFailure.SecurityPostureStatus == "restoration_required" {
			return postureBundle, reconcileExternalCheckpoint(privateRoot, providerPath, account, scopes, postureBundle, postureErr)
		}
	}
	return bundle, reconcileExternalCheckpoint(privateRoot, providerPath, account, scopes, bundle, acquisitionErr)
}

func daemonRequiredForConfirmedAction(action string) *AcquisitionError {
	return &AcquisitionError{
		Reason: "action_confirmation_mismatch", ResultCode: "action_required", WorkflowStatus: "blocked",
		NextAction: action, BlockingReasons: []string{"acquisition_daemon_unavailable"},
	}
}

func shouldRetryWithDarwinOneShot(err error) bool {
	var acquisition *AcquisitionError
	if !errors.As(err, &acquisition) {
		return false
	}
	return acquisition.ProcessAccessStatus == "denied" || acquisition.ProcessAccessError == "task_for_pid_denied" ||
		acquisition.HelperStatus == "launch_failed" || acquisition.HelperStatus == "not_installed"
}

func acquireOneShot(parent context.Context, path string, account localplatform.Account, scopes []string, catalogKey string) (CandidateBundle, error) {
	requestAccount, err := canonicalAcquisitionRequestAccount(account)
	if err != nil {
		return CandidateBundle{}, err
	}
	requestID, err := newRequestID()
	if err != nil {
		return CandidateBundle{}, errors.New("无法生成一次性请求标识")
	}
	request := acquireRequest{
		Protocol: Protocol, RequestID: requestID, Action: "acquire",
		CatalogKey: catalogKey,
		AccountDir: requestAccount.Path, DBDir: requestAccount.DBDir,
		Scopes:     append([]string(nil), scopes...),
		DeadlineMS: acquireBudget.Milliseconds(),
		Workflow:   workflowRequest{Operation: "finalize"},
	}
	response, err := executeOneShotProviderRequest(parent, path, request)
	if err != nil {
		return CandidateBundle{}, err
	}
	if err := validateFinalAcquisitionResponse(&response, scopes, requestAccount, catalogKey); err != nil {
		return CandidateBundle{}, err
	}
	return response, nil
}

func executeOneShotProviderRequest(parent context.Context, path string, request acquireRequest) (CandidateBundle, error) {
	if _, err := validateProviderExecutableTrust(path); err != nil {
		return CandidateBundle{}, ErrComponentUntrusted
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return CandidateBundle{}, errors.New("无法编码密钥请求")
	}
	markSensitiveBytes(payload)
	defer clearSensitiveBytes(payload)
	ctx, cancel := context.WithTimeout(parent, acquireTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, "acquire")
	command.Dir = filepath.Dir(path)
	configureProviderCommandEnvironment(command)
	command.Stdin = bytes.NewReader(payload)
	stdout := &limitedBuffer{limit: maxResponseBytes}
	stderr := &limitedBuffer{limit: 16 * 1024}
	defer stdout.Clear()
	defer stderr.Clear()
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return CandidateBundle{}, errors.New("密钥提供器运行超时")
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return CandidateBundle{}, &ExecutionError{Stage: "process_exit", ExitCode: exitErr.ExitCode()}
		}
		return CandidateBundle{}, &ExecutionError{Stage: "process_start", ExitCode: -1}
	}
	if stdout.over {
		return CandidateBundle{}, errors.New("密钥提供器响应超过安全上限")
	}
	var response CandidateBundle
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return CandidateBundle{}, errors.New("密钥提供器返回了无效 JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return CandidateBundle{}, errors.New("密钥提供器响应包含多余数据")
	}
	if response.Protocol != Protocol || response.RequestID != request.RequestID {
		return CandidateBundle{}, errors.New("密钥提供器响应与当前请求不匹配")
	}
	return response, nil
}

func validateSecurityPostureRevalidation(bundle CandidateBundle, scopes []string) error {
	requested, err := diagnosticStringList(bundle.Diagnostics, "requested_scopes")
	if err != nil {
		return err
	}
	expected, err := normalizeRequestedScopes(scopes)
	if err != nil || strings.Join(requested, "\x00") != strings.Join(expected, "\x00") {
		return errors.New("安全姿态复核没有回显原 checkpoint scopes")
	}
	if len(bundle.DatabaseKeys) != 0 || len(bundle.DatabaseProfiles) != 0 || bundle.DatabaseCredential != nil || bundle.ImageKeys != nil ||
		bundle.CatalogID != "" || len(bundle.CatalogEntries) != 0 || len(bundle.Profiles) != 0 {
		return errors.New("安全姿态复核意外返回了 acquisition 数据")
	}
	routes, routesErr := diagnosticStringList(bundle.Diagnostics, "routes_attempted")
	if diagnosticString(bundle.Diagnostics, "platform") != "darwin" ||
		diagnosticString(bundle.Diagnostics, "action_stage") != "security_posture_revalidation" ||
		diagnosticString(bundle.Diagnostics, "database_target_status") != "not_requested" ||
		diagnosticString(bundle.Diagnostics, "database_coverage_status") != "not_requested" ||
		diagnosticString(bundle.Diagnostics, "media_coverage_status") != "not_requested" ||
		routesErr != nil || len(routes) != 0 || diagnosticString(bundle.Diagnostics, "session_id") != "" {
		return errors.New("安全姿态复核混入了 acquisition session 或 route 状态")
	}
	for _, field := range []string{"route_selected", "process_instance_id", "process_access_status", "process_access_error", "process_discovery_method", "helper_status"} {
		if raw, present := bundle.Diagnostics[field]; present {
			value, valid := raw.(string)
			if !valid || value != "" {
				return errors.New("安全姿态复核混入了进程访问或 helper 状态")
			}
		}
	}
	sources, sourcesErr := diagnosticStringList(bundle.Diagnostics, "candidate_sources")
	if diagnosticString(bundle.Diagnostics, "candidate_mode") != "none" || sourcesErr != nil || len(sources) != 0 {
		return errors.New("安全姿态复核混入了候选采集状态")
	}
	for _, field := range []string{"process_count", "opened_process_count", "access_denied_count", "hook_target_found", "hook_capture_count", "scanned_bytes", "candidate_count", "raw_key_candidate_count", "validated_database_candidate_count"} {
		if _, present := bundle.Diagnostics[field]; !present {
			continue
		}
		count, valid := diagnosticInteger(bundle.Diagnostics, field)
		if !valid || count != 0 {
			return errors.New("安全姿态复核报告了不允许的进程扫描活动")
		}
	}
	for _, field := range []string{"hook_installed", "dynamic_hook_used", "static_scan_fallback"} {
		if active, present := bundle.Diagnostics[field]; present {
			value, valid := active.(bool)
			if !valid || value {
				return errors.New("安全姿态复核报告了不允许的 hook 活动")
			}
		}
	}
	posture := diagnosticString(bundle.Diagnostics, "security_posture_status")
	resultCode := diagnosticString(bundle.Diagnostics, "result_code")
	workflowStatus := diagnosticString(bundle.Diagnostics, "workflow_status")
	nextAction := diagnosticString(bundle.Diagnostics, "next_action")
	if posture == "sip_enabled_verified" && resultCode == "complete" && workflowStatus == "terminal" && nextAction == "none" {
		return nil
	}
	if posture == "restoration_required" && resultCode == "action_required" && workflowStatus == "waiting_action" && nextAction == "reenable_sip" {
		return nil
	}
	if posture == "not_evaluated" && resultCode == "unsupported" && workflowStatus == "blocked" && nextAction == "stop_and_report" {
		for _, reason := range diagnosticStrings(bundle.Diagnostics, "blocking_reasons") {
			if reason == "security_posture_not_verified" {
				return nil
			}
		}
	}
	return errors.New("Provider 没有返回可验证的 SIP 恢复状态")
}

func revalidateSecurityPostureOneShot(parent context.Context, path string, account localplatform.Account, scopes []string, catalogKey string) (CandidateBundle, error) {
	requestID, err := newRequestID()
	if err != nil {
		return CandidateBundle{}, errors.New("无法生成安全姿态复核请求标识")
	}
	request := acquireRequest{
		Protocol: Protocol, RequestID: requestID, Action: "acquire", CatalogKey: catalogKey,
		AccountDir: account.Path, DBDir: account.DBDir, Scopes: append([]string(nil), scopes...),
		DeadlineMS: acquireBudget.Milliseconds(), Workflow: workflowRequest{Operation: "revalidate_security_posture"},
	}
	bundle, err := executeOneShotProviderRequest(parent, path, request)
	if err != nil {
		return CandidateBundle{}, err
	}
	if err := validateSecurityPostureRevalidation(bundle, scopes); err != nil {
		return CandidateBundle{}, err
	}
	if diagnosticString(bundle.Diagnostics, "security_posture_status") != "sip_enabled_verified" {
		return bundle, acquisitionError(bundle.Diagnostics)
	}
	return bundle, nil
}

// IsSecurityPostureRevalidation 判断结果是否为成功且不涉及凭据的重新验证结果，
// setup 用它在不重新采集密钥的情况下结束外部恢复流程。
func IsSecurityPostureRevalidation(bundle CandidateBundle) bool {
	scopes, err := diagnosticStringList(bundle.Diagnostics, "requested_scopes")
	if err != nil || validateSecurityPostureRevalidation(bundle, scopes) != nil {
		return false
	}
	return diagnosticString(bundle.Diagnostics, "action_stage") == "security_posture_revalidation" &&
		diagnosticString(bundle.Diagnostics, "security_posture_status") == "sip_enabled_verified" &&
		diagnosticString(bundle.Diagnostics, "result_code") == "complete" &&
		diagnosticString(bundle.Diagnostics, "workflow_status") == "terminal" &&
		diagnosticString(bundle.Diagnostics, "next_action") == "none"
}

func diagnosticString(values map[string]any, name string) string {
	value, _ := values[name].(string)
	return value
}

func diagnosticStrings(values map[string]any, name string) []string {
	if direct, ok := values[name].([]string); ok {
		return append([]string(nil), direct...)
	}
	raw, _ := values[name].([]any)
	result := make([]string, 0, len(raw))
	for _, value := range raw {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func normalizeRequestedScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return nil, errors.New("密钥请求至少需要一个 scope")
	}
	database := false
	media := false
	for _, scope := range scopes {
		switch scope {
		case "database":
			if database {
				return nil, errors.New("密钥请求包含重复的 database scope")
			}
			database = true
		case "media":
			if media {
				return nil, errors.New("密钥请求包含重复的 media scope")
			}
			media = true
		default:
			return nil, fmt.Errorf("密钥请求包含不支持的 scope %q", scope)
		}
	}
	result := make([]string, 0, 2)
	if database {
		result = append(result, "database")
	}
	if media {
		result = append(result, "media")
	}
	return result, nil
}

func diagnosticStringList(values map[string]any, name string) ([]string, error) {
	value, present := values[name]
	if !present {
		return nil, fmt.Errorf("Provider diagnostics 缺少 %s", name)
	}
	switch list := value.(type) {
	case []string:
		return append([]string(nil), list...), nil
	case []any:
		result := make([]string, 0, len(list))
		for _, item := range list {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("Provider diagnostics %s 包含非字符串值", name)
			}
			result = append(result, text)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("Provider diagnostics %s 不是字符串数组", name)
	}
}

func validateScopeDiagnostics(bundle *CandidateBundle) error {
	if bundle.Protocol != Protocol {
		return nil
	}
	if len(bundle.Diagnostics) == 0 {
		return errors.New("Provider v1 响应缺少 diagnostics")
	}
	if _, present := bundle.Diagnostics["coverage_status"]; present {
		return errors.New("Provider v1 响应仍使用已移除的 coverage_status")
	}
	if _, present := bundle.Diagnostics["media_status"]; present {
		return errors.New("Provider v1 响应仍使用已移除的 media_status")
	}
	requested, err := diagnosticStringList(bundle.Diagnostics, "requested_scopes")
	if err != nil {
		return err
	}
	canonical, err := normalizeRequestedScopes(requested)
	if err != nil || strings.Join(requested, "\x00") != strings.Join(canonical, "\x00") {
		return errors.New("Provider diagnostics requested_scopes 无效、重复或未按规范排序")
	}
	databaseRequested := false
	mediaRequested := false
	for _, scope := range requested {
		databaseRequested = databaseRequested || scope == "database"
		mediaRequested = mediaRequested || scope == "media"
	}
	databaseCoverage := diagnosticString(bundle.Diagnostics, "database_coverage_status")
	mediaCoverage := diagnosticString(bundle.Diagnostics, "media_coverage_status")
	databaseTarget := diagnosticString(bundle.Diagnostics, "database_target_status")
	securityPosture := diagnosticString(bundle.Diagnostics, "security_posture_status")
	shadowRouteStatus := diagnosticString(bundle.Diagnostics, "shadow_route_status")
	routePriority, routePriorityErr := diagnosticStringList(bundle.Diagnostics, "route_priority")
	if routePriorityErr != nil {
		return routePriorityErr
	}
	routesAttempted, routesAttemptedErr := diagnosticStringList(bundle.Diagnostics, "routes_attempted")
	if routesAttemptedErr != nil {
		return routesAttemptedErr
	}
	nextAction := diagnosticString(bundle.Diagnostics, "next_action")
	targetBinding := diagnosticString(bundle.Diagnostics, "target_binding_status")
	sessionAccount := diagnosticString(bundle.Diagnostics, "session_account_status")
	candidateMode := diagnosticString(bundle.Diagnostics, "candidate_mode")
	validDatabaseCoverage := databaseCoverage == "not_requested" || databaseCoverage == "none" ||
		databaseCoverage == "partial" || databaseCoverage == "complete"
	validMediaCoverage := mediaCoverage == "not_requested" || mediaCoverage == "pending" ||
		mediaCoverage == "none" || mediaCoverage == "complete"
	if !validDatabaseCoverage || !validMediaCoverage {
		return errors.New("Provider diagnostics 包含无效的 scope coverage 状态")
	}
	validDatabaseTarget := databaseTarget == "not_requested" || databaseTarget == "none" || databaseTarget == "present"
	validSecurityPosture := securityPosture == "not_applicable" || securityPosture == "not_evaluated" ||
		securityPosture == "sip_enabled_verified" || securityPosture == "sip_disabled_verified" || securityPosture == "restoration_required"
	validShadowRouteStatus := map[string]bool{
		"not_applicable": true, "not_evaluated": true, "unavailable_in_build": true,
		"unsupported_for_target": true, "available": true, "awaiting_approval": true,
		"attempted_failed": true, "succeeded": true,
	}[shadowRouteStatus]
	validNextAction := map[string]bool{
		"none": true, "trigger_database": true, "restart_wechat": true, "relogin_wechat": true,
		"switch_to_target_account": true, "approve_shadow_mode": true, "disable_sip": true,
		"reenable_sip": true, "fix_permission": true, "stop_and_report": true,
	}[nextAction]
	validTargetBinding := map[string]bool{"unknown": true, "hmac_verified": true, "path_verified": true, "mismatch": true}[targetBinding]
	validSessionAccount := map[string]bool{"unknown": true, "known_target": true, "known_other": true}[sessionAccount]
	validCandidateMode := map[string]bool{"none": true, "global_passphrase": true, "per_database_enc_key": true, "mixed": true}[candidateMode]
	if !validDatabaseTarget || !validSecurityPosture || !validShadowRouteStatus || !validNextAction || !validTargetBinding || !validSessionAccount || !validCandidateMode {
		return errors.New("Provider diagnostics 包含未知或缺失的稳定状态枚举")
	}
	platform := diagnosticString(bundle.Diagnostics, "platform")
	if platform == "darwin" {
		if shadowRouteStatus == "not_applicable" || strings.Join(routePriority, "\x00") != "standard\x00shadow\x00sip_disabled" {
			return errors.New("Provider diagnostics 的 macOS route_priority 或 Shadow 状态无效")
		}
		if err := validateDarwinEvidence(bundle.Diagnostics); err != nil {
			return err
		}
	} else if platform != "windows" {
		return errors.New("Provider diagnostics 包含未知平台")
	} else if shadowRouteStatus != "not_applicable" || len(routePriority) != 0 {
		return errors.New("Provider diagnostics 在非 macOS 平台声明了 Shadow 路线")
	}
	validRoutes := map[string]bool{
		"darwin_arm64_standard_dynamic": true, "darwin_amd64_standard_dynamic": true,
		"darwin_arm64_shadow_dynamic": true, "darwin_amd64_shadow_dynamic": true,
		"darwin_arm64_sip_disabled": true, "darwin_amd64_sip_disabled": true,
		"darwin_standard_dynamic_waitfor": true, "darwin_sip_disabled_waitfor": true,
		"darwin_static_fallback": true,
		"windows_config_cipher":  true, "windows_memory_fallback": true,
	}
	shadowAttempted := false
	sipDisabledRouteAttempted := false
	seenRoutes := map[string]bool{}
	for _, route := range routesAttempted {
		route = strings.TrimSpace(route)
		if route == "" || seenRoutes[route] || !validRoutes[route] ||
			platform == "darwin" && !strings.HasPrefix(route, "darwin_") ||
			platform == "windows" && !strings.HasPrefix(route, "windows_") {
			return errors.New("Provider diagnostics 的 routes_attempted 包含未知、跨平台或重复 route")
		}
		seenRoutes[route] = true
		shadowAttempted = shadowAttempted || route == "darwin_arm64_shadow_dynamic" || route == "darwin_amd64_shadow_dynamic"
		sipDisabledRouteAttempted = sipDisabledRouteAttempted || route == "darwin_arm64_sip_disabled" ||
			route == "darwin_amd64_sip_disabled" || route == "darwin_sip_disabled_waitfor"
	}
	if platform == "windows" {
		if err := validateWindowsEvidence(bundle.Diagnostics, routesAttempted); err != nil {
			return err
		}
	}
	routeSelected := diagnosticString(bundle.Diagnostics, "route_selected")
	if raw, present := bundle.Diagnostics["route_selected"]; present {
		if _, valid := raw.(string); !valid {
			return errors.New("Provider diagnostics 的 route_selected 不是字符串")
		}
	}
	if routeSelected != "" && !seenRoutes[routeSelected] {
		return errors.New("Provider diagnostics 的 route_selected 不在 routes_attempted 执行历史中")
	}
	if shadowAttempted != (shadowRouteStatus == "attempted_failed" || shadowRouteStatus == "succeeded") {
		return errors.New("Provider diagnostics 的 Shadow 状态与 routes_attempted 执行历史矛盾")
	}
	if databaseRequested == (databaseCoverage == "not_requested") || mediaRequested == (mediaCoverage == "not_requested") {
		return errors.New("Provider diagnostics 的 requested_scopes 与 scope coverage 状态不一致")
	}
	if databaseRequested == (databaseTarget == "not_requested") || databaseTarget == "none" && databaseCoverage != "none" {
		return errors.New("Provider diagnostics 的 database_target_status 与数据库 scope/coverage 不一致")
	}
	if databaseRequested && ((len(bundle.CatalogEntries) == 0) != (databaseTarget == "none")) {
		return errors.New("Provider diagnostics 的 database_target_status 与 catalog 目标集合不一致")
	}
	if databaseRequested && (!validSecretHex(bundle.CatalogID) || len(bundle.CatalogID) != 64) {
		return errors.New("Provider diagnostics 的数据库 scope 缺少有效 catalog_id")
	}
	if bundle.ImageKeys == nil && mediaCoverage == "complete" {
		return errors.New("Provider diagnostics media_coverage_status=complete 但缺少已验真的媒体凭据")
	}
	if bundle.ImageKeys != nil && mediaCoverage != "complete" {
		return errors.New("Provider diagnostics media_coverage_status 未完成却携带媒体凭据")
	}
	resultCode := diagnosticString(bundle.Diagnostics, "result_code")
	workflowStatus := diagnosticString(bundle.Diagnostics, "workflow_status")
	validResultCode := resultCode == "complete" || resultCode == "partial" || resultCode == "action_required" ||
		resultCode == "permission_required" || resultCode == "ambiguous" || resultCode == "unsupported" ||
		resultCode == "deadline_exhausted" || resultCode == "cancelled" || resultCode == "failed"
	validWorkflowStatus := workflowStatus == "running" || workflowStatus == "waiting_action" ||
		workflowStatus == "blocked" || workflowStatus == "terminal"
	if !validResultCode || !validWorkflowStatus {
		return errors.New("Provider diagnostics 缺少整体结果或工作流状态")
	}
	if resultCode == "complete" {
		if workflowStatus != "terminal" || databaseRequested && databaseCoverage != "complete" ||
			mediaRequested && mediaCoverage != "complete" || nextAction != "none" {
			return errors.New("Provider diagnostics result_code=complete 但 requested scope 尚未全部完成")
		}
	}
	if targetBinding == "mismatch" || sessionAccount == "known_other" {
		if resultCode != "action_required" || workflowStatus != "waiting_action" || nextAction != "switch_to_target_account" {
			return errors.New("Provider diagnostics 的账号错配状态没有 fail-closed")
		}
	}
	if securityPosture == "restoration_required" &&
		(platform != "darwin" || resultCode != "action_required" || workflowStatus != "waiting_action" || nextAction != "reenable_sip") {
		return errors.New("Provider diagnostics 的安全姿态恢复状态没有绑定 reenable_sip")
	}
	blockingReasons := diagnosticStrings(bundle.Diagnostics, "blocking_reasons")
	containsReason := func(wanted string) bool {
		for _, reason := range blockingReasons {
			if reason == wanted {
				return true
			}
		}
		return false
	}
	for reason, requiredStatus := range map[string]string{
		"shadow_route_unavailable_in_build":   "unavailable_in_build",
		"shadow_route_unsupported_for_target": "unsupported_for_target",
		"shadow_route_failed":                 "attempted_failed",
	} {
		if containsReason(reason) && shadowRouteStatus != requiredStatus {
			return errors.New("Provider diagnostics 的 Shadow blocking_reason 与 shadow_route_status 矛盾")
		}
	}
	if nextAction == "approve_shadow_mode" {
		if platform != "darwin" || securityPosture != "sip_enabled_verified" || shadowRouteStatus != "awaiting_approval" ||
			resultCode != "action_required" || workflowStatus != "waiting_action" || !containsReason("standard_route_unavailable") {
			return errors.New("Provider diagnostics 的 Shadow 动作缺少标准路线失败机器证据")
		}
	}
	if nextAction == "disable_sip" {
		shadowFallbackReason := map[string]string{
			"unavailable_in_build":   "shadow_route_unavailable_in_build",
			"unsupported_for_target": "shadow_route_unsupported_for_target",
			"attempted_failed":       "shadow_route_failed",
		}[shadowRouteStatus]
		if platform != "darwin" || securityPosture != "sip_enabled_verified" ||
			diagnosticString(bundle.Diagnostics, "process_access_status") != "denied" ||
			diagnosticString(bundle.Diagnostics, "process_access_error") != "sip_enabled" ||
			resultCode != "action_required" || workflowStatus != "waiting_action" || shadowFallbackReason == "" ||
			!containsReason("standard_route_unavailable") || !containsReason(shadowFallbackReason) {
			return errors.New("Provider diagnostics 的 SIP 动作缺少标准失败与终态 Shadow 路由证据")
		}
	}
	if nextAction == "reenable_sip" {
		if platform != "darwin" || securityPosture != "restoration_required" ||
			resultCode != "action_required" || workflowStatus != "waiting_action" {
			return errors.New("Provider diagnostics 的 SIP 恢复动作缺少 macOS 安全姿态证据")
		}
		completeCoverage := hasCompleteRequestedCoverage(bundle.Diagnostics)
		routeFailed := containsReason("sip_route_failed")
		routeNotAttempted := containsReason("sip_disabled_route_not_attempted")
		if completeCoverage {
			if routeFailed || routeNotAttempted {
				return errors.New("Provider diagnostics 的完整 SIP 获取仍携带失败标记")
			}
		} else {
			if routeFailed == routeNotAttempted {
				return errors.New("Provider diagnostics 的未完成 SIP 恢复动作没有唯一的路由阶段证据")
			}
			if routeFailed && !sipDisabledRouteAttempted {
				return errors.New("Provider diagnostics 的 SIP 路线失败没有对应 routes_attempted 证据")
			}
			if routeNotAttempted && sipDisabledRouteAttempted {
				return errors.New("Provider diagnostics 声称 SIP 路线未启动但 routes_attempted 已包含该路线")
			}
		}
	}
	if nextAction != "none" && nextAction != "stop_and_report" && workflowStatus != "waiting_action" && workflowStatus != "blocked" {
		return errors.New("Provider diagnostics 的 next_action 与工作流状态不一致")
	}
	for _, reason := range blockingReasons {
		if !validBlockingReason(reason) {
			return fmt.Errorf("Provider diagnostics 包含未知 blocking_reason %q", reason)
		}
	}
	if mediaCoverage == "pending" && workflowStatus == "terminal" {
		return errors.New("Provider diagnostics 在 terminal 工作流中保留了 pending media coverage")
	}
	return nil
}

func validBlockingReason(value string) bool {
	return map[string]bool{
		"account_mismatch": true, "database_targets_not_found": true, "hook_not_triggered": true,
		"database_open_required": true, "login_time_derivation_required": true, "wechat_not_running": true,
		"process_access_denied": true, "process_identity_untrusted": true, "validator_conflict": true,
		"candidate_ambiguous": true, "deadline_exhausted": true, "action_receipt_required": true,
		"user_cancelled": true, "catalog_drift": true, "acquisition_request_in_progress": true,
		"action_receipt_rejected": true, "duplicate_action_without_state_change": true,
		"action_retry_budget_exhausted": true, "user_declined_action": true,
		"standard_route_unavailable": true, "shadow_route_failed": true,
		"shadow_route_unavailable_in_build": true, "shadow_route_unsupported_for_target": true,
		"shadow_route_not_evaluated": true, "security_posture_not_verified": true, "helper_untrusted": true,
		"user_declined_security_change": true, "sip_route_failed": true,
		"sip_disabled_route_not_attempted": true,
	}[value]
}

func validateDarwinEvidence(values map[string]any) error {
	fingerprintStatus := diagnosticString(values, "binary_fingerprint_status")
	signingStatus := diagnosticString(values, "binary_signing_status")
	architecture := diagnosticString(values, "process_architecture")
	architectureStatus := diagnosticString(values, "process_architecture_status")
	translationStatus := diagnosticString(values, "process_translation_status")
	registryStatus := diagnosticString(values, "compatibility_registry_status")
	standardStatus := diagnosticString(values, "standard_route_status")
	standardEvidence, err := diagnosticStringList(values, "standard_route_evidence")
	if err != nil {
		return err
	}
	if !map[string]bool{"not_evaluated": true, "verified": true, "unavailable": true}[fingerprintStatus] ||
		!map[string]bool{"not_evaluated": true, "verified": true, "invalid": true, "unavailable": true}[signingStatus] ||
		!map[string]bool{"not_evaluated": true, "verified_running_process": true, "unavailable": true}[architectureStatus] ||
		!map[string]bool{"not_evaluated": true, "native": true, "translated": true, "unknown": true}[translationStatus] ||
		!map[string]bool{"not_evaluated": true, "registered_supported": true, "registered_unsupported": true, "unregistered": true, "rejected_untrusted_binary": true}[registryStatus] ||
		!map[string]bool{"not_evaluated": true, "eligible_registered": true, "eligible_generic_dynamic": true, "unsupported_for_target": true}[standardStatus] {
		return errors.New("Provider diagnostics 包含未知或缺失的 macOS Phase 3 证据状态")
	}
	hash := strings.ToLower(diagnosticString(values, "executable_sha256"))
	teamID := diagnosticString(values, "signing_team_id")
	requirementHash := strings.ToLower(diagnosticString(values, "designated_requirement_sha256"))
	if fingerprintStatus == "verified" {
		if len(hash) != 64 || !validSecretHex(hash) {
			return errors.New("Provider diagnostics 的已验证 macOS binary fingerprint 无效")
		}
	} else if hash != "" {
		return errors.New("Provider diagnostics 在 fingerprint 未验证时携带 executable_sha256")
	}
	if signingStatus == "verified" {
		if teamID == "" || len(teamID) > 64 || strings.ContainsAny(teamID, " \t\r\n") || len(requirementHash) != 64 || !validSecretHex(requirementHash) {
			return errors.New("Provider diagnostics 的已验证 macOS 签名身份不完整")
		}
	} else if teamID != "" || requirementHash != "" {
		return errors.New("Provider diagnostics 在签名未验证时携带签名身份")
	}
	if architectureStatus == "verified_running_process" {
		if (architecture != "amd64" && architecture != "arm64") || translationStatus == "not_evaluated" {
			return errors.New("Provider diagnostics 的目标进程实际架构证据不完整")
		}
	} else if architecture != "unknown" || translationStatus != "not_evaluated" && translationStatus != "unknown" {
		return errors.New("Provider diagnostics 把未验证的目标进程架构写成确定值")
	}
	fullyVerified := architectureStatus == "verified_running_process" && fingerprintStatus == "verified" && signingStatus == "verified" &&
		diagnosticString(values, "wechat_version") != "" && diagnosticString(values, "wechat_build") != "" && diagnosticString(values, "macos_version") != ""
	standardEvidenceSet := map[string]bool{}
	allowedStandardEvidence := map[string]bool{
		"registry_exact_match": true, "registry_candidate_entry": true,
		"real_device_evidence_present": true, "release_promotion_verified": true,
		"generic_symbol_route_only": true, "registry_no_exact_match": true,
		"release_requires_registry_exact_match": true, "registry_entry_not_supported": true,
		"process_architecture_not_verified": true, "binary_fingerprint_not_verified": true,
		"binary_signing_invalid": true, "binary_signing_not_verified": true,
		"wechat_version_not_verified": true, "wechat_build_not_verified": true, "macos_version_not_verified": true,
	}
	for _, item := range standardEvidence {
		if !allowedStandardEvidence[item] || standardEvidenceSet[item] {
			return errors.New("Provider diagnostics 的 macOS standard route evidence 包含未知、空白或重复值")
		}
		standardEvidenceSet[item] = true
	}
	switch standardStatus {
	case "eligible_registered":
		promotionBound := standardEvidenceSet["real_device_evidence_present"] && standardEvidenceSet["release_promotion_verified"]
		candidateBound := standardEvidenceSet["registry_candidate_entry"]
		if registryStatus != "registered_supported" || !fullyVerified || !standardEvidenceSet["registry_exact_match"] ||
			releaseBuild() && !promotionBound || !releaseBuild() && !promotionBound && !candidateBound {
			return errors.New("Provider diagnostics 把非精确登记构建声明为 registered standard route")
		}
	case "eligible_generic_dynamic":
		if registryStatus != "unregistered" || !fullyVerified || len(standardEvidence) == 0 {
			return errors.New("Provider diagnostics 的通用 macOS route 缺少完整机器证据")
		}
	case "unsupported_for_target":
		if registryStatus != "registered_unsupported" && registryStatus != "rejected_untrusted_binary" || len(standardEvidence) == 0 {
			return errors.New("Provider diagnostics 的 macOS unsupported route 缺少拒绝证据")
		}
	case "not_evaluated":
		if registryStatus != "not_evaluated" {
			return errors.New("Provider diagnostics 的 macOS registry 与 standard route 状态矛盾")
		}
	}
	for _, route := range diagnosticStrings(values, "routes_attempted") {
		lower := strings.ToLower(route)
		if strings.Contains(lower, "darwin_amd64_") && architectureStatus == "verified_running_process" && architecture != "amd64" ||
			strings.Contains(lower, "darwin_arm64_") && architectureStatus == "verified_running_process" && architecture != "arm64" {
			return errors.New("Provider diagnostics 的 route ABI 与目标进程实际架构矛盾")
		}
	}
	return nil
}

func diagnosticBoolean(values map[string]any, name string) (bool, bool) {
	value, found := values[name]
	if !found {
		return false, false
	}
	result, valid := value.(bool)
	return result, valid
}

func diagnosticIntegerMap(values map[string]any, name string) (map[string]int, error) {
	raw, found := values[name]
	if !found {
		return nil, fmt.Errorf("Provider diagnostics 缺少 %s", name)
	}
	result := map[string]int{}
	switch entries := raw.(type) {
	case map[string]int:
		for key, value := range entries {
			result[key] = value
		}
	case map[string]any:
		for key := range entries {
			value, valid := diagnosticInteger(entries, key)
			if !valid {
				return nil, fmt.Errorf("Provider diagnostics %s 包含非整数计数", name)
			}
			result[key] = value
		}
	default:
		return nil, fmt.Errorf("Provider diagnostics %s 不是计数对象", name)
	}
	return result, nil
}

func validateWindowsEvidence(values map[string]any, routesAttempted []string) error {
	fingerprintStatus := diagnosticString(values, "binary_fingerprint_status")
	signingStatus := diagnosticString(values, "binary_signing_status")
	architecture := diagnosticString(values, "process_architecture")
	architectureStatus := diagnosticString(values, "process_architecture_status")
	registryStatus := diagnosticString(values, "compatibility_registry_status")
	configStatus := diagnosticString(values, "config_cipher_route_status")
	routeEvidence, err := diagnosticStringList(values, "windows_route_evidence")
	if err != nil {
		return err
	}
	if !map[string]bool{"not_evaluated": true, "verified": true, "unavailable": true}[fingerprintStatus] ||
		!map[string]bool{"not_evaluated": true, "verified": true, "invalid": true, "unavailable": true}[signingStatus] ||
		!map[string]bool{"not_evaluated": true, "verified_running_process": true, "unavailable": true}[architectureStatus] ||
		!map[string]bool{"not_evaluated": true, "registered_supported": true, "registered_unsupported": true, "unregistered": true, "rejected_untrusted_binary": true}[registryStatus] ||
		!map[string]bool{
			"not_evaluated": true, "unavailable_unregistered": true, "unavailable_untrusted_binary": true,
			"eligible_registered": true, "registered_reviewed_no_structure": true,
			"attempted_no_structure": true, "attempted_invalid_structure": true,
			"attempted_no_verified_candidate": true, "partial": true, "succeeded": true,
		}[configStatus] {
		return errors.New("Provider diagnostics 包含未知或缺失的 Windows Phase 4 证据状态")
	}
	allowedEvidence := map[string]bool{
		"process_architecture_not_verified": true, "binary_fingerprint_not_verified": true,
		"wechat_version_not_verified": true, "wechat_build_not_verified": true,
		"product_identity_not_verified": true, "binary_signing_invalid": true,
		"binary_signing_not_verified": true, "registry_exact_match": true,
		"real_device_evidence_present": true, "release_promotion_verified": true,
		"registry_candidate_entry": true, "registry_entry_not_supported": true,
		"registry_no_exact_match": true, "registered_profiles_do_not_cover_missing_catalog": true,
		"process_instance_not_verified":           true,
		"multiple_process_architectures_observed": true,
	}
	evidenceSet := map[string]bool{}
	for _, item := range routeEvidence {
		if !allowedEvidence[item] || evidenceSet[item] {
			return errors.New("Provider diagnostics 的 Windows route evidence 包含未知、空白或重复值")
		}
		evidenceSet[item] = true
	}

	countNames := []string{
		"process_count", "selected_process_count", "target_bound_process_count", "other_account_process_count",
		"unknown_account_process_count", "opened_process_count", "access_denied_count", "per_process_collector_count",
		"config_cipher_structure_count", "config_cipher_invalid_structure_count", "config_cipher_candidate_count",
		"config_cipher_verified_candidate_count", "fallback_candidate_count",
	}
	counts := map[string]int{}
	for _, name := range countNames {
		count, valid := diagnosticInteger(values, name)
		if !valid || count < 0 {
			return fmt.Errorf("Provider diagnostics 的 Windows %s 缺失或不是非负整数", name)
		}
		counts[name] = count
	}
	processCount := counts["process_count"]
	selectedCount := counts["selected_process_count"]
	targetCount := counts["target_bound_process_count"]
	otherCount := counts["other_account_process_count"]
	unknownCount := counts["unknown_account_process_count"]
	if processCount != targetCount+otherCount+unknownCount || selectedCount != targetCount+unknownCount ||
		counts["opened_process_count"]+counts["access_denied_count"] != selectedCount {
		return errors.New("Provider diagnostics 的 Windows 进程、账号绑定或访问计数互相矛盾")
	}
	targetBinding := diagnosticString(values, "target_binding_status")
	sessionAccount := diagnosticString(values, "session_account_status")
	if targetCount > 0 && (targetBinding != "path_verified" || sessionAccount != "known_target") {
		return errors.New("Provider diagnostics 的 Windows 目标路径证据没有绑定目标账号")
	}
	if targetCount == 0 && (targetBinding == "path_verified" || sessionAccount == "known_target") {
		return errors.New("Provider diagnostics 在没有目标进程路径时声明了 Windows 目标账号")
	}
	if (targetBinding == "mismatch") != (sessionAccount == "known_other") {
		return errors.New("Provider diagnostics 的 Windows mismatch 与 session account 状态不成对")
	}
	if targetBinding == "mismatch" || sessionAccount == "known_other" {
		if otherCount == 0 || targetCount != 0 || unknownCount != 0 {
			return errors.New("Provider diagnostics 的 Windows 账号错配缺少排他的进程路径证据")
		}
	}

	hash := diagnosticString(values, "executable_sha256")
	signerHash := diagnosticString(values, "binary_signer_sha256")
	if fingerprintStatus == "verified" {
		if hash != strings.ToLower(hash) || len(hash) != 64 || !validSecretHex(hash) {
			return errors.New("Provider diagnostics 的已验证 Windows binary fingerprint 无效")
		}
	} else if hash != "" {
		return errors.New("Provider diagnostics 在 Windows fingerprint 未验证时携带 executable_sha256")
	}
	if signingStatus == "verified" {
		if signerHash != strings.ToLower(signerHash) || len(signerHash) != 64 || !validSecretHex(signerHash) {
			return errors.New("Provider diagnostics 的已验证 Windows 签名身份不完整")
		}
	} else if signerHash != "" {
		return errors.New("Provider diagnostics 在 Windows 签名未验证时携带 signer fingerprint")
	}
	if translation := diagnosticString(values, "process_translation_status"); translation != "" && translation != "not_applicable" {
		return errors.New("Provider diagnostics 在 Windows 平台声明了无效的进程翻译状态")
	}
	if architectureStatus == "verified_running_process" {
		if !map[string]bool{"amd64": true, "arm64": true, "x86": true, "mixed": true}[architecture] ||
			architecture == "mixed" && selectedCount < 2 {
			return errors.New("Provider diagnostics 的 Windows 目标进程实际架构证据不完整")
		}
	} else if architecture != "unknown" {
		return errors.New("Provider diagnostics 把未验证的 Windows 进程架构写成确定值")
	}

	version := diagnosticString(values, "wechat_version")
	build := diagnosticString(values, "wechat_build")
	product := diagnosticString(values, "binary_product_identity")
	if product != "" && product != "weixin.exe" && product != "wechat.exe" {
		return errors.New("Provider diagnostics 的 Windows product identity 不是允许的目标进程标识")
	}
	fullyVerified := architectureStatus == "verified_running_process" &&
		(architecture == "amd64" || architecture == "arm64" || architecture == "mixed") &&
		fingerprintStatus == "verified" && signingStatus == "verified" &&
		version != "" && build != "" && product != ""
	switch registryStatus {
	case "registered_supported":
		promotionBound := evidenceSet["real_device_evidence_present"] && evidenceSet["release_promotion_verified"]
		candidateBound := evidenceSet["registry_candidate_entry"]
		if !fullyVerified || !evidenceSet["registry_exact_match"] ||
			releaseBuild() && !promotionBound || !releaseBuild() && !promotionBound && !candidateBound {
			return errors.New("Provider diagnostics 把缺少精确真机证据的 Windows 构建声明为 registered")
		}
	case "registered_unsupported":
		if !fullyVerified || !evidenceSet["registry_entry_not_supported"] {
			return errors.New("Provider diagnostics 的 Windows rejected registry entry 缺少完整机器证据")
		}
	case "unregistered":
		if !fullyVerified || !evidenceSet["registry_no_exact_match"] {
			return errors.New("Provider diagnostics 的 Windows unregistered 状态缺少完整 fingerprint 证据")
		}
	case "rejected_untrusted_binary":
		if signingStatus != "invalid" || !evidenceSet["binary_signing_invalid"] {
			return errors.New("Provider diagnostics 的 Windows 不可信二进制拒绝状态缺少签名失败证据")
		}
	case "not_evaluated":
		if configStatus != "not_evaluated" {
			return errors.New("Provider diagnostics 的 Windows registry 与 Config.Cipher 状态矛盾")
		}
	}

	configRouteIndex := -1
	fallbackRouteIndex := -1
	for index, route := range routesAttempted {
		switch route {
		case "windows_config_cipher":
			configRouteIndex = index
		case "windows_memory_fallback":
			fallbackRouteIndex = index
		}
	}
	configAttempted := map[string]bool{
		"attempted_no_structure": true, "attempted_invalid_structure": true,
		"attempted_no_verified_candidate": true, "partial": true, "succeeded": true,
	}[configStatus]
	switch configStatus {
	case "unavailable_unregistered":
		if registryStatus != "unregistered" && registryStatus != "registered_unsupported" {
			return errors.New("Provider diagnostics 的 Config.Cipher unavailable 状态与 registry 矛盾")
		}
	case "unavailable_untrusted_binary":
		if registryStatus != "rejected_untrusted_binary" {
			return errors.New("Provider diagnostics 的 Config.Cipher untrusted 状态与签名证据矛盾")
		}
	case "eligible_registered", "registered_reviewed_no_structure", "attempted_no_structure", "attempted_invalid_structure", "attempted_no_verified_candidate", "partial", "succeeded":
		if registryStatus != "registered_supported" {
			return errors.New("Provider diagnostics 在未精确登记的 Windows 构建上声明 Config.Cipher 可用")
		}
	}
	if configAttempted != (configRouteIndex >= 0) {
		return errors.New("Provider diagnostics 的 Config.Cipher 状态与 routes_attempted 矛盾")
	}
	structureCount := counts["config_cipher_structure_count"]
	invalidCount := counts["config_cipher_invalid_structure_count"]
	candidateCount := counts["config_cipher_candidate_count"]
	verifiedCount := counts["config_cipher_verified_candidate_count"]
	if invalidCount > structureCount || candidateCount > structureCount || verifiedCount > candidateCount {
		return errors.New("Provider diagnostics 的 Config.Cipher 结构、候选和验证计数矛盾")
	}
	switch configStatus {
	case "not_evaluated", "unavailable_unregistered", "unavailable_untrusted_binary", "eligible_registered", "registered_reviewed_no_structure":
		if structureCount != 0 || invalidCount != 0 || candidateCount != 0 || verifiedCount != 0 {
			return errors.New("Provider diagnostics 在未执行 Config.Cipher 时报告了扫描计数")
		}
	case "attempted_no_structure":
		if structureCount != 0 {
			return errors.New("Provider diagnostics 的 Config.Cipher no-structure 状态携带结构计数")
		}
	case "attempted_invalid_structure":
		if invalidCount == 0 || candidateCount != 0 || verifiedCount != 0 {
			return errors.New("Provider diagnostics 的 Config.Cipher invalid-structure 计数矛盾")
		}
	case "attempted_no_verified_candidate":
		if structureCount == 0 || verifiedCount != 0 {
			return errors.New("Provider diagnostics 的 Config.Cipher unverified 状态缺少结构证据")
		}
	case "partial", "succeeded":
		if verifiedCount == 0 {
			return errors.New("Provider diagnostics 的 Config.Cipher 成功状态没有已验证候选")
		}
	}

	staticFallback, fallbackPresent := diagnosticBoolean(values, "static_scan_fallback")
	if !fallbackPresent {
		return errors.New("Provider diagnostics 缺少 Windows static_scan_fallback")
	}
	fallbackStages, err := diagnosticIntegerMap(values, "fallback_stage_counts")
	if err != nil {
		return err
	}
	allowedStages := map[string]bool{
		"structured_key_object": true, "salt_neighborhood": true, "bounded_writable_heap": true,
		"bounded_readonly": true, "bounded_hex": true,
	}
	stageAttempts := 0
	for stage, count := range fallbackStages {
		if !allowedStages[stage] || count <= 0 {
			return errors.New("Provider diagnostics 的 Windows fallback stage 包含未知或非正计数")
		}
		stageAttempts += count
	}
	if staticFallback != (fallbackRouteIndex >= 0) || staticFallback != (stageAttempts > 0) {
		return errors.New("Provider diagnostics 的 Windows fallback 状态、route 与阶段计数矛盾")
	}
	if configRouteIndex >= 0 && fallbackRouteIndex >= 0 && configRouteIndex > fallbackRouteIndex {
		return errors.New("Provider diagnostics 没有按 Config.Cipher -> missing-only fallback 顺序执行")
	}
	if counts["per_process_collector_count"] < boolToInt(configAttempted)+stageAttempts {
		return errors.New("Provider diagnostics 的 Windows 逐进程 collector 计数不足")
	}
	if counts["per_process_collector_count"] > 0 && counts["opened_process_count"] == 0 {
		return errors.New("Provider diagnostics 在没有可读 Windows 进程时报告了 collector")
	}
	if verifiedCount > 0 {
		sources, sourceErr := diagnosticStringList(values, "candidate_sources")
		if sourceErr != nil {
			return sourceErr
		}
		found := false
		for _, source := range sources {
			found = found || source == "windows_config_cipher"
		}
		if !found {
			return errors.New("Provider diagnostics 的 Config.Cipher 已验证候选缺少来源证明")
		}
	}
	return nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func validateExpectedRequestedScopes(bundle *CandidateBundle, expected []string) error {
	normalized, err := normalizeRequestedScopes(expected)
	if err != nil {
		return err
	}
	actual, err := diagnosticStringList(bundle.Diagnostics, "requested_scopes")
	if err != nil {
		return err
	}
	if strings.Join(actual, "\x00") != strings.Join(normalized, "\x00") {
		return errors.New("Provider diagnostics requested_scopes 与当前请求不一致")
	}
	return nil
}

func validateBundleForScopes(bundle *CandidateBundle, expected []string) error {
	if err := ValidateBundle(bundle); err != nil {
		return err
	}
	return validateExpectedRequestedScopes(bundle, expected)
}

func validateBundleForRequest(bundle *CandidateBundle, expected []string, account localplatform.Account, catalogKey string) error {
	if err := validateBundleForScopes(bundle, expected); err != nil {
		return err
	}
	return validateProviderAccountBinding(bundle.DatabaseCredential, account, catalogKey)
}

func validateFinalAcquisitionResponse(bundle *CandidateBundle, expected []string, account localplatform.Account, catalogKey string) error {
	if err := validateBundleForRequest(bundle, expected, account, catalogKey); err != nil {
		return &ProtocolContractError{Cause: err}
	}
	if err := acquisitionStateError(bundle.Diagnostics); err != nil {
		if failure, ok := err.(*AcquisitionError); ok {
			failure.CatalogID = bundle.CatalogID
		}
		return err
	}
	return nil
}

func validateProviderAccountBinding(credential *DatabaseCredential, account localplatform.Account, catalogKey string) error {
	if credential == nil {
		return nil
	}
	// StorageAccountID 属于 CLI 的本地 keyring envelope，只会在 generation 验证后
	// 填充，并非 Provider v1 凭据 schema 的一部分；若从传输内容接受它，会模糊
	// Provider 证据与本地持久化之间的信任边界。
	if credential.StorageAccountID != "" {
		return errors.New("Provider credential 携带了非协议的本地存储账号绑定")
	}
	key, err := hex.DecodeString(catalogKey)
	if err != nil || len(key) != 32 {
		return errors.New("无法验证 Provider credential 的账号绑定")
	}
	defer func() {
		for index := range key {
			key[index] = 0
		}
	}()
	accountPath, err := filepath.Abs(account.Path)
	if err != nil {
		return errors.New("无法验证 Provider credential 的账号路径")
	}
	accountPath, err = filepath.EvalSymlinks(accountPath)
	if err != nil {
		return errors.New("无法验证 Provider credential 的账号真实路径")
	}
	expectedBinding := credentialCatalogHMAC(key, "account", accountPath)
	actualBinding := strings.ToLower(strings.TrimSpace(credential.AccountBindingID))
	if len(actualBinding) != 64 || !hmac.Equal([]byte(actualBinding), []byte(expectedBinding)) {
		return errors.New("Provider credential 的账号绑定与当前请求不一致")
	}
	return nil
}

func canonicalAcquisitionRequestAccount(account localplatform.Account) (localplatform.Account, error) {
	result := account
	var err error
	result.Path, err = filepath.Abs(account.Path)
	if err != nil {
		return localplatform.Account{}, errors.New("无法解析 Provider 账号路径")
	}
	result.Path, err = filepath.EvalSymlinks(result.Path)
	if err != nil {
		return localplatform.Account{}, errors.New("无法解析 Provider 账号真实路径")
	}
	result.DBDir, err = filepath.Abs(account.DBDir)
	if err != nil {
		return localplatform.Account{}, errors.New("无法解析 Provider 数据库路径")
	}
	result.DBDir, err = filepath.EvalSymlinks(result.DBDir)
	if err != nil {
		return localplatform.Account{}, errors.New("无法解析 Provider 数据库真实路径")
	}
	relative, err := filepath.Rel(result.Path, result.DBDir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return localplatform.Account{}, errors.New("Provider 数据库路径不属于当前账号")
	}
	return result, nil
}

func hasUsableRequestedCoverage(values map[string]any) bool {
	requested := diagnosticStrings(values, "requested_scopes")
	for _, scope := range requested {
		status := diagnosticString(values, scope+"_coverage_status")
		if status == "partial" || status == "complete" {
			return true
		}
	}
	return false
}

func hasCompleteRequestedCoverage(values map[string]any) bool {
	requested := diagnosticStrings(values, "requested_scopes")
	if len(requested) == 0 {
		return false
	}
	for _, scope := range requested {
		if diagnosticString(values, scope+"_coverage_status") != "complete" {
			return false
		}
	}
	return true
}

func diagnosticInteger(values map[string]any, name string) (int, bool) {
	value, found := values[name]
	if !found {
		return 0, false
	}
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), int64(int(number)) == number
	case float64:
		converted := int(number)
		return converted, number == float64(converted)
	case json.Number:
		parsed, err := number.Int64()
		return int(parsed), err == nil && int64(int(parsed)) == parsed
	default:
		return 0, false
	}
}

func validateCoverageDiagnostics(bundle *CandidateBundle, entries map[string]CatalogEntry) error {
	if len(bundle.Diagnostics) == 0 {
		return nil
	}
	_, hasDatabaseCount := bundle.Diagnostics["database_count"]
	_, hasRequiredCount := bundle.Diagnostics["required_database_count"]
	_, hasMatchedCount := bundle.Diagnostics["matched_database_count"]
	_, hasMissingCount := bundle.Diagnostics["missing_database_count"]
	_, hasMissingIDs := bundle.Diagnostics["missing_database_ids"]
	if !hasDatabaseCount && !hasRequiredCount && !hasMatchedCount && !hasMissingCount && !hasMissingIDs {
		return nil
	}

	keyedPaths := make(map[string]bool, len(bundle.DatabaseKeys))
	for path := range bundle.DatabaseKeys {
		keyedPaths[credentialPathKey(path)] = true
	}
	expectedMissing := map[string]bool{}
	classificationCounts := map[string]int{}
	requiredCount := 0
	for path, entry := range entries {
		classificationCounts[entry.Classification]++
		if entry.RequiredForKeyCoverage {
			requiredCount++
			if !keyedPaths[path] {
				expectedMissing[entry.DatabaseID] = true
			}
		}
	}
	expectedCounts := map[string]int{
		"database_count":            len(entries),
		"required_database_count":   requiredCount,
		"matched_database_count":    len(bundle.DatabaseKeys),
		"missing_database_count":    len(expectedMissing),
		"plaintext_database_count":  classificationCounts["plaintext"],
		"unreadable_database_count": classificationCounts["unreadable"],
		"unstable_database_count":   classificationCounts["unstable"],
		"truncated_database_count":  classificationCounts["truncated"],
	}
	for name, expected := range expectedCounts {
		if _, present := bundle.Diagnostics[name]; !present {
			continue
		}
		actual, valid := diagnosticInteger(bundle.Diagnostics, name)
		if !valid || actual != expected {
			return fmt.Errorf("Provider diagnostics %s=%v，与 catalog 证明计算值 %d 不一致", name, bundle.Diagnostics[name], expected)
		}
	}
	if hasMissingIDs {
		missing := diagnosticStrings(bundle.Diagnostics, "missing_database_ids")
		if len(missing) != len(expectedMissing) {
			return errors.New("Provider diagnostics missing_database_ids 数量与 catalog 证明不一致")
		}
		seen := map[string]bool{}
		for _, id := range missing {
			if len(id) != 64 || !validSecretHex(id) || seen[id] || !expectedMissing[id] {
				return errors.New("Provider diagnostics 包含无效、重复或非当前 catalog 的 missing database ID")
			}
			seen[id] = true
		}
	}
	if coverage, present := bundle.Diagnostics["database_coverage_status"].(string); present {
		expectedCoverage := "none"
		if len(entries) > 0 && len(expectedMissing) == 0 {
			expectedCoverage = "complete"
		} else if len(bundle.DatabaseKeys) > 0 {
			expectedCoverage = "partial"
		}
		if coverage != expectedCoverage {
			return fmt.Errorf("Provider diagnostics database_coverage_status=%q，与 catalog 证明计算值 %q 不一致", coverage, expectedCoverage)
		}
	}
	return nil
}

func acquisitionError(values map[string]any) *AcquisitionError {
	fallbackStages, _ := diagnosticIntegerMap(values, "fallback_stage_counts")
	result := &AcquisitionError{
		Reason:                      "no_candidates",
		ResultCode:                  diagnosticString(values, "result_code"),
		WorkflowStatus:              diagnosticString(values, "workflow_status"),
		RequestedScopes:             diagnosticStrings(values, "requested_scopes"),
		DatabaseTargetStatus:        diagnosticString(values, "database_target_status"),
		DatabaseCoverageStatus:      diagnosticString(values, "database_coverage_status"),
		MediaCoverageStatus:         diagnosticString(values, "media_coverage_status"),
		SecurityPostureStatus:       diagnosticString(values, "security_posture_status"),
		ShadowRouteStatus:           diagnosticString(values, "shadow_route_status"),
		RoutePriority:               diagnosticStrings(values, "route_priority"),
		NextAction:                  diagnosticString(values, "next_action"),
		TargetBindingStatus:         diagnosticString(values, "target_binding_status"),
		SessionAccountStatus:        diagnosticString(values, "session_account_status"),
		BlockingReasons:             diagnosticStrings(values, "blocking_reasons"),
		Platform:                    diagnosticString(values, "platform"),
		ProcessAccessStatus:         diagnosticString(values, "process_access_status"),
		ProcessAccessError:          diagnosticString(values, "process_access_error"),
		ProcessDiscoveryMethod:      diagnosticString(values, "process_discovery_method"),
		HelperStatus:                diagnosticString(values, "helper_status"),
		VersionSupport:              diagnosticString(values, "version_support"),
		WeChatVersion:               diagnosticString(values, "wechat_version"),
		WeChatBuild:                 diagnosticString(values, "wechat_build"),
		ExecutableSHA256:            diagnosticString(values, "executable_sha256"),
		BinaryFingerprintStatus:     diagnosticString(values, "binary_fingerprint_status"),
		BinarySigningStatus:         diagnosticString(values, "binary_signing_status"),
		BinarySignerSHA256:          diagnosticString(values, "binary_signer_sha256"),
		BinaryProductIdentity:       diagnosticString(values, "binary_product_identity"),
		SigningTeamID:               diagnosticString(values, "signing_team_id"),
		DesignatedRequirementSHA256: diagnosticString(values, "designated_requirement_sha256"),
		ProcessArchitecture:         diagnosticString(values, "process_architecture"),
		ProcessArchitectureStatus:   diagnosticString(values, "process_architecture_status"),
		ProcessTranslationStatus:    diagnosticString(values, "process_translation_status"),
		MacOSVersion:                diagnosticString(values, "macos_version"),
		CompatibilityRegistryStatus: diagnosticString(values, "compatibility_registry_status"),
		StandardRouteStatus:         diagnosticString(values, "standard_route_status"),
		StandardRouteEvidence:       diagnosticStrings(values, "standard_route_evidence"),
		ConfigCipherRouteStatus:     diagnosticString(values, "config_cipher_route_status"),
		WindowsRouteEvidence:        diagnosticStrings(values, "windows_route_evidence"),
		ProcessCount:                diagnosticIntegerOrZero(values, "process_count"),
		SelectedProcessCount:        diagnosticIntegerOrZero(values, "selected_process_count"),
		TargetBoundProcessCount:     diagnosticIntegerOrZero(values, "target_bound_process_count"),
		OtherAccountProcessCount:    diagnosticIntegerOrZero(values, "other_account_process_count"),
		UnknownAccountProcessCount:  diagnosticIntegerOrZero(values, "unknown_account_process_count"),
		OpenedProcessCount:          diagnosticIntegerOrZero(values, "opened_process_count"),
		AccessDeniedCount:           diagnosticIntegerOrZero(values, "access_denied_count"),
		PerProcessCollectorCount:    diagnosticIntegerOrZero(values, "per_process_collector_count"),
		ConfigCipherStructureCount:  diagnosticIntegerOrZero(values, "config_cipher_structure_count"),
		ConfigCipherInvalidCount:    diagnosticIntegerOrZero(values, "config_cipher_invalid_structure_count"),
		ConfigCipherCandidateCount:  diagnosticIntegerOrZero(values, "config_cipher_candidate_count"),
		ConfigCipherVerifiedCount:   diagnosticIntegerOrZero(values, "config_cipher_verified_candidate_count"),
		FallbackCandidateCount:      diagnosticIntegerOrZero(values, "fallback_candidate_count"),
		FallbackStageCounts:         fallbackStages,
		SessionID:                   diagnosticString(values, "session_id"),
		ProcessInstanceID:           diagnosticString(values, "process_instance_id"),
		ActionStage:                 diagnosticString(values, "action_stage"),
		RouteSelected:               diagnosticString(values, "route_selected"),
	}
	switch result.NextAction {
	case "trigger_database":
		result.Reason = "hook_trigger_required"
	case "restart_wechat":
		result.Reason = "hook_restart_required"
	case "relogin_wechat":
		result.Reason = "relogin_required"
	case "switch_to_target_account":
		result.Reason = "account_mismatch"
	case "fix_permission":
		result.Reason = "process_access_denied"
	case "disable_sip":
		result.Reason = "sip_required"
	case "reenable_sip":
		result.Reason = "sip_restoration_required"
	case "approve_shadow_mode":
		result.Reason = "shadow_approval_required"
	}
	switch result.ResultCode {
	case "deadline_exhausted":
		result.Reason = "deadline_exhausted"
	case "ambiguous":
		result.Reason = "ambiguous"
	case "unsupported":
		result.Reason = "unsupported"
	}
	for _, reason := range result.BlockingReasons {
		if reason == "validator_conflict" {
			result.Reason = "validator_conflict"
			break
		}
	}
	switch result.ProcessAccessStatus {
	case "process_list_unavailable":
		result.Reason = "process_list_unavailable"
	case "denied":
		result.Reason = "process_access_denied"
	case "wechat_not_running":
		result.Reason = "wechat_not_running"
	case "deadline_exhausted":
		result.Reason = "deadline_exhausted"
	}
	// 观察到的 SIP 状态本身既不是授权，也不能证明协议中的 standard 和 Shadow route
	// 均已失败。只有明确的结构化 next_action 才能请求跨重启的 SIP 工作流。
	if result.ProcessAccessError == "sip_enabled" && result.NextAction == "disable_sip" {
		result.Reason = "sip_required"
	} else if result.ProcessAccessError == "hook_restart_required" {
		result.Reason = "hook_restart_required"
	} else if result.ProcessAccessError == "hook_trigger_required" {
		result.Reason = "hook_trigger_required"
	}
	// 结构化 v1 状态的优先级高于旧版兼容字段，以它作为最终依据。
	if result.TargetBindingStatus == "mismatch" || result.NextAction == "switch_to_target_account" {
		result.Reason = "account_mismatch"
	} else if result.NextAction == "relogin_wechat" {
		result.Reason = "relogin_required"
	} else if result.NextAction == "reenable_sip" {
		result.Reason = "sip_restoration_required"
	}
	if result.ResultCode == "ambiguous" {
		result.Reason = "ambiguous"
	} else if result.ResultCode == "unsupported" {
		result.Reason = "unsupported"
	}
	return result
}

func diagnosticIntegerOrZero(values map[string]any, name string) int {
	value, _ := diagnosticInteger(values, name)
	return value
}

func acquisitionStateError(values map[string]any) error {
	if len(values) == 0 {
		return nil
	}
	resultCode := diagnosticString(values, "result_code")
	workflowStatus := diagnosticString(values, "workflow_status")
	targetBinding := diagnosticString(values, "target_binding_status")
	if diagnosticString(values, "next_action") == "reenable_sip" &&
		diagnosticString(values, "security_posture_status") == "restoration_required" && hasCompleteRequestedCoverage(values) {
		return nil
	}
	if targetBinding == "mismatch" || workflowStatus == "waiting_action" || workflowStatus == "blocked" {
		return acquisitionError(values)
	}
	switch resultCode {
	case "deadline_exhausted":
		if workflowStatus == "terminal" && hasUsableRequestedCoverage(values) {
			return nil
		}
		return acquisitionError(values)
	case "action_required", "permission_required", "ambiguous", "unsupported", "cancelled", "failed":
		return acquisitionError(values)
	}
	return nil
}

func normalizeDatabaseKey(value string) (string, error) {
	normalized := strings.NewReplacer(" ", "", ":", "").Replace(strings.TrimSpace(value))
	if strings.HasPrefix(strings.ToLower(normalized), "x'") && strings.HasSuffix(normalized, "'") {
		normalized = normalized[2 : len(normalized)-1]
	}
	if !hexKeyPattern.MatchString(normalized) {
		return "", errors.New("数据库候选密钥不是 32 字节十六进制值")
	}
	if len(normalized) == 96 {
		normalized = normalized[:64]
	}
	return strings.ToLower(normalized), nil
}

// ValidateBundle 仅校验协议形状；候选是否正确仍由数据库和媒体样本验真。
// 校验过程会把数据库候选就地归一化，因此接收指针，让改写在调用点可见。
func ValidateBundle(bundle *CandidateBundle) error {
	if bundle == nil {
		return errors.New("密钥候选为空")
	}
	if len(bundle.CatalogEntries) > maxCatalogEntries || len(bundle.DatabaseKeys) > maxCatalogEntries ||
		len(bundle.DatabaseProfiles) > maxCatalogEntries || len(bundle.Profiles) > maxResponseProfiles {
		return errors.New("密钥提供器响应超过 catalog/profile 数量上限")
	}
	if err := validateScopeDiagnostics(bundle); err != nil {
		return err
	}
	if err := validateDatabaseCredential(bundle.DatabaseCredential); err != nil {
		return err
	}
	plaintextOnlyComplete := diagnosticString(bundle.Diagnostics, "result_code") == "complete" &&
		diagnosticString(bundle.Diagnostics, "database_coverage_status") == "complete"
	structuredIncomplete := bundle.Protocol == Protocol && diagnosticString(bundle.Diagnostics, "result_code") != "complete"
	hasPlaintextCatalog := false
	for _, entry := range bundle.CatalogEntries {
		if entry.Classification == "plaintext" {
			hasPlaintextCatalog = true
			break
		}
	}
	if len(bundle.DatabaseKeys) == 0 && bundle.DatabaseCredential == nil && !plaintextOnlyComplete && !structuredIncomplete && !hasPlaintextCatalog && bundle.ImageKeys == nil {
		return errors.New("密钥提供器没有返回数据库候选")
	}
	for name, value := range bundle.DatabaseKeys {
		if strings.TrimSpace(name) == "" {
			return errors.New("数据库候选包含空名称")
		}
		normalized, err := normalizeDatabaseKey(value)
		if err != nil {
			return fmt.Errorf("数据库候选 %q 无效：%w", name, err)
		}
		bundle.DatabaseKeys[name] = normalized
	}
	if diagnosticString(bundle.Diagnostics, "platform") == "windows" && bundle.DatabaseCredential != nil {
		if err := validateWindowsCredentialProvenance(bundle.DatabaseCredential, bundle.DatabaseKeys, bundle.DatabaseProfiles); err != nil {
			return err
		}
	}
	if bundle.Protocol == Protocol && (len(bundle.DatabaseKeys) > 0 || len(bundle.CatalogEntries) > 0) {
		if !validSecretHex(bundle.CatalogID) || len(bundle.CatalogEntries) == 0 {
			return errors.New("密钥提供器响应缺少 catalog 文件证明")
		}
		profiles := make(map[string]ProfileSummary, len(bundle.Profiles))
		for _, profile := range bundle.Profiles {
			if profile.ID == "" || !supportedProfileSummary(profile) {
				return errors.New("密钥提供器返回了 CLI 不支持的 profile registry")
			}
			if _, duplicate := profiles[profile.ID]; duplicate {
				return errors.New("密钥提供器返回了重复 profile")
			}
			profiles[profile.ID] = profile
		}
		entries := make(map[string]CatalogEntry, len(bundle.CatalogEntries))
		for _, entry := range bundle.CatalogEntries {
			clean := filepath.Clean(entry.RelativePath)
			if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return errors.New("catalog 文件证明包含越界路径")
			}
			key := credentialPathKey(clean)
			if _, duplicate := entries[key]; duplicate {
				return errors.New("catalog 文件证明包含重复路径")
			}
			if !validSecretHex(entry.DatabaseID) || entry.CanonicalFileID != "" && !validSecretHex(entry.CanonicalFileID) ||
				entry.FirstPageSHA256 != "" && !validSecretHex(entry.FirstPageSHA256) {
				return errors.New("catalog 文件证明格式无效")
			}
			if (entry.Classification == "encrypted_eligible" || entry.Classification == "plaintext") &&
				(len(entry.CanonicalFileID) != 64 || len(entry.FirstPageSHA256) != 64) {
				return errors.New("可发布 catalog 条目缺少完整文件证明")
			}
			switch entry.Classification {
			case "encrypted_eligible":
				if !entry.RequiredForKeyCoverage {
					return errors.New("加密 catalog 条目的覆盖或 profile 标记无效")
				}
			case "plaintext":
				if entry.RequiredForKeyCoverage || entry.ProfileID != "" {
					return errors.New("明文 catalog 条目的覆盖或 profile 标记无效")
				}
			case "unreadable", "unstable", "truncated":
				if !entry.RequiredForKeyCoverage {
					return errors.New("不可发布 catalog 条目不能从覆盖率中排除")
				}
			default:
				return errors.New("catalog 条目 classification 无效")
			}
			entries[key] = entry
		}
		for name := range bundle.DatabaseKeys {
			entry, found := entries[credentialPathKey(name)]
			if !found {
				return fmt.Errorf("数据库候选 %q 没有对应的 catalog 文件证明", name)
			}
			if entry.Classification != "encrypted_eligible" {
				return fmt.Errorf("数据库候选 %q 指向不需要密钥的 catalog 条目", name)
			}
			profileID := bundle.DatabaseProfiles[name]
			if profileID == "" || entry.ProfileID != "" && profileID != entry.ProfileID {
				return fmt.Errorf("数据库候选 %q 没有与 catalog 一致的 profile", name)
			}
			if _, supported := profiles[profileID]; !supported {
				return fmt.Errorf("数据库候选 %q 的 profile 未在响应 registry 中登记", name)
			}
		}
		if err := validateCoverageDiagnostics(bundle, entries); err != nil {
			return err
		}
	}
	for name, profileID := range bundle.DatabaseProfiles {
		if _, found := bundle.DatabaseKeys[name]; !found {
			return fmt.Errorf("数据库 profile %q 没有对应的有效 key", name)
		}
		if strings.TrimSpace(profileID) == "" {
			return fmt.Errorf("数据库 profile %q 为空", name)
		}
	}
	if bundle.ImageKeys != nil {
		if len(bundle.ImageKeys.AES) != 16 || bundle.ImageKeys.XOR < 0 || bundle.ImageKeys.XOR > 255 {
			return errors.New("图片 AES/XOR 候选格式无效")
		}
	}
	return nil
}
