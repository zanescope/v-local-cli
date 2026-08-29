package wxgfqual

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
)

const (
	ProviderProtocol                = "v-local-cli-image-decoder/1"
	maxProviderResponseBytes        = 64 * 1024
	maxProviderDiagnosticBytes      = 16 * 1024
	maxDecodedOutputBytes           = 64 * 1024 * 1024
	defaultProviderTimeout          = 30 * time.Second
	providerProcessMemoryLimitBytes = 512 * 1024 * 1024
	providerJobMemoryLimitBytes     = 768 * 1024 * 1024
	providerActiveProcessLimit      = 2
)

var (
	ErrProviderTimeout             = errors.New("WXGF 解码适配器运行超时")
	ErrProviderExecution           = errors.New("WXGF 解码适配器执行失败")
	ErrProviderProtocol            = errors.New("WXGF 解码适配器协议无效")
	ErrProviderOutput              = errors.New("WXGF 解码适配器输出无效")
	newImageDecoderProviderCommand = func(ctx context.Context, executable string) *exec.Cmd {
		return exec.CommandContext(ctx, executable, "decode-still")
	}
)

type ProviderRequest struct {
	Protocol       string `json:"protocol"`
	RequestID      string `json:"request_id"`
	Action         string `json:"action"`
	InputPath      string `json:"input_path"`
	InputFormat    string `json:"input_format"`
	InputSHA256    string `json:"input_sha256"`
	OutputPath     string `json:"output_path"`
	OutputFormat   string `json:"output_format"`
	MaximumFrames  int    `json:"maximum_frames"`
	MaximumPixels  int64  `json:"maximum_pixels"`
	NetworkAllowed bool   `json:"network_allowed"`
}

type ProviderResponse struct {
	Protocol       string `json:"protocol"`
	RequestID      string `json:"request_id"`
	Status         string `json:"status"`
	InputSHA256    string `json:"input_sha256"`
	OutputSHA256   string `json:"output_sha256"`
	OutputFormat   string `json:"output_format"`
	FrameCount     int    `json:"frame_count"`
	NetworkUsed    *bool  `json:"network_used"`
	Decoder        string `json:"decoder"`
	DecoderVersion string `json:"decoder_version"`
}

type providerRequest = ProviderRequest
type providerResponse = ProviderResponse

type ProviderOptions struct {
	Executable    string
	TemporaryRoot string
	Timeout       time.Duration
}

// ProviderIsolation reports only controls the runner actually established.
// CreateProcessTreeContained is deliberately narrower than a claim that every
// process an untrusted provider could induce is contained. Provider
// self-reporting can never set these fields.
type ProviderIsolation struct {
	Method                     string
	CreateProcessTreeContained bool
	JobMemberMemoryLimited     bool
	NetworkIsolated            bool
	FilesystemIsolated         bool
	ProcessMemoryLimitBytes    int64
	JobMemoryLimitBytes        int64
	ActiveProcessLimit         int
}

// Result is deliberately marked as qualification-only. ProductionReady stays
// false even after a successful decode because this experimental runner does
// not yet attest the provider binary or establish a complete OS sandbox.
type Result struct {
	Inspection        Inspection
	Validation        cryptoutil.ImageValidation
	OutputPNG         []byte
	Decoder           string
	DecoderVersion    string
	Isolation         ProviderIsolation
	ProductionReady   bool
	PromotionBlockers []string
}

func providerPromotionBlockers(isolation ProviderIsolation) []string {
	blockers := []string{
		"wxgf_container_layout_not_fully_specified",
		"provider_binary_trust_not_verified",
		"os_network_isolation_not_enforced",
		"os_filesystem_credential_isolation_not_enforced",
	}
	if !isolation.CreateProcessTreeContained || !isolation.JobMemberMemoryLimited {
		blockers = append(blockers, "createprocess_tree_and_job_memory_limits_not_enforced")
	}
	return append(blockers,
		"real_fixture_matrix_insufficient",
		"decoded_visual_equivalence_not_confirmed",
		"decoder_distribution_license_not_qualified",
	)
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
	over   bool
}

func (writer *limitedBuffer) Write(payload []byte) (int, error) {
	remaining := writer.limit - writer.buffer.Len()
	if remaining < 0 {
		remaining = 0
	}
	if remaining > len(payload) {
		remaining = len(payload)
	}
	if remaining > 0 {
		_, _ = writer.buffer.Write(payload[:remaining])
	}
	if len(payload) > remaining {
		writer.over = true
	}
	return len(payload), nil
}

func randomRequestID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func fileSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func writePrivateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	succeeded := false
	defer func() {
		_ = file.Close()
		if !succeeded {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	succeeded = true
	return nil
}

func safeMetadata(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("._+-:/", character)) {
			return false
		}
	}
	return true
}

func providerEnvironment(temporaryDirectory string) []string {
	values := []string{
		"HOME=",
		"LANG=C",
		"LC_ALL=C",
		"PATH=",
		"TMP=" + temporaryDirectory,
		"TEMP=" + temporaryDirectory,
		"TMPDIR=" + temporaryDirectory,
	}
	if runtime.GOOS != "windows" {
		return values
	}
	for _, name := range []string{"SystemRoot", "WINDIR", "ComSpec", "PATHEXT"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			values = append(values, name+"="+value)
		}
	}
	return values
}

func validateExecutable(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("缺少 WXGF 解码适配器路径")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("WXGF 解码适配器不是普通文件")
	}
	return absolute, nil
}

func prepareStage(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("缺少 WXGF 资格验证临时目录")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("WXGF 资格验证临时根目录无效")
	}
	stage, err := os.MkdirTemp(absolute, "v-local-cli-wxgf-qualification-*")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(stage, 0o700); err != nil {
		_ = os.Remove(stage)
		return "", err
	}
	return stage, nil
}

func removeStage(root, stage string) error {
	relative, err := filepath.Rel(root, stage)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("拒绝清理越界的 WXGF 资格验证目录")
	}
	return os.RemoveAll(stage)
}

func decodeProviderResponse(payload []byte) (providerResponse, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var response providerResponse
	if err := decoder.Decode(&response); err != nil {
		return providerResponse{}, ErrProviderProtocol
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return providerResponse{}, ErrProviderProtocol
	}
	return response, nil
}

func readProviderOutput(path, expectedDigest string) ([]byte, cryptoutil.ImageValidation, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxDecodedOutputBytes {
		return nil, cryptoutil.ImageValidation{}, ErrProviderOutput
	}
	payload, err := os.ReadFile(path)
	if err != nil || fileSHA256(payload) != expectedDigest {
		return nil, cryptoutil.ImageValidation{}, ErrProviderOutput
	}
	validation, err := cryptoutil.ValidateImageStructure(payload)
	if err != nil || validation.Format != "png" {
		return nil, cryptoutil.ImageValidation{}, ErrProviderOutput
	}
	return payload, validation, nil
}

func verifyProviderInput(path, expectedDigest string, expectedBytes int) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != int64(expectedBytes) {
		return ErrProviderProtocol
	}
	payload, err := os.ReadFile(path)
	if err != nil || fileSHA256(payload) != expectedDigest {
		return ErrProviderProtocol
	}
	return nil
}

// RunProviderTrial extracts only the qualified Annex-B candidate into a private
// staging directory, invokes one local provider request, and independently
// validates its PNG output. This function is intentionally not used by the CLI.
func RunProviderTrial(parent context.Context, data []byte, options ProviderOptions) (result Result, returnErr error) {
	if parent == nil {
		return Result{}, errors.New("WXGF 资格验证缺少 context")
	}
	inspection, err := Inspect(data)
	if err != nil {
		return Result{}, err
	}
	executable, err := validateExecutable(options.Executable)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(options.TemporaryRoot) == "" {
		return Result{}, errors.New("缺少 WXGF 资格验证临时目录")
	}
	temporaryRoot, err := filepath.Abs(options.TemporaryRoot)
	if err != nil {
		return Result{}, err
	}
	stage, err := prepareStage(temporaryRoot)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if cleanupErr := removeStage(temporaryRoot, stage); cleanupErr != nil {
			result = Result{}
			returnErr = errors.New("WXGF 资格验证临时明文清理失败")
		}
	}()

	hevc := data[inspection.HEVCOffset:]
	inputPath := filepath.Join(stage, "input.hevc")
	outputPath := filepath.Join(stage, "output.png")
	if err := writePrivateFile(inputPath, hevc); err != nil {
		return Result{}, err
	}
	if err := os.Chmod(inputPath, 0o400); err != nil {
		return Result{}, err
	}
	requestID, err := randomRequestID()
	if err != nil {
		return Result{}, err
	}
	inputDigest := fileSHA256(hevc)
	request := providerRequest{
		Protocol: ProviderProtocol, RequestID: requestID, Action: "decode_still",
		InputPath: inputPath, InputFormat: "hevc_annex_b", InputSHA256: inputDigest,
		OutputPath: outputPath, OutputFormat: "png", MaximumFrames: 1,
		MaximumPixels: cryptoutil.MaxDecodedImagePixels, NetworkAllowed: false,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return Result{}, err
	}
	timeout := options.Timeout
	if timeout <= 0 || timeout > defaultProviderTimeout {
		timeout = defaultProviderTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command := newImageDecoderProviderCommand(ctx, executable)
	command.Dir = stage
	command.Env = providerEnvironment(stage)
	command.Stdin = bytes.NewReader(append(payload, '\n'))
	stdout := &limitedBuffer{limit: maxProviderResponseBytes}
	stderr := &limitedBuffer{limit: maxProviderDiagnosticBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = 2 * time.Second
	isolation, runErr := runProviderCommand(command)
	if runErr != nil {
		if ctx.Err() != nil {
			return Result{}, ErrProviderTimeout
		}
		return Result{}, ErrProviderExecution
	}
	if stdout.over || stderr.over {
		return Result{}, ErrProviderProtocol
	}
	if err := verifyProviderInput(inputPath, inputDigest, len(hevc)); err != nil {
		return Result{}, err
	}
	response, err := decodeProviderResponse(stdout.buffer.Bytes())
	if err != nil {
		return Result{}, err
	}
	if response.Protocol != ProviderProtocol || response.RequestID != requestID || response.Status != "decoded" ||
		response.InputSHA256 != inputDigest || response.OutputFormat != "png" || response.FrameCount != 1 ||
		response.NetworkUsed == nil || *response.NetworkUsed || !safeMetadata(response.Decoder) || !safeMetadata(response.DecoderVersion) ||
		len(response.OutputSHA256) != sha256.Size*2 {
		return Result{}, ErrProviderProtocol
	}
	if _, err := hex.DecodeString(response.OutputSHA256); err != nil {
		return Result{}, ErrProviderProtocol
	}
	output, validation, err := readProviderOutput(outputPath, response.OutputSHA256)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Inspection: inspection, Validation: validation, OutputPNG: output,
		Decoder: strings.TrimSpace(response.Decoder), DecoderVersion: strings.TrimSpace(response.DecoderVersion),
		Isolation: isolation, ProductionReady: false,
		PromotionBlockers: providerPromotionBlockers(isolation),
	}, nil
}

func (result Result) String() string {
	return fmt.Sprintf("wxgf qualification: %dx%d, decoder=%s, production_ready=%t", result.Validation.Width, result.Validation.Height, result.Decoder, result.ProductionReady)
}
