package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
)

const (
	acquisitionDaemonSchemaVersion = 1
	acquisitionResumeVersion       = 1
	externalCheckpointVersion      = 1
	acquisitionSessionLifetime     = 15 * time.Minute
	externalCheckpointLifetime     = 7 * 24 * time.Hour
	acquisitionDaemonStartTimeout  = 1500 * time.Millisecond
	acquisitionDaemonResponseMax   = maxResponseBytes
)

var errAcquisitionDaemonUnsupported = errors.New("密钥提供器不支持 acquisition daemon")

type acquisitionDaemonEndpoint struct {
	SchemaVersion int    `json:"schema_version"`
	Address       string `json:"address"`
	Transport     string `json:"transport"`
	Token         string `json:"token"`
	PID           int    `json:"pid"`
	Version       string `json:"version"`
	ProviderPath  string `json:"provider_path"`
	DaemonPath    string `json:"daemon_path,omitempty"`
	ClientPath    string `json:"client_path"`
	StartedAt     string `json:"started_at"`
}

type acquisitionDaemonRequest struct {
	SchemaVersion int             `json:"schema_version"`
	Token         string          `json:"token"`
	Command       string          `json:"command"`
	Acquire       *acquireRequest `json:"acquire,omitempty"`
}

type acquisitionDaemonError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type acquisitionDaemonResponse struct {
	SchemaVersion int                     `json:"schema_version"`
	Status        string                  `json:"status,omitempty"`
	Result        *CandidateBundle        `json:"result,omitempty"`
	Error         *acquisitionDaemonError `json:"error,omitempty"`
}

type acquisitionResume struct {
	Version           int      `json:"version"`
	ProviderPath      string   `json:"provider_path"`
	EndpointStartedAt string   `json:"endpoint_started_at"`
	AccountDir        string   `json:"account_dir"`
	DBDir             string   `json:"db_dir"`
	Scopes            []string `json:"scopes"`
	SessionID         string   `json:"session_id"`
	CatalogID         string   `json:"catalog_id"`
	NextAction        string   `json:"next_action"`
	ProcessInstanceID string   `json:"process_instance_id,omitempty"`
	Route             string   `json:"route,omitempty"`
	ActionStage       string   `json:"action_stage,omitempty"`
	ExpiresAt         string   `json:"expires_at"`
}

// ExternalCheckpointStatus 是跨重启或会令采集 daemon 失效的工作流所使用的无权限
// handoff 记录。它有意不包含操作回执、daemon token、进程路径、账号路径或密钥材料。
// 后续调用必须创建新 session 并重新验证全部机器证据，之后才能继续推进。
type ExternalCheckpointStatus struct {
	Version                     int      `json:"version"`
	WorkflowID                  string   `json:"workflow_id"`
	ProviderID                  string   `json:"provider_id"`
	AccountID                   string   `json:"account_id"`
	Scopes                      []string `json:"scopes"`
	RevalidationStage           string   `json:"revalidation_stage"`
	PriorRequestedAction        string   `json:"prior_requested_action"`
	PriorCatalogID              string   `json:"prior_catalog_id,omitempty"`
	LastSecurityPostureStatus   string   `json:"last_security_posture_status"`
	CreatedAt                   string   `json:"created_at"`
	ExpiresAt                   string   `json:"expires_at"`
	MachineRevalidationRequired bool     `json:"machine_revalidation_required"`
}

func opaquePathID(value string) string {
	normalized := filepath.Clean(value)
	if absolute, err := filepath.Abs(normalized); err == nil {
		normalized = absolute
	}
	normalized = localplatform.CanonicalSystemPath(normalized)
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(normalized))))
	return hex.EncodeToString(sum[:8])
}

func acquisitionPaths(privateRoot, providerPath, accountPath string) (string, string, error) {
	absolute, err := filepath.Abs(privateRoot)
	if err != nil || strings.TrimSpace(privateRoot) == "" {
		return "", "", errors.New("acquisition 私有目录无效")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("acquisition 私有目录不可用")
	}
	if err := validateAcquisitionPrivateRoot(absolute); err != nil {
		return "", "", err
	}
	base := "provider-" + opaquePathID(providerPath)
	endpoint := filepath.Join(absolute, base+".json")
	resume := filepath.Join(absolute, base+"-"+opaquePathID(accountPath)+".resume.json")
	return endpoint, resume, nil
}

func externalCheckpointPath(privateRoot, accountPath string) (string, error) {
	absolute, err := filepath.Abs(privateRoot)
	if err != nil || strings.TrimSpace(privateRoot) == "" {
		return "", errors.New("acquisition 私有目录无效")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("acquisition 私有目录不可用")
	}
	if err := validateAcquisitionPrivateRoot(absolute); err != nil {
		return "", err
	}
	return filepath.Join(absolute, "external-"+opaquePathID(accountPath)+".checkpoint.json"), nil
}

func externalCheckpointFilename(name string, allowBackup bool) bool {
	const prefix = "external-"
	suffix := ".checkpoint.json"
	if allowBackup && strings.HasSuffix(name, suffix+".old") {
		suffix += ".old"
	}
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	return validOpaqueID(id, 8) && id == strings.ToLower(id)
}

func externalActionStage(action string) string {
	switch action {
	case "approve_shadow_mode", "disable_sip":
		return "external_change_revalidation_required"
	case "reenable_sip":
		return "security_restoration_revalidation_required"
	default:
		return ""
	}
}

func validOpaqueID(value string, bytes int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == bytes
}

// errExternalCheckpointExpired 与结构损坏区分开：过期是 7 天生命周期的正常终点，
// 过期记录只是失去权限、不再代表待办工作流，调用方可以把它当作不存在；损坏记录则
// 必须按失败关闭处理，绝不能作为可信工作流状态呈现给 Agent。
var errExternalCheckpointExpired = errors.New("跨重启 acquisition checkpoint 已过期")

func validateExternalCheckpoint(value ExternalCheckpointStatus, now time.Time) error {
	createdAt, createdErr := time.Parse(time.RFC3339Nano, value.CreatedAt)
	expiresAt, expiresErr := time.Parse(time.RFC3339Nano, value.ExpiresAt)
	canonical, scopeErr := normalizeRequestedScopes(value.Scopes)
	if value.Version != externalCheckpointVersion || !validOpaqueID(value.WorkflowID, 16) ||
		!validOpaqueID(value.ProviderID, 8) || !validOpaqueID(value.AccountID, 8) || scopeErr != nil ||
		strings.Join(value.Scopes, "\x00") != strings.Join(canonical, "\x00") ||
		externalActionStage(value.PriorRequestedAction) == "" || value.RevalidationStage != externalActionStage(value.PriorRequestedAction) ||
		createdErr != nil || expiresErr != nil || expiresAt.Before(createdAt) ||
		expiresAt.Sub(createdAt) > externalCheckpointLifetime+time.Minute || !value.MachineRevalidationRequired {
		return errors.New("跨重启 acquisition checkpoint 无效")
	}
	if value.PriorCatalogID != "" && (!validSecretHex(value.PriorCatalogID) || len(value.PriorCatalogID) != 64) {
		return errors.New("跨重启 acquisition checkpoint 的 catalog 标识无效")
	}
	switch value.LastSecurityPostureStatus {
	case "not_applicable", "not_evaluated", "sip_enabled_verified", "sip_disabled_verified", "restoration_required":
	default:
		return errors.New("跨重启 acquisition checkpoint 的安全姿态无效")
	}
	if value.PriorRequestedAction == "reenable_sip" && value.LastSecurityPostureStatus != "restoration_required" {
		return errors.New("SIP 恢复 checkpoint 没有绑定 restoration_required")
	}
	if value.PriorRequestedAction == "disable_sip" && value.LastSecurityPostureStatus != "sip_enabled_verified" {
		return errors.New("SIP 变更 checkpoint 没有绑定已验证的启用状态")
	}
	// 过期判定放在全部结构检查之后：既损坏又过期的记录必须报告为损坏，不能因为
	// 先命中过期而被当成可跳过的正常记录。
	if !now.Before(expiresAt) {
		return errExternalCheckpointExpired
	}
	return nil
}

func decodeSingleJSON(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("状态目标不是可信普通文件")
	}
	if err := validateAcquisitionStateFile(path); err != nil {
		return err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	markSensitiveBytes(payload)
	defer clearSensitiveBytes(payload)
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("状态文件包含多余数据")
	}
	return nil
}

func loadAcquisitionEndpoint(path, providerPath string) (acquisitionDaemonEndpoint, error) {
	var endpoint acquisitionDaemonEndpoint
	if err := decodeSingleJSON(path, &endpoint); err != nil {
		return acquisitionDaemonEndpoint{}, err
	}
	if endpoint.SchemaVersion != acquisitionDaemonSchemaVersion || len(endpoint.Token) != 64 || endpoint.PID <= 0 ||
		strings.TrimSpace(endpoint.Version) == "" ||
		!sameExecutablePath(endpoint.ProviderPath, providerPath) ||
		!trustedAcquisitionDaemonPath(endpoint.DaemonPath, providerPath) {
		return acquisitionDaemonEndpoint{}, errors.New("acquisition daemon endpoint 无效")
	}
	if endpoint.Transport == "tcp4-development" {
		host, _, splitErr := net.SplitHostPort(endpoint.Address)
		ip := net.ParseIP(host)
		if splitErr != nil || ip == nil || !ip.IsLoopback() {
			return acquisitionDaemonEndpoint{}, errors.New("acquisition daemon development endpoint 无效")
		}
	}
	if err := validateAcquisitionEndpointTransport(endpoint); err != nil {
		return acquisitionDaemonEndpoint{}, errors.New("acquisition daemon transport 或客户端绑定无效")
	}
	if raw, err := hex.DecodeString(endpoint.Token); err != nil || len(raw) != 32 {
		return acquisitionDaemonEndpoint{}, errors.New("acquisition daemon token 无效")
	}
	if err := validateAcquisitionDaemonProcessIdentity(endpoint, providerPath); err != nil {
		return acquisitionDaemonEndpoint{}, errors.New("acquisition daemon 运行时身份无效")
	}
	return endpoint, nil
}

func trustedAcquisitionDaemonPath(daemonPath, providerPath string) bool {
	if daemonPath == "" || sameExecutablePath(daemonPath, providerPath) {
		return true
	}
	if runtime.GOOS != "darwin" || filepath.Base(daemonPath) != "v-local-key-provider-helper" {
		return false
	}
	daemonDir, daemonErr := filepath.EvalSymlinks(filepath.Dir(daemonPath))
	providerDir, providerErr := filepath.EvalSymlinks(filepath.Dir(providerPath))
	return daemonErr == nil && providerErr == nil && sameFilePath(daemonDir, providerDir)
}

func sameExecutablePath(left, right string) bool {
	return sameFilePath(left, right)
}

func acquisitionDaemonExchange(parent context.Context, endpoint acquisitionDaemonEndpoint, command string, acquire *acquireRequest) (acquisitionDaemonResponse, error) {
	connection, err := dialAcquisitionDaemonEndpoint(parent, endpoint)
	if err != nil {
		return acquisitionDaemonResponse{}, err
	}
	defer connection.Close()
	stopCancellationWatch := make(chan struct{})
	defer close(stopCancellationWatch)
	go func() {
		select {
		case <-parent.Done():
			_ = connection.Close()
		case <-stopCancellationWatch:
		}
	}()
	deadline := time.Now().Add(acquireTimeout)
	if parentDeadline, found := parent.Deadline(); found && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	_ = connection.SetDeadline(deadline)
	request := acquisitionDaemonRequest{
		SchemaVersion: acquisitionDaemonSchemaVersion, Token: endpoint.Token, Command: command, Acquire: acquire,
	}
	requestPayload, err := json.Marshal(request)
	if err != nil {
		return acquisitionDaemonResponse{}, err
	}
	markSensitiveBytes(requestPayload)
	defer clearSensitiveBytes(requestPayload)
	if _, err := io.Copy(connection, bytes.NewReader(requestPayload)); err != nil {
		return acquisitionDaemonResponse{}, err
	}
	if _, err := io.WriteString(connection, "\n"); err != nil {
		return acquisitionDaemonResponse{}, err
	}
	responsePayload, err := io.ReadAll(io.LimitReader(connection, acquisitionDaemonResponseMax+1))
	if err != nil || len(responsePayload) == 0 || len(responsePayload) > acquisitionDaemonResponseMax {
		return acquisitionDaemonResponse{}, errors.New("acquisition daemon 响应为空或超过安全上限")
	}
	markSensitiveBytes(responsePayload)
	defer clearSensitiveBytes(responsePayload)
	var result acquisitionDaemonResponse
	decoder := json.NewDecoder(bytes.NewReader(responsePayload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return acquisitionDaemonResponse{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return acquisitionDaemonResponse{}, errors.New("acquisition daemon 响应包含多余数据")
	}
	if result.SchemaVersion != acquisitionDaemonSchemaVersion {
		return acquisitionDaemonResponse{}, errors.New("acquisition daemon 响应版本不匹配")
	}
	if result.Error != nil {
		return acquisitionDaemonResponse{}, fmt.Errorf("acquisition daemon: %s", result.Error.Code)
	}
	return result, nil
}

var startAcquisitionDaemonProcess = func(providerPath, endpointPath string) (*exec.Cmd, error) {
	clientPath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	clientPath, err = filepath.EvalSymlinks(clientPath)
	if err != nil {
		return nil, err
	}
	command := exec.Command(providerPath, "daemon", "serve", endpointPath, clientPath)
	command.Dir = filepath.Dir(providerPath)
	configureProviderCommandEnvironment(command)
	configureDetachedProviderCommand(command)
	if err := command.Start(); err != nil {
		return nil, err
	}
	return command, nil
}

func ensureAcquisitionDaemon(parent context.Context, providerPath, endpointPath string) (acquisitionDaemonEndpoint, error) {
	if endpoint, err := loadAcquisitionEndpoint(endpointPath, providerPath); err == nil {
		if response, pingErr := acquisitionDaemonExchange(parent, endpoint, "ping", nil); pingErr == nil && response.Status == "ready" {
			return endpoint, nil
		}
	}
	releaseStartupLock, lockErr := acquireAcquisitionStartupLock(endpointPath + ".startup.lock")
	if lockErr != nil {
		if !errors.Is(lockErr, errAcquisitionStartupBusy) {
			return acquisitionDaemonEndpoint{}, errAcquisitionDaemonUnsupported
		}
		deadline := time.NewTimer(acquisitionDaemonStartTimeout)
		defer deadline.Stop()
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-parent.Done():
				return acquisitionDaemonEndpoint{}, parent.Err()
			case <-deadline.C:
				return acquisitionDaemonEndpoint{}, errAcquisitionDaemonUnsupported
			case <-ticker.C:
				endpoint, err := loadAcquisitionEndpoint(endpointPath, providerPath)
				if err != nil {
					continue
				}
				response, err := acquisitionDaemonExchange(parent, endpoint, "ping", nil)
				if err == nil && response.Status == "ready" {
					return endpoint, nil
				}
			}
		}
	}
	defer releaseStartupLock()
	// 另一个进程可能在本进程刚取得锁之前已经完成启动。删除旧端点或启动子进程前必须
	// 再检查一次。
	if endpoint, err := loadAcquisitionEndpoint(endpointPath, providerPath); err == nil {
		if response, pingErr := acquisitionDaemonExchange(parent, endpoint, "ping", nil); pingErr == nil && response.Status == "ready" {
			return endpoint, nil
		}
	}
	_ = os.Remove(endpointPath)
	command, err := startAcquisitionDaemonProcess(providerPath, endpointPath)
	if err != nil {
		return acquisitionDaemonEndpoint{}, errAcquisitionDaemonUnsupported
	}
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	stopChild := func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
		}
	}
	timer := time.NewTimer(acquisitionDaemonStartTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-parent.Done():
			stopChild()
			return acquisitionDaemonEndpoint{}, parent.Err()
		case <-finished:
			return acquisitionDaemonEndpoint{}, errAcquisitionDaemonUnsupported
		case <-timer.C:
			stopChild()
			return acquisitionDaemonEndpoint{}, errAcquisitionDaemonUnsupported
		case <-ticker.C:
			endpoint, err := loadAcquisitionEndpoint(endpointPath, providerPath)
			if err != nil {
				continue
			}
			response, err := acquisitionDaemonExchange(parent, endpoint, "ping", nil)
			if err == nil && response.Status == "ready" {
				return endpoint, nil
			}
		}
	}
}

func canonicalScopes(scopes []string) []string {
	result := append([]string(nil), scopes...)
	sort.Strings(result)
	return result
}

func sameResumeScope(resume acquisitionResume, providerPath string, account localplatform.Account, scopes []string, endpoint acquisitionDaemonEndpoint) bool {
	expiresAt, err := time.Parse(time.RFC3339Nano, resume.ExpiresAt)
	return err == nil && time.Now().Before(expiresAt) && resume.Version == acquisitionResumeVersion &&
		sameExecutablePath(resume.ProviderPath, providerPath) && sameFilePath(resume.AccountDir, account.Path) &&
		sameFilePath(resume.DBDir, account.DBDir) && strings.Join(resume.Scopes, "\x00") == strings.Join(canonicalScopes(scopes), "\x00") &&
		resume.SessionID != "" && resume.CatalogID != "" && resume.EndpointStartedAt == endpoint.StartedAt
}

func sameFilePath(left, right string) bool {
	leftResolved, leftErr := filepath.EvalSymlinks(left)
	rightResolved, rightErr := filepath.EvalSymlinks(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftAbs, leftErr := filepath.Abs(leftResolved)
	rightAbs, rightErr := filepath.Abs(rightResolved)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftInfo, leftStatErr := os.Stat(leftAbs)
	rightInfo, rightStatErr := os.Stat(rightAbs)
	if leftStatErr == nil && rightStatErr == nil {
		return os.SameFile(leftInfo, rightInfo)
	}
	leftClean := filepath.Clean(leftAbs)
	rightClean := filepath.Clean(rightAbs)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftClean, rightClean)
	}
	return leftClean == rightClean
}

func loadAcquisitionResume(path string, target *acquisitionResume) error {
	if err := decodeSingleJSON(path, target); err == nil {
		return nil
	} else {
		var backup acquisitionResume
		if backupErr := decodeSingleJSON(path+".old", &backup); backupErr == nil {
			*target = backup
			return nil
		}
		return err
	}
}

func removeAcquisitionResume(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + ".old")
}

func saveAcquisitionResume(path string, resume acquisitionResume) error {
	return saveAcquisitionJSON(path, ".acquisition-resume-*.tmp", resume)
}

func saveAcquisitionJSON(path, temporaryPattern string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	markSensitiveBytes(payload)
	defer clearSensitiveBytes(payload)
	file, err := os.CreateTemp(filepath.Dir(path), temporaryPattern)
	if err != nil {
		return err
	}
	temporary := file.Name()
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		return err
	}
	if _, err := io.WriteString(file, "\n"); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	backup := path + ".old"
	_ = os.Remove(backup)
	movedOld := false
	if _, statErr := os.Stat(path); statErr == nil {
		if err := os.Rename(path, backup); err != nil {
			return err
		}
		movedOld = true
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err := os.Rename(temporary, path); err != nil {
		if movedOld {
			_ = os.Rename(backup, path)
		}
		return err
	}
	if movedOld {
		_ = os.Remove(backup)
	}
	remove = false
	return nil
}

// absentExternalCheckpoint 判断这次加载是否等价于「没有待办的跨重启工作流」：
// 文件不存在，或记录已过期因而不再代表任何待办操作。
func absentExternalCheckpoint(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, errExternalCheckpointExpired)
}

func loadExternalCheckpoint(path string) (ExternalCheckpointStatus, error) {
	load := func(candidate string) (ExternalCheckpointStatus, error) {
		var value ExternalCheckpointStatus
		if err := decodeSingleJSON(candidate, &value); err != nil {
			return ExternalCheckpointStatus{}, err
		}
		if err := validateExternalCheckpoint(value, time.Now()); err != nil {
			return ExternalCheckpointStatus{}, err
		}
		return value, nil
	}
	value, err := load(path)
	if err == nil {
		return value, nil
	}
	if backup, backupErr := load(path + ".old"); backupErr == nil {
		return backup, nil
	}
	return ExternalCheckpointStatus{}, err
}

func pendingSecurityRestorationCheckpoint(privateRoot, providerPath string, account localplatform.Account) (ExternalCheckpointStatus, bool, error) {
	path, err := externalCheckpointPath(privateRoot, account.Path)
	if err != nil {
		return ExternalCheckpointStatus{}, false, err
	}
	checkpoint, err := loadExternalCheckpoint(path)
	if absentExternalCheckpoint(err) {
		return ExternalCheckpointStatus{}, false, nil
	}
	if err != nil {
		return ExternalCheckpointStatus{}, false, errors.New("跨重启 acquisition checkpoint 无法安全验证")
	}
	if checkpoint.ProviderID != opaquePathID(providerPath) || checkpoint.AccountID != opaquePathID(account.Path) {
		return ExternalCheckpointStatus{}, false, errors.New("跨重启 acquisition checkpoint 与当前 Provider 或账号不匹配")
	}
	if checkpoint.RevalidationStage != "security_restoration_revalidation_required" || checkpoint.PriorRequestedAction != "reenable_sip" {
		return ExternalCheckpointStatus{}, false, nil
	}
	return checkpoint, true, nil
}

func pendingExternalChangeCheckpoint(privateRoot, providerPath string, account localplatform.Account, scopes []string) (ExternalCheckpointStatus, bool, error) {
	path, err := externalCheckpointPath(privateRoot, account.Path)
	if err != nil {
		return ExternalCheckpointStatus{}, false, err
	}
	checkpoint, err := loadExternalCheckpoint(path)
	if absentExternalCheckpoint(err) {
		return ExternalCheckpointStatus{}, false, nil
	}
	if err != nil {
		return ExternalCheckpointStatus{}, false, errors.New("跨重启 acquisition checkpoint 无法安全验证")
	}
	if checkpoint.ProviderID != opaquePathID(providerPath) || checkpoint.AccountID != opaquePathID(account.Path) {
		return ExternalCheckpointStatus{}, false, errors.New("跨重启 acquisition checkpoint 与当前 Provider 或账号不匹配")
	}
	if checkpoint.RevalidationStage != "external_change_revalidation_required" {
		return checkpoint, false, nil
	}
	if strings.Join(checkpoint.Scopes, "\x00") != strings.Join(canonicalScopes(scopes), "\x00") {
		return checkpoint, false, &AcquisitionError{
			Reason: "external_workflow_scope_mismatch", ResultCode: "action_required", WorkflowStatus: "blocked",
			RequestedScopes: append([]string(nil), checkpoint.Scopes...), SecurityPostureStatus: checkpoint.LastSecurityPostureStatus,
			NextAction: checkpoint.PriorRequestedAction, BlockingReasons: []string{"external_workflow_scope_mismatch"},
			ExternalCheckpointStatus: "persisted", ExternalWorkflowID: checkpoint.WorkflowID,
		}
	}
	return checkpoint, true, nil
}

func removeExternalCheckpoint(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + ".old")
}

func saveExternalCheckpoint(path string, value ExternalCheckpointStatus) error {
	if err := validateExternalCheckpoint(value, time.Now().Add(-time.Second)); err != nil {
		return err
	}
	return saveAcquisitionJSON(path, ".external-workflow-*.tmp", value)
}

func reconcileExternalCheckpoint(privateRoot, providerPath string, account localplatform.Account, scopes []string, bundle CandidateBundle, acquisitionErr error) error {
	if strings.TrimSpace(privateRoot) == "" {
		return acquisitionErr
	}
	var failure *AcquisitionError
	if !errors.As(acquisitionErr, &failure) && acquisitionErr == nil && externalActionStage(diagnosticString(bundle.Diagnostics, "next_action")) != "" {
		failure = acquisitionError(bundle.Diagnostics)
		failure.CatalogID = bundle.CatalogID
	}
	path, err := externalCheckpointPath(privateRoot, account.Path)
	if err != nil {
		if failure != nil && externalActionStage(failure.NextAction) != "" {
			failure.ExternalCheckpointStatus = "unavailable"
			if bundle.Diagnostics != nil {
				bundle.Diagnostics["external_checkpoint_status"] = "unavailable"
			}
			if acquisitionErr == nil {
				return failure
			}
		}
		return acquisitionErr
	}
	if failure != nil && externalActionStage(failure.NextAction) != "" {
		now := time.Now().UTC()
		workflowID, idErr := newRequestID()
		createdAt := now.Format(time.RFC3339Nano)
		expiresAt := now.Add(externalCheckpointLifetime).Format(time.RFC3339Nano)
		if previous, loadErr := loadExternalCheckpoint(path); loadErr == nil && previous.ProviderID == opaquePathID(providerPath) &&
			previous.AccountID == opaquePathID(account.Path) && strings.Join(previous.Scopes, "\x00") == strings.Join(canonicalScopes(scopes), "\x00") {
			workflowID = previous.WorkflowID
			createdAt = previous.CreatedAt
			expiresAt = previous.ExpiresAt
		}
		if idErr != nil && workflowID == "" {
			failure.ExternalCheckpointStatus = "unavailable"
			if bundle.Diagnostics != nil {
				bundle.Diagnostics["external_checkpoint_status"] = "unavailable"
			}
			if acquisitionErr == nil {
				return failure
			}
			return acquisitionErr
		}
		checkpoint := ExternalCheckpointStatus{
			Version: externalCheckpointVersion, WorkflowID: workflowID, ProviderID: opaquePathID(providerPath),
			AccountID: opaquePathID(account.Path), Scopes: canonicalScopes(scopes), RevalidationStage: externalActionStage(failure.NextAction),
			PriorRequestedAction: failure.NextAction, PriorCatalogID: failure.CatalogID,
			LastSecurityPostureStatus: failure.SecurityPostureStatus, CreatedAt: createdAt,
			ExpiresAt: expiresAt, MachineRevalidationRequired: true,
		}
		if checkpoint.LastSecurityPostureStatus == "" {
			checkpoint.LastSecurityPostureStatus = "not_evaluated"
		}
		if saveErr := saveExternalCheckpoint(path, checkpoint); saveErr != nil {
			failure.ExternalCheckpointStatus = "unavailable"
			if bundle.Diagnostics != nil {
				bundle.Diagnostics["external_checkpoint_status"] = "unavailable"
			}
			if acquisitionErr == nil {
				return failure
			}
			return acquisitionErr
		}
		failure.ExternalCheckpointStatus = "persisted"
		failure.ExternalWorkflowID = checkpoint.WorkflowID
		if bundle.Diagnostics != nil {
			bundle.Diagnostics["external_checkpoint_status"] = "persisted"
			bundle.Diagnostics["external_workflow_id"] = checkpoint.WorkflowID
		}
		return acquisitionErr
	}
	if acquisitionErr == nil {
		previous, loadErr := loadExternalCheckpoint(path)
		if absentExternalCheckpoint(loadErr) {
			return nil
		}
		if loadErr != nil {
			return errors.New("跨重启 acquisition checkpoint 无法安全验证")
		}
		requestedScopes, scopeErr := diagnosticStringList(bundle.Diagnostics, "requested_scopes")
		matchesCheckpoint := scopeErr == nil && strings.Join(canonicalScopes(requestedScopes), "\x00") == strings.Join(previous.Scopes, "\x00") &&
			previous.ProviderID == opaquePathID(providerPath) && previous.AccountID == opaquePathID(account.Path) &&
			previous.MachineRevalidationRequired
		verifiedEnabledTerminal := diagnosticString(bundle.Diagnostics, "platform") == "darwin" &&
			diagnosticString(bundle.Diagnostics, "result_code") == "complete" &&
			diagnosticString(bundle.Diagnostics, "workflow_status") == "terminal" &&
			diagnosticString(bundle.Diagnostics, "security_posture_status") == "sip_enabled_verified" &&
			diagnosticString(bundle.Diagnostics, "next_action") == "none"
		// disable checkpoint 只记录已请求的外部操作，不能证明用户已经执行。后续一次
		// 完整的普通 Provider 采集若获得经验证的 SIP-enabled 证据，即可证明该 handoff
		// 已经过期。
		disableWasNotApplied := matchesCheckpoint && verifiedEnabledTerminal &&
			previous.RevalidationStage == "external_change_revalidation_required" && previous.PriorRequestedAction == "disable_sip" &&
			previous.LastSecurityPostureStatus == "sip_enabled_verified" &&
			diagnosticString(bundle.Diagnostics, "action_stage") != "security_posture_revalidation" &&
			hasCompleteRequestedCoverage(bundle.Diagnostics)
		baseRestored := matchesCheckpoint && verifiedEnabledTerminal &&
			previous.RevalidationStage == "security_restoration_revalidation_required" && previous.PriorRequestedAction == "reenable_sip"
		fullSessionRestored := diagnosticString(bundle.Diagnostics, "session_id") != "" && hasCompleteRequestedCoverage(bundle.Diagnostics)
		postureOnlyRestored := diagnosticString(bundle.Diagnostics, "session_id") == "" &&
			diagnosticString(bundle.Diagnostics, "action_stage") == "security_posture_revalidation" &&
			diagnosticString(bundle.Diagnostics, "database_coverage_status") == "not_requested" &&
			diagnosticString(bundle.Diagnostics, "media_coverage_status") == "not_requested" &&
			len(bundle.DatabaseKeys) == 0 && bundle.DatabaseCredential == nil && bundle.ImageKeys == nil && len(bundle.CatalogEntries) == 0
		restored := baseRestored && (fullSessionRestored || postureOnlyRestored)
		if disableWasNotApplied || restored {
			removeExternalCheckpoint(path)
		}
	}
	return acquisitionErr
}

// ListExternalCheckpoints 在不创建采集目录的情况下查找无权限的跨重启 handoff。
// 无效记录会按失败关闭处理，不会作为可信工作流状态呈现给 Agent。
func ListExternalCheckpoints(privateRoot string) ([]ExternalCheckpointStatus, error) {
	absolute, err := filepath.Abs(privateRoot)
	if err != nil || strings.TrimSpace(privateRoot) == "" {
		return nil, errors.New("acquisition 私有目录无效")
	}
	info, err := os.Lstat(absolute)
	if os.IsNotExist(err) {
		return []ExternalCheckpointStatus{}, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("acquisition 私有目录不可用")
	}
	if err := validateAcquisitionPrivateRoot(absolute); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return nil, err
	}
	checkpointPaths := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !externalCheckpointFilename(entry.Name(), true) {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".old")
		checkpointPaths[filepath.Join(absolute, name)] = true
	}
	paths := make([]string, 0, len(checkpointPaths))
	for path := range checkpointPaths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]ExternalCheckpointStatus, 0, len(paths))
	for _, path := range paths {
		value, loadErr := loadExternalCheckpoint(path)
		// 过期记录已经不代表待办工作流，跳过它，让其余账号的有效 handoff 仍能列出；
		// 畸形记录仍然整体失败关闭，不冒充可信工作流状态。
		if errors.Is(loadErr, errExternalCheckpointExpired) {
			continue
		}
		if loadErr != nil {
			return nil, loadErr
		}
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].CreatedAt < result[right].CreatedAt })
	return result, nil
}

// ClearExternalCheckpoints 只移除无权限的跨重启 handoff 记录。它有意不触碰 daemon
// endpoint、可恢复的 Phase 2 session、snapshot 或凭据；当畸形记录无法解码到足以识别
// 账号时，以此作为明确的恢复路径。
func ClearExternalCheckpoints(privateRoot string) (int, error) {
	absolute, err := filepath.Abs(privateRoot)
	if err != nil || strings.TrimSpace(privateRoot) == "" {
		return 0, errors.New("acquisition 私有目录无效")
	}
	info, err := os.Lstat(absolute)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("acquisition 私有目录不可用")
	}
	if err := validateAcquisitionPrivateRoot(absolute); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return 0, err
	}
	targets := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !externalCheckpointFilename(name, true) {
			continue
		}
		path := filepath.Join(absolute, name)
		entryInfo, infoErr := os.Lstat(path)
		if infoErr != nil {
			return 0, infoErr
		}
		if entryInfo.IsDir() {
			return 0, errors.New("跨重启 acquisition checkpoint 目标不能是目录")
		}
		targets = append(targets, path)
	}
	removed := 0
	for _, path := range targets {
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func requestForWorkflow(account localplatform.Account, scopes []string, workflow workflowRequest, catalogKey string) (acquireRequest, error) {
	requestAccount, err := canonicalAcquisitionRequestAccount(account)
	if err != nil {
		return acquireRequest{}, err
	}
	requestID, err := newRequestID()
	if err != nil {
		return acquireRequest{}, errors.New("无法生成一次性请求标识")
	}
	return acquireRequest{
		Protocol: Protocol, RequestID: requestID, Action: "acquire",
		CatalogKey: catalogKey,
		AccountDir: requestAccount.Path, DBDir: requestAccount.DBDir, Scopes: append([]string(nil), scopes...),
		DeadlineMS: acquireBudget.Milliseconds(), Workflow: workflow,
	}, nil
}

func requireDaemonResult(exchange acquisitionDaemonResponse, request acquireRequest) (CandidateBundle, error) {
	if exchange.Result == nil {
		return CandidateBundle{}, errors.New("acquisition daemon 没有返回协议结果")
	}
	result := *exchange.Result
	if result.Protocol != Protocol || result.RequestID != request.RequestID {
		return CandidateBundle{}, errors.New("acquisition daemon 响应与当前请求不匹配")
	}
	return result, nil
}

func updateResumeFromResult(resume *acquisitionResume, result CandidateBundle) {
	resume.NextAction = diagnosticString(result.Diagnostics, "next_action")
	resume.ProcessInstanceID = diagnosticString(result.Diagnostics, "process_instance_id")
	resume.Route = diagnosticString(result.Diagnostics, "route_selected")
	resume.ActionStage = diagnosticString(result.Diagnostics, "action_stage")
}

func resumableAcquisitionAction(action string) bool {
	return map[string]bool{
		"trigger_database": true, "restart_wechat": true, "relogin_wechat": true,
		"switch_to_target_account": true,
	}[action]
}

func actionConfirmationError(resume acquisitionResume, reason string) *AcquisitionError {
	return &AcquisitionError{
		Reason: "action_confirmation_mismatch", ResultCode: "action_required", WorkflowStatus: "waiting_action",
		NextAction: resume.NextAction, BlockingReasons: []string{reason}, SessionID: resume.SessionID,
		CatalogID: resume.CatalogID, ProcessInstanceID: resume.ProcessInstanceID,
		ActionStage: resume.ActionStage, RouteSelected: resume.Route,
	}
}

func cancelAcquisitionSession(parent context.Context, endpoint acquisitionDaemonEndpoint, account localplatform.Account, scopes []string, resume acquisitionResume) {
	request, err := requestForWorkflow(account, scopes, workflowRequest{Operation: "cancel", SessionID: resume.SessionID}, "")
	if err == nil {
		_, _ = acquisitionDaemonExchange(parent, endpoint, "acquire", &request)
	}
}

func finalizeDaemonAcquisition(parent context.Context, endpoint acquisitionDaemonEndpoint, account localplatform.Account, scopes []string, resume acquisitionResume, resumePath, catalogKey string) (CandidateBundle, bool, error) {
	finalize, err := requestForWorkflow(account, scopes, workflowRequest{
		Operation: "finalize", SessionID: resume.SessionID, ExpectedCatalogID: resume.CatalogID,
	}, catalogKey)
	if err != nil {
		return CandidateBundle{}, true, err
	}
	finalExchange, err := acquisitionDaemonExchange(parent, endpoint, "acquire", &finalize)
	removeAcquisitionResume(resumePath)
	if err != nil {
		cancelAcquisitionSession(parent, endpoint, account, scopes, resume)
		return CandidateBundle{}, true, err
	}
	result, err := requireDaemonResult(finalExchange, finalize)
	if err != nil {
		return CandidateBundle{}, true, err
	}
	if err := validateFinalAcquisitionResponse(&result, scopes, localplatform.Account{Path: finalize.AccountDir}, catalogKey); err != nil {
		return CandidateBundle{}, true, err
	}
	return result, true, nil
}

func acquireViaDaemon(parent context.Context, providerPath string, account localplatform.Account, scopes []string, privateRoot, confirmedAction, catalogKey string) (CandidateBundle, bool, error) {
	endpointPath, resumePath, err := acquisitionPaths(privateRoot, providerPath, account.Path)
	if err != nil {
		return CandidateBundle{}, true, err
	}
	endpoint, err := ensureAcquisitionDaemon(parent, providerPath, endpointPath)
	if errors.Is(err, errAcquisitionDaemonUnsupported) {
		return CandidateBundle{}, false, nil
	}
	if err != nil {
		return CandidateBundle{}, true, err
	}
	var resume acquisitionResume
	resumeValid := loadAcquisitionResume(resumePath, &resume) == nil && sameResumeScope(resume, providerPath, account, scopes, endpoint)
	if !resumeValid {
		removeAcquisitionResume(resumePath)
		prepare, err := requestForWorkflow(account, scopes, workflowRequest{Operation: "prepare"}, catalogKey)
		if err != nil {
			return CandidateBundle{}, true, err
		}
		exchange, err := acquisitionDaemonExchange(parent, endpoint, "acquire", &prepare)
		if err != nil {
			return CandidateBundle{}, true, err
		}
		prepared, err := requireDaemonResult(exchange, prepare)
		if err != nil {
			return CandidateBundle{}, true, err
		}
		if err := validateBundleForRequest(&prepared, scopes, localplatform.Account{Path: prepare.AccountDir}, catalogKey); err != nil {
			return CandidateBundle{}, true, &ProtocolContractError{Cause: err}
		}
		resume = acquisitionResume{
			Version: acquisitionResumeVersion, ProviderPath: providerPath, EndpointStartedAt: endpoint.StartedAt,
			AccountDir: account.Path, DBDir: account.DBDir, Scopes: canonicalScopes(scopes),
			SessionID: diagnosticString(prepared.Diagnostics, "session_id"), CatalogID: prepared.CatalogID,
			ExpiresAt: diagnosticString(prepared.Diagnostics, "session_expires_at"),
		}
		if resume.ExpiresAt == "" {
			resume.ExpiresAt = time.Now().Add(acquisitionSessionLifetime).UTC().Format(time.RFC3339Nano)
		}
		updateResumeFromResult(&resume, prepared)
		if resume.SessionID == "" || resume.CatalogID == "" {
			return CandidateBundle{}, true, errors.New("acquisition daemon prepare 响应缺少 session 或 catalog 绑定")
		}
		if err := saveAcquisitionResume(resumePath, resume); err != nil {
			cancelAcquisitionSession(parent, endpoint, account, scopes, resume)
			return CandidateBundle{}, true, err
		}
	}
	if confirmedAction == "stop_and_report" {
		if !resumableAcquisitionAction(resume.NextAction) {
			cancelAcquisitionSession(parent, endpoint, account, scopes, resume)
			removeAcquisitionResume(resumePath)
			return CandidateBundle{}, true, actionConfirmationError(resume, "no_pending_partial_action")
		}
		return finalizeDaemonAcquisition(parent, endpoint, account, scopes, resume, resumePath, catalogKey)
	}
	if confirmedAction != "" {
		if !resumableAcquisitionAction(confirmedAction) {
			return CandidateBundle{}, true, actionConfirmationError(resume, "confirmed_action_not_resumable")
		}
		if confirmedAction != resume.NextAction {
			return CandidateBundle{}, true, actionConfirmationError(resume, "confirmed_action_mismatch")
		}
	}
	workflow := workflowRequest{
		Operation: "observe", SessionID: resume.SessionID, ExpectedCatalogID: resume.CatalogID,
	}
	if confirmedAction != "" {
		workflow.ActionReceipt = &actionReceipt{
			Action: confirmedAction, UserConfirmed: true, ProcessInstanceID: resume.ProcessInstanceID,
			Route: resume.Route, ActionStage: resume.ActionStage,
		}
	}
	observe, err := requestForWorkflow(account, scopes, workflow, catalogKey)
	if err != nil {
		return CandidateBundle{}, true, err
	}
	exchange, err := acquisitionDaemonExchange(parent, endpoint, "acquire", &observe)
	if err != nil {
		removeAcquisitionResume(resumePath)
		return CandidateBundle{}, true, err
	}
	observed, err := requireDaemonResult(exchange, observe)
	if err != nil {
		return CandidateBundle{}, true, err
	}
	if err := validateBundleForRequest(&observed, scopes, localplatform.Account{Path: observe.AccountDir}, catalogKey); err != nil {
		cancelAcquisitionSession(parent, endpoint, account, scopes, resume)
		removeAcquisitionResume(resumePath)
		return CandidateBundle{}, true, &ProtocolContractError{Cause: err}
	}
	updateResumeFromResult(&resume, observed)
	workflowStatus := diagnosticString(observed.Diagnostics, "workflow_status")
	resultCode := diagnosticString(observed.Diagnostics, "result_code")
	if workflowStatus == "waiting_action" || resultCode == "action_required" {
		if resume.NextAction == "reenable_sip" && hasCompleteRequestedCoverage(observed.Diagnostics) {
			cancelAcquisitionSession(parent, endpoint, account, scopes, resume)
			removeAcquisitionResume(resumePath)
			return observed, true, nil
		}
		if !resumableAcquisitionAction(resume.NextAction) {
			cancelAcquisitionSession(parent, endpoint, account, scopes, resume)
			removeAcquisitionResume(resumePath)
			failure := acquisitionError(observed.Diagnostics)
			failure.CatalogID = observed.CatalogID
			return CandidateBundle{}, true, failure
		}
		if err := saveAcquisitionResume(resumePath, resume); err != nil {
			cancelAcquisitionSession(parent, endpoint, account, scopes, resume)
			return CandidateBundle{}, true, err
		}
		failure := acquisitionError(observed.Diagnostics)
		failure.CatalogID = observed.CatalogID
		return CandidateBundle{}, true, failure
	}
	if workflowStatus == "blocked" {
		cancelAcquisitionSession(parent, endpoint, account, scopes, resume)
		removeAcquisitionResume(resumePath)
		failure := acquisitionError(observed.Diagnostics)
		failure.CatalogID = observed.CatalogID
		return CandidateBundle{}, true, failure
	}
	return finalizeDaemonAcquisition(parent, endpoint, account, scopes, resume, resumePath, catalogKey)
}

// CancelAcquisition 取消当前账号尚未完成的密钥获取会话。续接文件不含秘密；无论守护
// 进程是否仍存活，取消都会先移除本地续接记录，避免后续误续接。
func CancelAcquisition(parent context.Context, explicit string, account localplatform.Account, privateRoot string) (bool, error) {
	checkpointPath, checkpointErr := externalCheckpointPath(privateRoot, account.Path)
	if checkpointErr != nil {
		return false, checkpointErr
	}
	checkpointRemoved := false
	if _, err := os.Lstat(checkpointPath); err == nil {
		removeExternalCheckpoint(checkpointPath)
		checkpointRemoved = true
	} else if !os.IsNotExist(err) {
		return false, err
	}
	providerPath, _ := Resolve(explicit)
	if providerPath == "" {
		if checkpointRemoved {
			return true, nil
		}
		return false, ErrComponentMissing
	}
	endpointPath, resumePath, err := acquisitionPaths(privateRoot, providerPath, account.Path)
	if err != nil {
		return false, err
	}
	var resume acquisitionResume
	if err := loadAcquisitionResume(resumePath, &resume); err != nil {
		if os.IsNotExist(err) {
			return checkpointRemoved, nil
		}
		removeAcquisitionResume(resumePath)
		return checkpointRemoved, errors.New("acquisition resume 元数据无效，已清理")
	}
	removeAcquisitionResume(resumePath)
	endpoint, err := loadAcquisitionEndpoint(endpointPath, providerPath)
	if err != nil || resume.Version != acquisitionResumeVersion || resume.SessionID == "" ||
		!sameFilePath(resume.AccountDir, account.Path) || !sameFilePath(resume.DBDir, account.DBDir) ||
		resume.EndpointStartedAt != endpoint.StartedAt {
		return true, nil
	}
	cancelAcquisitionSession(parent, endpoint, account, resume.Scopes, resume)
	return true, nil
}
