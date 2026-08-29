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
	ProviderProtocol                 = "v-local-cli-image-decoder/2"
	ProviderIdentityManifestProtocol = "v-local-cli/wxgf-provider-identity-manifest/v1"
	ProviderIdentityBasis            = "host_staged_manifest_bound_provider_and_decoder_sha256"
	ProviderSourceStatus             = "unverified"
	DecoderSourceStatus              = "unverified"
	DecoderDistributionLicenseStatus = "not_qualified"
	ProviderBinaryTrustStatus        = "unverified"
	maxProviderResponseBytes         = 64 * 1024
	maxProviderDiagnosticBytes       = 16 * 1024
	maxDecodedOutputBytes            = 64 * 1024 * 1024
	maxProviderManifestBytes         = 64 * 1024
	maxProviderExecutableBytes       = 256 * 1024 * 1024
	maxDecoderExecutableBytes        = 1024 * 1024 * 1024
	defaultProviderTimeout           = 30 * time.Second
	providerProcessMemoryLimitBytes  = 512 * 1024 * 1024
	providerJobMemoryLimitBytes      = 768 * 1024 * 1024
	providerActiveProcessLimit       = 2
)

var (
	ErrProviderTimeout             = errors.New("WXGF 解码适配器运行超时")
	ErrProviderExecution           = errors.New("WXGF 解码适配器执行失败")
	ErrProviderProtocol            = errors.New("WXGF 解码适配器协议无效")
	ErrProviderOutput              = errors.New("WXGF 解码适配器输出无效")
	ErrProviderIdentity            = errors.New("WXGF 解码适配器身份绑定无效")
	newImageDecoderProviderCommand = func(ctx context.Context, executable string) *exec.Cmd {
		return exec.CommandContext(ctx, executable, "decode-still")
	}
)

type ProviderRequest struct {
	Protocol                       string `json:"protocol"`
	RequestID                      string `json:"request_id"`
	Action                         string `json:"action"`
	InputPath                      string `json:"input_path"`
	InputFormat                    string `json:"input_format"`
	InputSHA256                    string `json:"input_sha256"`
	OutputPath                     string `json:"output_path"`
	OutputFormat                   string `json:"output_format"`
	MaximumFrames                  int    `json:"maximum_frames"`
	MaximumPixels                  int64  `json:"maximum_pixels"`
	NetworkAllowed                 bool   `json:"network_allowed"`
	ProviderIdentityManifestSHA256 string `json:"provider_identity_manifest_sha256"`
	ProviderSHA256                 string `json:"provider_sha256"`
	DecoderName                    string `json:"decoder_name"`
	DecoderSHA256                  string `json:"decoder_sha256"`
	DecoderIdentityBasis           string `json:"decoder_identity_basis"`
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

// ProviderIdentityManifest is an operator-supplied identity intent, not a
// signature or provenance root. The host independently hashes the named files,
// stages the exact bytes it verified, and keeps the trust status unverified.
type ProviderIdentityManifest struct {
	Protocol                         string `json:"protocol"`
	ProviderFileName                 string `json:"provider_file_name"`
	ProviderSHA256                   string `json:"provider_sha256"`
	DecoderName                      string `json:"decoder_name"`
	DecoderFileName                  string `json:"decoder_file_name"`
	DecoderSHA256                    string `json:"decoder_sha256"`
	ProviderSourceStatus             string `json:"provider_source_status"`
	DecoderSourceStatus              string `json:"decoder_source_status"`
	DecoderDistributionLicenseStatus string `json:"decoder_distribution_license_status"`
}

type ProviderBinaryIdentity struct {
	ManifestProtocol                 string `json:"manifest_protocol"`
	ManifestSHA256                   string `json:"manifest_sha256"`
	ProviderSHA256                   string `json:"provider_sha256"`
	DecoderName                      string `json:"decoder_name"`
	DecoderSHA256                    string `json:"decoder_sha256"`
	IdentityBasis                    string `json:"identity_basis"`
	ProviderSourceStatus             string `json:"provider_source_status"`
	DecoderSourceStatus              string `json:"decoder_source_status"`
	DecoderDistributionLicenseStatus string `json:"decoder_distribution_license_status"`
	ProviderBinaryTrustStatus        string `json:"provider_binary_trust_status"`
}

type providerBundle struct {
	providerPath    string
	decoderPath     string
	manifestPath    string
	manifestPayload []byte
	manifest        ProviderIdentityManifest
}

type stagedProviderBundle struct {
	executablePath string
	decoderPath    string
	manifestPath   string
	identity       ProviderBinaryIdentity
}

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

// Result is deliberately marked as qualification-only. The host binds exact
// staged bytes, but the unsigned operator manifest does not establish binary
// provenance, publisher trust, license qualification, or a complete OS sandbox.
type Result struct {
	Inspection        Inspection
	Validation        cryptoutil.ImageValidation
	OutputPNG         []byte
	Decoder           string
	DecoderVersion    string
	BinaryIdentity    ProviderBinaryIdentity
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

func validProviderSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func sameProviderPath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func sameProviderFileName(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func validProviderFileName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 || filepath.IsAbs(value) || filepath.Base(value) != value || value == "." || value == ".." {
		return false
	}
	if strings.ContainsAny(value, `<>:"/\|?*`) || strings.HasSuffix(value, ".") || strings.HasSuffix(value, " ") {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func providerPathContainsLinkOrReparse(path string) (bool, error) {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	root := volume + string(filepath.Separator)
	current := root
	remainder := strings.TrimPrefix(clean, root)
	for _, segment := range strings.Split(remainder, string(filepath.Separator)) {
		if segment == "" {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			return false, err
		}
		unsafe, err := providerPathEntryIsLinkOrReparse(current, info.Mode())
		if err != nil || unsafe {
			return unsafe, err
		}
	}
	return false, nil
}

func canonicalProviderFile(path string, maximum int64) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("empty_path: %w", ErrProviderIdentity)
	}
	absolute, err := filepath.Abs(path)
	if err != nil || (runtime.GOOS == "windows" && (strings.HasPrefix(absolute, `\\`) || strings.HasPrefix(absolute, "//"))) {
		return "", fmt.Errorf("non_local_absolute_path: %w", ErrProviderIdentity)
	}
	if runtime.GOOS != "windows" {
		original, err := os.Lstat(absolute)
		if err != nil || original.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("linked_path: %w", ErrProviderIdentity)
		}
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return "", fmt.Errorf("linked_path: %w", ErrProviderIdentity)
		}
		absolute = resolved
	}
	unsafePath, err := providerPathContainsLinkOrReparse(absolute)
	if err != nil || unsafePath {
		return "", fmt.Errorf("linked_path: %w", ErrProviderIdentity)
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
		return "", fmt.Errorf("invalid_regular_file: %w", ErrProviderIdentity)
	}
	return absolute, nil
}

func readProviderFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
		return nil, ErrProviderIdentity
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrProviderIdentity
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(payload)) != info.Size() || int64(len(payload)) > maximum {
		return nil, ErrProviderIdentity
	}
	return payload, nil
}

func decodeProviderIdentityManifest(payload []byte) (ProviderIdentityManifest, error) {
	keys := json.NewDecoder(bytes.NewReader(payload))
	token, err := keys.Token()
	if err != nil || token != json.Delim('{') {
		return ProviderIdentityManifest{}, ErrProviderIdentity
	}
	seen := map[string]bool{}
	for keys.More() {
		key, err := keys.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return ProviderIdentityManifest{}, ErrProviderIdentity
		}
		seen[name] = true
		var raw json.RawMessage
		if err := keys.Decode(&raw); err != nil {
			return ProviderIdentityManifest{}, ErrProviderIdentity
		}
	}
	if token, err := keys.Token(); err != nil || token != json.Delim('}') {
		return ProviderIdentityManifest{}, ErrProviderIdentity
	}
	if token, err := keys.Token(); err != io.EOF || token != nil {
		return ProviderIdentityManifest{}, ErrProviderIdentity
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var manifest ProviderIdentityManifest
	if err := decoder.Decode(&manifest); err != nil {
		return ProviderIdentityManifest{}, ErrProviderIdentity
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ProviderIdentityManifest{}, ErrProviderIdentity
	}
	return manifest, nil
}

func expectedProviderDecoderFileName() string {
	if runtime.GOOS == "windows" {
		return "ffmpeg.exe"
	}
	return "ffmpeg"
}

func loadProviderBundle(path string) (providerBundle, error) {
	providerPath, err := canonicalProviderFile(path, maxProviderExecutableBytes)
	if err != nil {
		return providerBundle{}, fmt.Errorf("provider_file: %w", err)
	}
	manifestPath, err := canonicalProviderFile(providerPath+".manifest.json", maxProviderManifestBytes)
	if err != nil {
		return providerBundle{}, fmt.Errorf("manifest_file: %w", err)
	}
	manifestPayload, err := readProviderFile(manifestPath, maxProviderManifestBytes)
	if err != nil {
		return providerBundle{}, fmt.Errorf("manifest_read: %w", err)
	}
	manifest, err := decodeProviderIdentityManifest(manifestPayload)
	if err != nil || manifest.Protocol != ProviderIdentityManifestProtocol ||
		!validProviderFileName(manifest.ProviderFileName) || !sameProviderFileName(manifest.ProviderFileName, filepath.Base(providerPath)) ||
		!validProviderSHA256(manifest.ProviderSHA256) || manifest.DecoderName != "ffmpeg" ||
		!validProviderFileName(manifest.DecoderFileName) || !sameProviderFileName(manifest.DecoderFileName, expectedProviderDecoderFileName()) ||
		!validProviderSHA256(manifest.DecoderSHA256) || manifest.ProviderSourceStatus != ProviderSourceStatus ||
		manifest.DecoderSourceStatus != DecoderSourceStatus ||
		manifest.DecoderDistributionLicenseStatus != DecoderDistributionLicenseStatus {
		return providerBundle{}, fmt.Errorf("manifest_content: %w", ErrProviderIdentity)
	}
	decoderPath, err := canonicalProviderFile(filepath.Join(filepath.Dir(providerPath), manifest.DecoderFileName), maxDecoderExecutableBytes)
	if err != nil || sameProviderPath(providerPath, decoderPath) {
		return providerBundle{}, fmt.Errorf("decoder_file: %w", ErrProviderIdentity)
	}
	return providerBundle{
		providerPath: providerPath, decoderPath: decoderPath, manifestPath: manifestPath,
		manifestPayload: manifestPayload, manifest: manifest,
	}, nil
}

func copyProviderFile(sourcePath, targetPath string, maximum int64, expectedSHA256 string) (returnErr error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return ErrProviderIdentity
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return ErrProviderIdentity
	}
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrProviderIdentity
	}
	succeeded := false
	defer func() {
		_ = target.Close()
		if !succeeded {
			_ = os.Remove(targetPath)
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(target, hash), io.LimitReader(source, maximum+1))
	if err != nil || written != info.Size() || written > maximum || hex.EncodeToString(hash.Sum(nil)) != expectedSHA256 {
		return ErrProviderIdentity
	}
	if err := target.Sync(); err != nil {
		return ErrProviderIdentity
	}
	if err := target.Close(); err != nil {
		return ErrProviderIdentity
	}
	if err := os.Chmod(targetPath, 0o500); err != nil {
		return ErrProviderIdentity
	}
	succeeded = true
	return nil
}

func digestProviderFile(path string, maximum int64) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
		return "", ErrProviderIdentity
	}
	file, err := os.Open(path)
	if err != nil {
		return "", ErrProviderIdentity
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maximum+1))
	if err != nil || written != info.Size() || written > maximum {
		return "", ErrProviderIdentity
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyStagedProviderBundle(bundle stagedProviderBundle) error {
	providerDigest, providerErr := digestProviderFile(bundle.executablePath, maxProviderExecutableBytes)
	decoderDigest, decoderErr := digestProviderFile(bundle.decoderPath, maxDecoderExecutableBytes)
	manifestDigest, manifestErr := digestProviderFile(bundle.manifestPath, maxProviderManifestBytes)
	if providerErr != nil || decoderErr != nil || manifestErr != nil ||
		providerDigest != bundle.identity.ProviderSHA256 || decoderDigest != bundle.identity.DecoderSHA256 ||
		manifestDigest != bundle.identity.ManifestSHA256 {
		return ErrProviderIdentity
	}
	return nil
}

func stageProviderBundle(bundle providerBundle, stage string) (stagedProviderBundle, error) {
	bin := filepath.Join(stage, "provider-bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		return stagedProviderBundle{}, ErrProviderIdentity
	}
	executablePath := filepath.Join(bin, bundle.manifest.ProviderFileName)
	decoderPath := filepath.Join(bin, bundle.manifest.DecoderFileName)
	manifestPath := executablePath + ".manifest.json"
	if err := copyProviderFile(bundle.providerPath, executablePath, maxProviderExecutableBytes, bundle.manifest.ProviderSHA256); err != nil {
		return stagedProviderBundle{}, err
	}
	if err := copyProviderFile(bundle.decoderPath, decoderPath, maxDecoderExecutableBytes, bundle.manifest.DecoderSHA256); err != nil {
		return stagedProviderBundle{}, err
	}
	if err := writePrivateFile(manifestPath, bundle.manifestPayload); err != nil {
		return stagedProviderBundle{}, ErrProviderIdentity
	}
	if err := os.Chmod(manifestPath, 0o400); err != nil {
		return stagedProviderBundle{}, ErrProviderIdentity
	}
	staged := stagedProviderBundle{
		executablePath: executablePath, decoderPath: decoderPath, manifestPath: manifestPath,
		identity: ProviderBinaryIdentity{
			ManifestProtocol: ProviderIdentityManifestProtocol,
			ManifestSHA256:   fileSHA256(bundle.manifestPayload), ProviderSHA256: bundle.manifest.ProviderSHA256,
			DecoderName: bundle.manifest.DecoderName, DecoderSHA256: bundle.manifest.DecoderSHA256,
			IdentityBasis: ProviderIdentityBasis, ProviderSourceStatus: bundle.manifest.ProviderSourceStatus,
			DecoderSourceStatus:              bundle.manifest.DecoderSourceStatus,
			DecoderDistributionLicenseStatus: bundle.manifest.DecoderDistributionLicenseStatus,
			ProviderBinaryTrustStatus:        ProviderBinaryTrustStatus,
		},
	}
	if err := verifyStagedProviderBundle(staged); err != nil {
		return stagedProviderBundle{}, err
	}
	return staged, nil
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
	bundle, err := loadProviderBundle(options.Executable)
	if err != nil {
		return Result{}, fmt.Errorf("provider identity load: %w", err)
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
	stagedBundle, err := stageProviderBundle(bundle, stage)
	if err != nil {
		return Result{}, fmt.Errorf("provider identity stage: %w", err)
	}

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
		ProviderIdentityManifestSHA256: stagedBundle.identity.ManifestSHA256,
		ProviderSHA256:                 stagedBundle.identity.ProviderSHA256,
		DecoderName:                    stagedBundle.identity.DecoderName, DecoderSHA256: stagedBundle.identity.DecoderSHA256,
		DecoderIdentityBasis: stagedBundle.identity.IdentityBasis,
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
	if err := verifyStagedProviderBundle(stagedBundle); err != nil {
		return Result{}, fmt.Errorf("provider identity preflight: %w", err)
	}
	command := newImageDecoderProviderCommand(ctx, stagedBundle.executablePath)
	command.Dir = stage
	command.Env = providerEnvironment(stage)
	command.Stdin = bytes.NewReader(append(payload, '\n'))
	stdout := &limitedBuffer{limit: maxProviderResponseBytes}
	stderr := &limitedBuffer{limit: maxProviderDiagnosticBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = 2 * time.Second
	isolation, runErr := runProviderCommand(command)
	if err := verifyStagedProviderBundle(stagedBundle); err != nil {
		return Result{}, fmt.Errorf("provider identity postflight: %w", err)
	}
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
		response.NetworkUsed == nil || *response.NetworkUsed || response.Decoder != stagedBundle.identity.DecoderName ||
		response.DecoderVersion != "sha256:"+stagedBundle.identity.DecoderSHA256 ||
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
		Decoder: stagedBundle.identity.DecoderName, DecoderVersion: "sha256:" + stagedBundle.identity.DecoderSHA256,
		BinaryIdentity: stagedBundle.identity,
		Isolation:      isolation, ProductionReady: false,
		PromotionBlockers: providerPromotionBlockers(isolation),
	}, nil
}

func (result Result) String() string {
	return fmt.Sprintf("wxgf qualification: %dx%d, decoder=%s, production_ready=%t", result.Validation.Width, result.Validation.Height, result.Decoder, result.ProductionReady)
}
