package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
)

const maxResponseBytes = 1024 * 1024

// acquireTimeout 是硬性上限，到点直接杀进程；acquireBudget 是告知提供器的自我收敛时限。
// 两者之间留出的余量用于进程启动、收尾和响应编码，确保提供器有机会
// 在被杀之前把已经验证出的密钥写回来。
const (
	acquireTimeout = 90 * time.Second
	acquireBudget  = 75 * time.Second
)

// ErrComponentMissing 表示 PATH 上没有找到密钥获取组件，用户尚未单独安装它；
// 调用方据此区分「组件未安装」与「组件已安装但取证失败」两种情况。
var ErrComponentMissing = errors.New("未找到 v-local-key-provider")

// AcquisitionError contains only provider-supplied status enums. It never
// carries candidate values, paths, stderr, or process memory.
type AcquisitionError struct {
	Reason              string
	Platform            string
	ProcessAccessStatus string
	ProcessAccessError  string
	HelperStatus        string
	VersionSupport      string
}

func (err *AcquisitionError) Error() string {
	return "密钥提供器没有返回可验证的数据库候选"
}

var hexKeyPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}(?:[0-9a-fA-F]{32})?$`)

type ImageKeys struct {
	AES string `json:"aes"`
	XOR int    `json:"xor"`
}

type CandidateBundle struct {
	Protocol     string            `json:"protocol,omitempty"`
	RequestID    string            `json:"request_id,omitempty"`
	DatabaseKeys map[string]string `json:"database_keys"`
	ImageKeys    *ImageKeys        `json:"image_keys,omitempty"`
	Diagnostics  map[string]any    `json:"diagnostics,omitempty"`
}

type acquireRequest struct {
	Protocol   string   `json:"protocol"`
	RequestID  string   `json:"request_id"`
	Action     string   `json:"action"`
	AccountDir string   `json:"account_dir"`
	DBDir      string   `json:"db_dir"`
	Scopes     []string `json:"scopes"`
	DeadlineMS int64    `json:"deadline_ms"`
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
	over   bool
}

func (writer *limitedBuffer) Write(data []byte) (int, error) {
	remaining := writer.limit - writer.buffer.Len()
	if remaining > 0 {
		chunk := data
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
		}
		_, _ = writer.buffer.Write(chunk)
	}
	if len(data) > remaining {
		writer.over = true
	}
	return len(data), nil
}

func (writer *limitedBuffer) Bytes() []byte  { return writer.buffer.Bytes() }
func (writer *limitedBuffer) String() string { return writer.buffer.String() }

func newRequestID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// Acquire 在受限协议上调用独立密钥提供器。密钥只经 stdin/stdout 传递。
func Acquire(parent context.Context, explicit string, account localplatform.Account) (CandidateBundle, error) {
	path, _ := Resolve(explicit)
	if path == "" {
		return CandidateBundle{}, ErrComponentMissing
	}
	requestID, err := newRequestID()
	if err != nil {
		return CandidateBundle{}, errors.New("无法生成一次性请求标识")
	}
	request := acquireRequest{
		Protocol: Protocol, RequestID: requestID, Action: "acquire",
		AccountDir: account.Path, DBDir: account.DBDir,
		Scopes:     []string{"database", "image"},
		DeadlineMS: acquireBudget.Milliseconds(),
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return CandidateBundle{}, errors.New("无法编码密钥请求")
	}
	ctx, cancel := context.WithTimeout(parent, acquireTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, "acquire")
	command.Dir = filepath.Dir(path)
	command.Stdin = bytes.NewReader(payload)
	stdout := &limitedBuffer{limit: maxResponseBytes}
	stderr := &limitedBuffer{limit: 16 * 1024}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return CandidateBundle{}, errors.New("密钥提供器运行超时")
		}
		return CandidateBundle{}, errors.New("密钥提供器执行失败；原始 stderr 已隐藏")
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
	if response.Protocol != Protocol || response.RequestID != requestID {
		return CandidateBundle{}, errors.New("密钥提供器响应与当前请求不匹配")
	}
	if err := ValidateBundle(&response); err != nil {
		if len(response.DatabaseKeys) == 0 {
			return CandidateBundle{}, acquisitionError(response.Diagnostics)
		}
		return CandidateBundle{}, err
	}
	return response, nil
}

func diagnosticString(values map[string]any, name string) string {
	value, _ := values[name].(string)
	return value
}

func acquisitionError(values map[string]any) *AcquisitionError {
	result := &AcquisitionError{
		Reason:              "no_candidates",
		Platform:            diagnosticString(values, "platform"),
		ProcessAccessStatus: diagnosticString(values, "process_access_status"),
		ProcessAccessError:  diagnosticString(values, "process_access_error"),
		HelperStatus:        diagnosticString(values, "helper_status"),
		VersionSupport:      diagnosticString(values, "version_support"),
	}
	switch result.ProcessAccessStatus {
	case "denied":
		result.Reason = "process_access_denied"
	case "wechat_not_running":
		result.Reason = "wechat_not_running"
	case "deadline_exhausted":
		result.Reason = "deadline_exhausted"
	}
	if result.ProcessAccessError == "sip_enabled" {
		result.Reason = "sip_required"
	} else if result.ProcessAccessError == "hook_restart_required" {
		result.Reason = "hook_restart_required"
	} else if result.ProcessAccessError == "hook_trigger_required" {
		result.Reason = "hook_trigger_required"
	}
	return result
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
	if len(bundle.DatabaseKeys) == 0 {
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
	if bundle.ImageKeys != nil {
		if len(bundle.ImageKeys.AES) != 16 || bundle.ImageKeys.XOR < 0 || bundle.ImageKeys.XOR > 255 {
			return errors.New("图片 AES/XOR 候选格式无效")
		}
	}
	return nil
}
