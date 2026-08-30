package wxgfqual

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var providerHelperMode string

func providerTestPNG(t *testing.T) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 3, 2))
	value.Set(0, 0, color.RGBA{R: 0x21, G: 0x43, B: 0x65, A: 0xff})
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestImageDecoderProviderHelperProcess(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	mode := os.Args[separator+1]
	if mode == "escape_child" {
		if separator+2 >= len(os.Args) {
			os.Exit(7)
		}
		time.Sleep(750 * time.Millisecond)
		if err := os.WriteFile(os.Args[separator+2], []byte("escaped"), 0o600); err != nil {
			os.Exit(8)
		}
		os.Exit(0)
	}
	if mode == "timeout" {
		time.Sleep(5 * time.Second)
		return
	}
	var request providerRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		os.Exit(2)
	}
	if mode == "oversize_response" {
		_, _ = os.Stdout.Write(make([]byte, maxProviderResponseBytes+1))
		os.Exit(0)
	}
	if mode == "unknown_field" {
		_, _ = os.Stdout.Write([]byte(`{"protocol":"` + ProviderProtocol + `","unknown":true}`))
		os.Exit(0)
	}
	if mode == "stderr_overflow" {
		_, _ = os.Stderr.Write(make([]byte, maxProviderDiagnosticBytes+1))
	}
	if mode == "mutate_input" {
		_ = os.Chmod(request.InputPath, 0o600)
		if err := os.WriteFile(request.InputPath, []byte("mutated"), 0o600); err != nil {
			os.Exit(5)
		}
	}
	if mode == "spawn_child" {
		if separator+2 >= len(os.Args) {
			os.Exit(9)
		}
		discard, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			os.Exit(10)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestImageDecoderProviderHelperProcess$", "--", "escape_child", os.Args[separator+2])
		child.Stdout = discard
		child.Stderr = discard
		if err := child.Start(); err != nil {
			_ = discard.Close()
			os.Exit(11)
		}
		_ = child.Process.Release()
		_ = discard.Close()
	}
	output := providerTestPNG(t)
	if mode == "trailing_png" {
		output = append(output, 0)
	}
	if mode != "missing_output" {
		if err := os.WriteFile(request.OutputPath, output, 0o600); err != nil {
			os.Exit(3)
		}
	}
	networkUsed := mode == "network"
	inputDigest := request.InputSHA256
	if mode == "wrong_input_digest" {
		inputDigest = fileSHA256([]byte("wrong"))
	}
	frameCount := 1
	if mode == "multiple_frames" {
		frameCount = 2
	}
	response := providerResponse{
		Protocol: ProviderProtocol, RequestID: request.RequestID, Status: "decoded",
		InputSHA256: inputDigest, OutputSHA256: fileSHA256(output), OutputFormat: "png",
		FrameCount: frameCount, NetworkUsed: &networkUsed, Decoder: request.DecoderName,
		DecoderVersion: "sha256:" + request.DecoderSHA256,
	}
	if mode == "unsafe_metadata" {
		response.Decoder = "test-hevc\ninjected"
	}
	if mode == "wrong_decoder_identity" {
		response.DecoderVersion = "sha256:" + fileSHA256([]byte("wrong-decoder"))
	}
	if mode == "missing_network_claim" {
		response.NetworkUsed = nil
	}
	if mode == "wrong_output_digest" {
		response.OutputSHA256 = fileSHA256([]byte("wrong"))
	}
	if mode == "uppercase_output_digest" {
		response.OutputSHA256 = strings.ToUpper(response.OutputSHA256)
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		os.Exit(4)
	}
	if mode == "trailing_response" {
		_, _ = os.Stdout.Write([]byte("{}"))
	}
	os.Exit(0)
}

func providerIdentityFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	providerName := "provider-test"
	if runtime.GOOS == "windows" {
		providerName += ".exe"
	}
	providerPath := filepath.Join(root, providerName)
	providerPayload := []byte("provider-fixture")
	decoderPayload := []byte("decoder-fixture")
	if err := os.WriteFile(providerPath, providerPayload, 0o700); err != nil {
		t.Fatal(err)
	}
	decoderPath := filepath.Join(root, expectedProviderDecoderFileName())
	if err := os.WriteFile(decoderPath, decoderPayload, 0o500); err != nil {
		t.Fatal(err)
	}
	manifest := ProviderIdentityManifest{
		Protocol: ProviderIdentityManifestProtocol, ProviderFileName: providerName,
		ProviderSHA256: fileSHA256(providerPayload), DecoderName: "ffmpeg",
		DecoderFileName: expectedProviderDecoderFileName(), DecoderSHA256: fileSHA256(decoderPayload),
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(providerPath+".manifest.json", payload, 0o400); err != nil {
		t.Fatal(err)
	}
	return providerPath
}

func withProviderHelper(t *testing.T, mode string) {
	t.Helper()
	previous := newImageDecoderProviderCommand
	providerHelperMode = mode
	newImageDecoderProviderCommand = func(ctx context.Context, _ string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestImageDecoderProviderHelperProcess$", "--", providerHelperMode)
	}
	t.Cleanup(func() {
		newImageDecoderProviderCommand = previous
		providerHelperMode = ""
	})
}

func runProviderTest(t *testing.T, mode string, timeout time.Duration) (Result, error, string) {
	t.Helper()
	withProviderHelper(t, mode)
	root := t.TempDir()
	provider := providerIdentityFixture(t)
	result, err := RunProviderTrial(context.Background(), singlePictureFixture(), ProviderOptions{
		Executable: provider, TemporaryRoot: root, Timeout: timeout,
	})
	return result, err, root
}

func assertStageCleaned(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("WXGF 资格验证遗留了临时明文：%v", entries)
	}
}

func TestRunProviderTrialValidatesBoundPNG(t *testing.T) {
	result, err, root := runProviderTest(t, "success", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	assertStageCleaned(t, root)
	if result.Validation.Format != "png" || result.Validation.Width != 3 || result.Validation.Height != 2 ||
		result.Decoder != "ffmpeg" || result.ProductionReady ||
		result.BinaryIdentity.ManifestProtocol != ProviderIdentityManifestProtocol ||
		result.BinaryIdentity.DecoderName != "ffmpeg" ||
		result.BinaryIdentity.IdentityBasis != ProviderIdentityBasis || !validProviderSHA256(result.BinaryIdentity.ProviderSHA256) ||
		!validProviderSHA256(result.BinaryIdentity.DecoderSHA256) || !validProviderSHA256(result.BinaryIdentity.ManifestSHA256) ||
		result.DecoderVersion != "sha256:"+result.BinaryIdentity.DecoderSHA256 ||
		result.BinaryIdentity.ProviderSourceStatus != ProviderSourceStatus || result.BinaryIdentity.DecoderSourceStatus != DecoderSourceStatus ||
		result.BinaryIdentity.ProviderSignatureStatus != ProviderSignatureStatus ||
		result.BinaryIdentity.DecoderSignatureStatus != DecoderSignatureStatus ||
		result.BinaryIdentity.DecoderDistributionLicenseStatus != DecoderDistributionLicenseStatus ||
		result.BinaryIdentity.ProviderBinaryTrustStatus != ProviderBinaryTrustStatus {
		t.Fatalf("WXGF provider 资格结果异常：%+v", result)
	}
	expectedBlockers := 11
	if runtime.GOOS == "windows" {
		expectedBlockers = 10
		if !result.Isolation.CreateProcessTreeContained || !result.Isolation.JobMemberMemoryLimited || result.Isolation.NetworkIsolated ||
			result.Isolation.FilesystemIsolated || result.Isolation.ActiveProcessLimit != providerActiveProcessLimit {
			t.Fatalf("Windows Job Object 隔离声明异常：%+v", result.Isolation)
		}
	}
	if len(result.PromotionBlockers) != expectedBlockers {
		t.Fatalf("WXGF provider 阻断项异常：%v", result.PromotionBlockers)
	}
	blockers := map[string]bool{}
	for _, blocker := range result.PromotionBlockers {
		blockers[blocker] = true
	}
	requiredBlockers := []string{
		"wxgf_container_layout_not_fully_specified",
		"provider_source_not_verified",
		"decoder_source_not_verified",
		"provider_signature_not_qualified",
		"decoder_signature_not_qualified",
		"os_network_isolation_not_enforced",
		"os_filesystem_credential_isolation_not_enforced",
		"real_fixture_matrix_insufficient",
		"decoded_visual_equivalence_not_confirmed",
		"decoder_distribution_license_not_qualified",
	}
	if runtime.GOOS != "windows" {
		requiredBlockers = append(requiredBlockers, "createprocess_tree_and_job_memory_limits_not_enforced")
	}
	for _, blocker := range requiredBlockers {
		if !blockers[blocker] {
			t.Fatalf("WXGF provider 缺少预期阻断项 %q：%v", blocker, result.PromotionBlockers)
		}
	}
	if len(result.OutputPNG) == 0 || result.String() == "" {
		t.Fatal("资格结果没有保留经过完整验证的 PNG")
	}
}

func TestRunProviderTrialRejectsUntrustedProviderClaims(t *testing.T) {
	for _, mode := range []string{
		"network", "missing_network_claim", "wrong_input_digest", "wrong_output_digest", "uppercase_output_digest",
		"multiple_frames", "unsafe_metadata", "wrong_decoder_identity", "unknown_field", "trailing_response", "oversize_response",
		"stderr_overflow", "mutate_input",
	} {
		t.Run(mode, func(t *testing.T) {
			_, err, root := runProviderTest(t, mode, 3*time.Second)
			if err == nil {
				t.Fatalf("适配器异常声明未被拒绝：%s", mode)
			}
			assertStageCleaned(t, root)
		})
	}
}

func TestProviderEnvironmentIsAllowlisted(t *testing.T) {
	t.Setenv("V_LOCAL_TEST_SECRET", "must-not-cross-provider-boundary")
	stage := t.TempDir()
	environment := providerEnvironment(stage)
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "V_LOCAL_TEST_SECRET") || !strings.Contains(joined, "PATH=") ||
		!strings.Contains(joined, "TMP="+stage) || !strings.Contains(joined, "TEMP="+stage) ||
		!strings.Contains(joined, "TMPDIR="+stage) {
		t.Fatalf("适配器环境变量白名单异常：%q", environment)
	}
}

func TestRunProviderTrialRejectsInvalidOrMissingOutput(t *testing.T) {
	for _, mode := range []string{"trailing_png", "missing_output"} {
		t.Run(mode, func(t *testing.T) {
			_, err, root := runProviderTest(t, mode, 3*time.Second)
			if err == nil {
				t.Fatalf("无效适配器输出未被拒绝：%s", mode)
			}
			assertStageCleaned(t, root)
		})
	}
}

func TestRunProviderTrialTimesOutAndCleansStage(t *testing.T) {
	_, err, root := runProviderTest(t, "timeout", 100*time.Millisecond)
	if err != ErrProviderTimeout {
		t.Fatalf("适配器超时分类异常：%v", err)
	}
	assertStageCleaned(t, root)
}

func TestRunProviderTrialRequiresRegularExecutableAndPrivateRoot(t *testing.T) {
	root := t.TempDir()
	if _, err := RunProviderTrial(context.Background(), singlePictureFixture(), ProviderOptions{
		Executable: filepath.Join(root, "missing"), TemporaryRoot: root,
	}); err == nil {
		t.Fatal("缺失适配器未被拒绝")
	}
	file := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunProviderTrial(context.Background(), singlePictureFixture(), ProviderOptions{
		Executable: os.Args[0], TemporaryRoot: file,
	}); err == nil {
		t.Fatal("非目录临时根未被拒绝")
	}
}

func TestRunProviderTrialRejectsMissingOrChangedIdentityBundle(t *testing.T) {
	for _, mode := range []string{
		"missing_manifest", "provider_changed", "decoder_changed", "manifest_unknown_field", "manifest_duplicate_field",
		"manifest_overstated_source", "manifest_qualified_signature", "manifest_qualified_license",
	} {
		t.Run(mode, func(t *testing.T) {
			provider := providerIdentityFixture(t)
			manifestPath := provider + ".manifest.json"
			switch mode {
			case "missing_manifest":
				if err := os.Chmod(manifestPath, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(manifestPath); err != nil {
					t.Fatal(err)
				}
			case "provider_changed":
				if err := os.WriteFile(provider, []byte("changed-provider"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "decoder_changed":
				decoder := filepath.Join(filepath.Dir(provider), expectedProviderDecoderFileName())
				if err := os.Chmod(decoder, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(decoder, []byte("changed-decoder"), 0o500); err != nil {
					t.Fatal(err)
				}
			case "manifest_unknown_field", "manifest_duplicate_field":
				payload, err := os.ReadFile(manifestPath)
				if err != nil {
					t.Fatal(err)
				}
				addition := `,"unknown":true}`
				if mode == "manifest_duplicate_field" {
					addition = `,"provider_sha256":"` + strings.Repeat("0", 64) + `"}`
				}
				payload = append(bytes.TrimSuffix(payload, []byte("}")), []byte(addition)...)
				if err := os.Chmod(manifestPath, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(manifestPath, payload, 0o400); err != nil {
					t.Fatal(err)
				}
			case "manifest_overstated_source", "manifest_qualified_signature", "manifest_qualified_license":
				payload, err := os.ReadFile(manifestPath)
				if err != nil {
					t.Fatal(err)
				}
				addition := `,"provider_source_status":"verified"}`
				if mode == "manifest_qualified_signature" {
					addition = `,"provider_signature_status":"qualified"}`
				}
				if mode == "manifest_qualified_license" {
					addition = `,"decoder_distribution_license_status":"qualified"}`
				}
				payload = append(bytes.TrimSuffix(payload, []byte("}")), []byte(addition)...)
				if err := os.Chmod(manifestPath, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(manifestPath, payload, 0o400); err != nil {
					t.Fatal(err)
				}
			}
			root := t.TempDir()
			_, err := RunProviderTrial(context.Background(), singlePictureFixture(), ProviderOptions{
				Executable: provider, TemporaryRoot: root, Timeout: time.Second,
			})
			if !errors.Is(err, ErrProviderIdentity) {
				t.Fatalf("身份包异常未被绑定错误拒绝：mode=%s err=%v", mode, err)
			}
			assertStageCleaned(t, root)
		})
	}
}

func TestProviderIdentityBundleRejectsProviderDecoderPathCollision(t *testing.T) {
	root := t.TempDir()
	provider := filepath.Join(root, expectedProviderDecoderFileName())
	payload := []byte("same-provider-decoder-fixture")
	if err := os.WriteFile(provider, payload, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := ProviderIdentityManifest{
		Protocol: ProviderIdentityManifestProtocol, ProviderFileName: filepath.Base(provider), ProviderSHA256: fileSHA256(payload),
		DecoderName: "ffmpeg", DecoderFileName: filepath.Base(provider), DecoderSHA256: fileSHA256(payload),
	}
	manifestPayload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(provider+".manifest.json", manifestPayload, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProviderBundle(provider); !errors.Is(err, ErrProviderIdentity) {
		t.Fatalf("provider/decoder 路径碰撞未被拒绝：%v", err)
	}
}

func TestRunProviderTrialDetectsStagedIdentityReplacement(t *testing.T) {
	for _, target := range []string{"provider", "decoder", "manifest"} {
		t.Run(target, func(t *testing.T) {
			provider := providerIdentityFixture(t)
			root := t.TempDir()
			previous := newImageDecoderProviderCommand
			var mutationErr error
			newImageDecoderProviderCommand = func(ctx context.Context, stagedProvider string) *exec.Cmd {
				path := stagedProvider
				switch target {
				case "decoder":
					path = filepath.Join(filepath.Dir(stagedProvider), expectedProviderDecoderFileName())
				case "manifest":
					path = stagedProvider + ".manifest.json"
				}
				if err := os.Chmod(path, 0o700); err != nil {
					mutationErr = err
				} else {
					mutationErr = os.WriteFile(path, []byte("staged-identity-replaced"), 0o700)
				}
				return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestImageDecoderProviderHelperProcess$", "--", "success")
			}
			t.Cleanup(func() { newImageDecoderProviderCommand = previous })
			_, err := RunProviderTrial(context.Background(), singlePictureFixture(), ProviderOptions{
				Executable: provider, TemporaryRoot: root, Timeout: 3 * time.Second,
			})
			if mutationErr != nil {
				t.Fatalf("测试无法替换 staging %s：%v", target, mutationErr)
			}
			if !errors.Is(err, ErrProviderIdentity) {
				t.Fatalf("staging %s 替换未被拒绝：%v", target, err)
			}
			assertStageCleaned(t, root)
		})
	}
}

func TestProviderIdentityBundleRejectsLinksWhenSupported(t *testing.T) {
	provider := providerIdentityFixture(t)
	manifest := provider + ".manifest.json"
	target := manifest + ".target"
	payload, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, payload, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, manifest); err != nil {
		t.Skipf("当前环境不能创建符号链接：%v", err)
	}
	if _, err := loadProviderBundle(provider); !errors.Is(err, ErrProviderIdentity) {
		t.Fatalf("链接身份清单未被拒绝：%v", err)
	}
}

func TestRealWXGFFixtureQualification(t *testing.T) {
	fixture := os.Getenv("V_LOCAL_TEST_WXGF_FIXTURE")
	provider := os.Getenv("V_LOCAL_TEST_WXGF_PROVIDER")
	if fixture == "" || provider == "" {
		t.Skip("设置 V_LOCAL_TEST_WXGF_FIXTURE 与 V_LOCAL_TEST_WXGF_PROVIDER 后运行真实资格验证")
	}
	info, err := os.Lstat(fixture)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > MaxWXGFBytes {
		t.Fatal("真实 WXGF 夹具不是边界内的普通文件")
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunProviderTrial(context.Background(), data, ProviderOptions{
		Executable: provider, TemporaryRoot: t.TempDir(), Timeout: defaultProviderTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProductionReady || result.Validation.Format != "png" || result.Inspection.PictureCount != 1 {
		t.Fatalf("真实 WXGF 资格结果越权或不完整：%+v", result)
	}
	t.Logf("真实 WXGF 仅通过实验资格检查：method=%s dimensions=%dx%d decoder=%s blockers=%v",
		result.Inspection.Method, result.Validation.Width, result.Validation.Height, result.Decoder, result.PromotionBlockers)
}
