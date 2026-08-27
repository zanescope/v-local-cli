package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zanescope/v-local-cli/internal/nativeocr"
	"github.com/zanescope/v-local-cli/internal/provider"
	"github.com/zanescope/v-local-cli/internal/snapshot"
	"github.com/zanescope/v-local-cli/internal/state"
	_ "modernc.org/sqlite"
)

func runForTest(args ...string) (int, map[string]any, map[string]any) {
	var stdout, stderr bytes.Buffer
	code := Main(args, &stdout, &stderr)
	output := map[string]any{}
	errors := map[string]any{}
	if stdout.Len() > 0 {
		_ = json.Unmarshal(stdout.Bytes(), &output)
	}
	if stderr.Len() > 0 {
		_ = json.Unmarshal(stderr.Bytes(), &errors)
	}
	return code, output, errors
}

func TestSecurityPostureRevalidationRequiresLiveProviderProvenance(t *testing.T) {
	bundle := provider.CandidateBundle{Diagnostics: map[string]any{
		"platform": "darwin", "action_stage": "security_posture_revalidation",
		"requested_scopes": []any{"database"}, "database_target_status": "not_requested",
		"database_coverage_status": "not_requested", "media_coverage_status": "not_requested",
		"routes_attempted": []any{}, "candidate_mode": "none", "candidate_sources": []any{},
		"security_posture_status": "sip_enabled_verified", "result_code": "complete",
		"workflow_status": "terminal", "next_action": "none",
	}}
	if !isLiveProviderSecurityPostureRevalidation("provider", bundle) {
		t.Fatal("live Provider posture revalidation was not recognized")
	}
	if isLiveProviderSecurityPostureRevalidation("candidate_file", bundle) {
		t.Fatal("candidate file was allowed to claim machine posture revalidation")
	}
}

func TestCandidateFileRejectsProviderOnlyCredentialProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	payload := `{
		"database_keys":{"message.db":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		"database_credential":{"mode":"global_passphrase"}
	}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCandidateFile(path); err == nil {
		t.Fatal("user candidate file was allowed to assert Provider-only credential provenance")
	}
}

func TestKeyProviderCommandErrorKeepsMacOSHelperRecoverySimple(t *testing.T) {
	err := keyProviderCommandError(&provider.AcquisitionError{
		Reason: "process_access_denied", Platform: "darwin", HelperStatus: "not_installed",
	})
	if err.typeName != "key_provider_helper_missing" || !strings.Contains(err.hint, "npx @zanescope/v-local-key-provider@latest install") {
		t.Fatalf("unexpected helper recovery: %+v", err)
	}

	err = keyProviderCommandError(&provider.AcquisitionError{
		Reason: "process_access_denied", Platform: "darwin", HelperStatus: "used",
	})
	if err.typeName != "key_provider_permission_denied" || strings.Contains(err.hint, "helper-acquire") {
		t.Fatalf("internal helper operation leaked into user recovery: %+v", err)
	}

	err = keyProviderCommandError(&provider.AcquisitionError{
		Reason: "process_access_denied", Platform: "darwin", HelperStatus: "launch_failed",
	})
	if err.typeName != "key_provider_helper_failed" || !strings.Contains(err.hint, "npx @zanescope/v-local-key-provider@latest install") {
		t.Fatalf("unexpected helper launch recovery: %+v", err)
	}

	err = keyProviderCommandError(&provider.AcquisitionError{
		Reason: "process_access_denied", Platform: "windows", HelperStatus: "not_applicable",
	})
	if err.typeName != "key_provider_permission_denied" || strings.Contains(err.message, "macOS") {
		t.Fatalf("non-Darwin denial used macOS recovery text: %+v", err)
	}

	err = keyProviderCommandError(&provider.AcquisitionError{
		Reason: "sip_required", Platform: "darwin", HelperStatus: "sip_enabled", NextAction: "disable_sip",
		ShadowRouteStatus: "unavailable_in_build", RoutePriority: []string{"standard", "shadow", "sip_disabled"}, ExternalCheckpointStatus: "persisted",
	})
	details, detailsOK := err.details.(map[string]any)
	if err.typeName != "key_provider_sip_required" || !strings.Contains(err.hint, "恢复模式") || !strings.Contains(err.hint, "不会自动启动、退出或重启微信") ||
		!detailsOK || details["shadow_route_status"] != "unavailable_in_build" {
		t.Fatalf("unexpected SIP recovery: %+v", err)
	}

	err = keyProviderCommandError(&provider.AcquisitionError{
		Reason: "sip_required", Platform: "darwin", NextAction: "disable_sip", ExternalCheckpointStatus: "unavailable",
	})
	if err.typeName != "key_provider_external_checkpoint_failed" || !strings.Contains(err.hint, "先恢复 SIP") {
		t.Fatalf("external action proceeded without a durable checkpoint: %+v", err)
	}

	err = keyProviderCommandError(&provider.AcquisitionError{
		Reason: "hook_trigger_required", Platform: "darwin", VersionSupport: "commoncrypto_dynamic",
	})
	if err.typeName != "key_provider_hook_trigger_required" || !strings.Contains(err.hint, "只读数据库页面") || !strings.Contains(err.hint, "15 分钟") {
		t.Fatalf("unexpected hook recovery: %+v", err)
	}

	err = keyProviderCommandError(&provider.AcquisitionError{
		Reason: "hook_restart_required", Platform: "darwin",
	})
	if err.typeName != "key_provider_hook_restart_required" || !strings.Contains(err.hint, "15 分钟") || !strings.Contains(err.hint, "不会自动终止") {
		t.Fatalf("unexpected hook restart recovery: %+v", err)
	}

	err = keyProviderCommandError(&provider.AcquisitionError{
		Reason: "action_confirmation_mismatch", Platform: "darwin", NextAction: "reenable_sip",
		ExternalCheckpointStatus: "persisted", ExternalWorkflowID: strings.Repeat("a", 32),
	})
	if err.typeName != "key_provider_action_confirmation_mismatch" || !strings.Contains(err.hint, "不要用该参数确认 Shadow 或 SIP") {
		t.Fatalf("cross-reboot SIP action rejection was misreported: %+v", err)
	}

	err = keyProviderCommandError(&provider.AcquisitionError{
		Reason: "external_workflow_scope_mismatch", NextAction: "disable_sip",
		RequestedScopes: []string{"database", "media"}, ExternalCheckpointStatus: "persisted",
	})
	if err.typeName != "key_provider_external_workflow_scope_mismatch" || !strings.Contains(err.hint, "原来的") {
		t.Fatalf("external workflow scope drift used an unrelated recovery: %+v", err)
	}

	err = keyProviderCommandError(&provider.AcquisitionError{
		Reason: "process_list_unavailable", Platform: "darwin", ProcessDiscoveryMethod: "ps_then_launchctl",
	})
	if err.typeName != "key_provider_process_list_unavailable" || !strings.Contains(err.hint, "不等同于微信未运行") {
		t.Fatalf("unexpected process discovery recovery: %+v", err)
	}
}

func TestAcquisitionCommandErrorExposesScopeQualifiedCoverage(t *testing.T) {
	err := acquisitionCommandError(&provider.AcquisitionError{
		ResultCode: "partial", WorkflowStatus: "terminal", RequestedScopes: []string{"database", "media"},
		DatabaseCoverageStatus: "complete", MediaCoverageStatus: "none",
	}, "key_provider_failed", "failed", "retry")
	details, ok := err.details.(map[string]any)
	if !ok || details["database_coverage_status"] != "complete" || details["media_coverage_status"] != "none" ||
		details["coverage_status"] != nil || details["media_status"] != nil {
		t.Fatalf("Agent error details did not preserve scope-qualified coverage: %#v", err.details)
	}
	requested, ok := details["requested_scopes"].([]string)
	if !ok || len(requested) != 2 || requested[0] != "database" || requested[1] != "media" {
		t.Fatalf("Agent error details lost requested scope order: %#v", err.details)
	}
}

func TestAcquisitionCommandErrorExposesSafeWindowsPhase4Evidence(t *testing.T) {
	err := acquisitionCommandError(&provider.AcquisitionError{
		Platform: "windows", ConfigCipherRouteStatus: "unavailable_unregistered",
		WindowsRouteEvidence: []string{"registry_no_exact_match"},
		ProcessCount:         3, SelectedProcessCount: 2, TargetBoundProcessCount: 1,
		OtherAccountProcessCount: 1, UnknownAccountProcessCount: 1,
		OpenedProcessCount: 1, AccessDeniedCount: 1, PerProcessCollectorCount: 2,
		FallbackStageCounts: map[string]int{"structured_key_object": 2},
	}, "key_provider_failed", "failed", "retry")
	details, ok := err.details.(map[string]any)
	if !ok || details["config_cipher_route_status"] != "unavailable_unregistered" ||
		details["selected_process_count"] != 2 || details["other_account_process_count"] != 1 {
		t.Fatalf("Agent error details lost Windows Phase 4 state: %#v", err.details)
	}
	evidence, ok := details["windows_route_evidence"].([]string)
	if !ok || len(evidence) != 1 || evidence[0] != "registry_no_exact_match" {
		t.Fatalf("Agent error details lost redacted Windows route evidence: %#v", err.details)
	}
}

func TestStatusFailsClosedOnInvalidExternalWorkflowCheckpoint(t *testing.T) {
	t.Setenv("V_LOCAL_CLI_HOME", t.TempDir())
	root, err := state.AcquisitionRoot()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "external-aaaaaaaaaaaaaaaa.checkpoint.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"authorization":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, failure := runForTest("status")
	errorValue, _ := failure["error"].(map[string]any)
	if code == 0 || errorValue["type"] != "external_workflow_state_invalid" {
		t.Fatalf("invalid external checkpoint was not surfaced explicitly: code=%d failure=%v", code, failure)
	}
	code, output, failure := runForTest("setup", "--cancel-all-external-workflows")
	if code != 0 || output["data"].(map[string]any)["other_private_state_preserved"] != true {
		t.Fatalf("explicit malformed-checkpoint recovery failed: code=%d output=%v failure=%v", code, output, failure)
	}
	if code, _, failure := runForTest("status"); code != 0 {
		t.Fatalf("status remained blocked after narrow checkpoint cleanup: code=%d failure=%v", code, failure)
	}
}

func privateTestSnapshot(t *testing.T, home, accountID, source string) string {
	t.Helper()
	target := filepath.Join(home, "accounts", accountID, "snapshots", "test-generation")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, target); err != nil {
		t.Fatal(err)
	}
	return target
}

func emptyTestGeneration(t *testing.T, home, accountID string) string {
	t.Helper()
	target := filepath.Join(home, "accounts", accountID, "snapshots", "initial-generation")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	return target
}

func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Main([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("退出码=%d stderr=%s", code, stderr.String())
	}
	if stdout.String() != Version+"\n" {
		t.Fatalf("版本输出=%q", stdout.String())
	}
}

func TestCapabilitiesDoNotPromoteBuildTargetsWithoutEmbeddedEvidence(t *testing.T) {
	result, err := runCapabilities(nil)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := result.(map[string]any)
	validation := capabilities["validation_evidence"].(map[string]any)
	if validation["status"] != "not_embedded" || validation["release_manifest_required_for_real_device_claims"] != true ||
		validation["current_runtime_build_target_declared"] != true || validation["current_runtime_build_supported"] != nil {
		t.Fatalf("capabilities promoted an unauthenticated real-device claim: %v", validation)
	}
	providerCapabilities := capabilities["provider"].(map[string]any)
	if providerCapabilities["automatic_key_access_validation"] != "requires_signed_live_release_evidence" || providerCapabilities["user_supplied_candidate_file"] != true {
		t.Fatalf("provider validation boundary is ambiguous: %v", providerCapabilities)
	}
	ocrCapabilities := capabilities["ocr"].(map[string]any)
	targets, ok := ocrCapabilities["native_backend_implementation_targets"].([]string)
	if !ok || len(targets) != 1 || targets[0] != "windows/amd64" {
		t.Fatalf("原生 OCR 支持平台声明扩大：%v", ocrCapabilities)
	}
}

func TestSchemaOnlyListsImplementedCommands(t *testing.T) {
	code, output, errors := runForTest("schema")
	if code != 0 {
		t.Fatalf("退出码=%d error=%v", code, errors)
	}
	data := output["data"].(map[string]any)
	commands := data["commands"].(map[string]any)
	history, found := commands["history"].(map[string]any)
	if !found {
		t.Fatal("schema 缺少 history")
	}
	redPacketFields, _ := history["red_packet_fields"].(string)
	if !strings.Contains(redPacketFields, "receive_status") || !strings.Contains(redPacketFields, "message_date") || !strings.Contains(redPacketFields, "amount_status") || !strings.Contains(redPacketFields, "amount_source") || !strings.Contains(redPacketFields, "amount_kind") {
		t.Fatalf("schema 缺少红包状态、日期或金额契约：%v", history)
	}
	moments := commands["moments"].(map[string]any)
	if moments["interaction_scope"] != "locally_retained_visible_only" || moments["complete_interaction_history"] != false {
		t.Fatalf("schema 缺少朋友圈互动边界：%v", moments)
	}
	momentMedia := commands["export-moment-media"].(map[string]any)
	mediaKinds, kindsOK := momentMedia["media_kinds"].([]any)
	if momentMedia["container_validation"] != "strict" || !kindsOK || len(mediaKinds) != 2 || momentMedia["max_video_bytes"] != float64(512*1024*1024) {
		t.Fatalf("schema 缺少朋友圈媒体严格校验边界：%v", momentMedia)
	}
	chatImage := commands["export-chat-image"].(map[string]any)
	if chatImage["evidence_binding"] != "message_resource_stem+hardlink_map" || chatImage["container_validation"] != "full_decode" || chatImage["network"] != false {
		t.Fatalf("schema 缺少聊天图片强绑定与离线校验边界：%v", chatImage)
	}
	refresh := commands["refresh"].(map[string]any)
	if refresh["reads_saved_keychain"] != true || refresh["reads_process"] != false || refresh["network"] != false || refresh["writes_snapshot"] != true || refresh["modifies_saved_secrets"] != false || refresh["account_lock"] != true || refresh["prevents_coverage_regression"] != true {
		t.Fatalf("schema 缺少 refresh 安全边界：%v", refresh)
	}
	setup := commands["setup"].(map[string]any)
	if setup["reads_process_only_with_authorization"] != true || setup["explicit_action_confirmation"] != true || setup["action_confirmation_option"] != "--confirm-key-action" ||
		setup["external_workflow_cleanup_scope"] != "checkpoint_files_only" {
		t.Fatalf("schema 缺少 acquisition 授权与动作确认分离边界：%v", setup)
	}
	if commands["forget"].(map[string]any)["requires_confirmation"] != true || commands["install"].(map[string]any)["external_installer"] != false {
		t.Fatalf("schema 缺少删除确认或本地 Skill 安装边界")
	}
	if commands["capabilities"].(map[string]any)["real_device_claims_require_embedded_evidence"] != true {
		t.Fatal("schema 缺少平台验证边界")
	}
	if commands["voice-search"].(map[string]any)["writes_private_cache_unless_cached_only"] != true ||
		commands["official-article"].(map[string]any)["network_requires_flag"] != "allow-network" {
		t.Fatal("schema 缺少语音转写缓存或公众号正文联网边界")
	}
	if commands["ocr-file"].(map[string]any)["private_ipc_requires_flag"] != "allow-private-ipc" ||
		commands["ocr-file"].(map[string]any)["vendor_no_sandbox_switch"] != true ||
		commands["ocr-recognize"].(map[string]any)["stores_original_image"] != false ||
		commands["ocr-search"].(map[string]any)["source"] != "wechat_index_probe+v-local-cli_private_cache" {
		t.Fatal("schema 缺少 OCR 私有缓存或原生 OCR 授权边界")
	}
	if len(commands) != 42 {
		t.Fatalf("schema 命令数量异常：%d", len(commands))
	}
}

func TestOCRFileRequiresPrivateIPCAuthorization(t *testing.T) {
	previousStatus, previousRecognize := currentNativeOCR, recognizeNativeOCR
	defer func() { currentNativeOCR, recognizeNativeOCR = previousStatus, previousRecognize }()
	currentNativeOCR = func(bool) nativeocr.Status {
		return nativeocr.Status{Available: true, Platform: runtime.GOOS, Architecture: runtime.GOARCH, WeChatVersion: "4.1.test", PrivateIPC: true}
	}
	invoked := false
	recognizeNativeOCR = func(context.Context, string) (nativeocr.Result, error) {
		invoked = true
		return nativeocr.Result{}, nil
	}
	imageValue := image.NewRGBA(image.Rect(0, 0, 2, 2))
	path := filepath.Join(t.TempDir(), "input.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, imageValue); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	code, _, errorOutput := runForTest("ocr-file", path)
	if code == 0 || errorOutput["error"].(map[string]any)["type"] != "wechat_native_ocr_authorization_required" || invoked {
		t.Fatalf("原生 OCR 未保持显式私有 IPC 授权：code=%d error=%v invoked=%v", code, errorOutput, invoked)
	}
}

func TestOCRFileAuthorizationIsPerInvocation(t *testing.T) {
	previousStatus, previousRecognize := currentNativeOCR, recognizeNativeOCR
	defer func() { currentNativeOCR, recognizeNativeOCR = previousStatus, previousRecognize }()
	currentNativeOCR = func(bool) nativeocr.Status {
		return nativeocr.Status{Available: true, Platform: runtime.GOOS, Architecture: runtime.GOARCH, WeChatVersion: "4.1.test", PrivateIPC: true}
	}
	invocations := 0
	recognizeNativeOCR = func(context.Context, string) (nativeocr.Result, error) {
		invocations++
		return nativeocr.Result{Text: "ok"}, nil
	}
	imageValue := image.NewRGBA(image.Rect(0, 0, 2, 2))
	path := filepath.Join(t.TempDir(), "input.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, imageValue); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	code, _, errorOutput := runForTest("ocr-file", "--allow-private-ipc=false", path)
	if code == 0 || errorOutput["error"].(map[string]any)["type"] != "wechat_native_ocr_authorization_required" || invocations != 0 {
		t.Fatalf("显式 false 不得授权私有 IPC：code=%d error=%v invocations=%d", code, errorOutput, invocations)
	}
	code, _, errorOutput = runForTest("ocr-file", "--allow-private-ipc", path)
	if code != 0 || invocations != 1 {
		t.Fatalf("本次显式授权应只调用一次私有 IPC：code=%d error=%v invocations=%d", code, errorOutput, invocations)
	}
	code, _, errorOutput = runForTest("ocr-file", path)
	if code == 0 || errorOutput["error"].(map[string]any)["type"] != "wechat_native_ocr_authorization_required" || invocations != 1 {
		t.Fatalf("授权不得复用到后续调用：code=%d error=%v invocations=%d", code, errorOutput, invocations)
	}
	code, _, errorOutput = runForTest("ocr-file", path, "--allow-private-ipc")
	if code == 0 || errorOutput["error"].(map[string]any)["type"] != "invalid_arguments" || invocations != 1 {
		t.Fatalf("位置参数后的授权标志不得被接受：code=%d error=%v invocations=%d", code, errorOutput, invocations)
	}
}

func TestOutputPublishingUsesRandomPrivateSiblings(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "result.json")
	legacyTemporary := fmt.Sprintf("%s.%d.tmp", target, os.Getpid())
	if err := os.WriteFile(legacyTemporary, []byte("legacy-temp"), 0o600); err != nil {
		t.Fatal(err)
	}
	temporary, err := writeTemporaryFileNear(target, []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if temporary == legacyTemporary {
		t.Fatal("临时输出不得复用可预测的旧命名")
	}
	legacyPayload, err := os.ReadFile(legacyTemporary)
	if err != nil || string(legacyPayload) != "legacy-temp" {
		t.Fatalf("随机临时输出覆盖了预置同目录文件：payload=%q err=%v", legacyPayload, err)
	}
	if err := os.WriteFile(target, []byte("old-target"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixedOld := target + ".old"
	if err := os.WriteFile(fixedOld, []byte("user-owned-old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publishFile(temporary, target); err != nil {
		t.Fatal(err)
	}
	published, err := os.ReadFile(target)
	if err != nil || string(published) != "new" {
		t.Fatalf("发布结果错误：payload=%q err=%v", published, err)
	}
	oldPayload, err := os.ReadFile(fixedOld)
	if err != nil || string(oldPayload) != "user-owned-old" {
		t.Fatalf("覆盖输出误伤了固定 .old 同名文件：payload=%q err=%v", oldPayload, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(directory, ".v-local-cli-backup-*.old"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("随机备份未清理：%v err=%v", leftovers, err)
	}
}

func TestNoForcePublishingRejectsConcurrentTargetCreation(t *testing.T) {
	directory := t.TempDir()
	target, err := prepareOutputTarget(filepath.Join(directory, "result.json"), false)
	if err != nil {
		t.Fatal(err)
	}
	temporary, err := writeTemporaryFileNear(target, []byte("trusted-output"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("concurrent-owner"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publishNewFile(temporary, target); err == nil {
		t.Fatal("目标在发布前被并发创建时仍然覆盖成功")
	}
	payload, err := os.ReadFile(target)
	if err != nil || string(payload) != "concurrent-owner" {
		t.Fatalf("无覆盖发布改写了并发创建的目标：payload=%q err=%v", payload, err)
	}
	if _, err := os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatalf("发布冲突后临时输出未清理：%v", err)
	}
}

func TestForcePublishingBreaksHardlinkWithoutMutatingPeer(t *testing.T) {
	directory := t.TempDir()
	peer := filepath.Join(directory, "peer.json")
	target := filepath.Join(directory, "result.json")
	if err := os.WriteFile(peer, []byte("peer-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(peer, target); err != nil {
		t.Skipf("当前文件系统不支持硬链接：%v", err)
	}
	prepared, err := prepareOutputTarget(target, true)
	if err != nil {
		t.Fatal(err)
	}
	temporary, err := writeTemporaryFileNear(prepared, []byte("new-output"))
	if err != nil {
		t.Fatal(err)
	}
	if err := publishFile(temporary, prepared); err != nil {
		t.Fatal(err)
	}
	peerPayload, peerErr := os.ReadFile(peer)
	targetPayload, targetErr := os.ReadFile(target)
	if peerErr != nil || string(peerPayload) != "peer-content" || targetErr != nil || string(targetPayload) != "new-output" {
		t.Fatalf("覆盖硬链接改写了同 inode 的其它名称：peer=%q peerErr=%v target=%q targetErr=%v", peerPayload, peerErr, targetPayload, targetErr)
	}
}

func TestPrepareOutputTargetRejectsSymbolicLink(t *testing.T) {
	directory := t.TempDir()
	peer := filepath.Join(directory, "peer.json")
	link := filepath.Join(directory, "result.json")
	if err := os.WriteFile(peer, []byte("peer-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(peer, link); err != nil {
		t.Skipf("当前环境不允许创建符号链接：%v", err)
	}
	if _, err := prepareOutputTarget(link, true); err == nil {
		t.Fatal("输出目标符号链接未被拒绝")
	}
	payload, err := os.ReadFile(peer)
	if err != nil || string(payload) != "peer-content" {
		t.Fatalf("拒绝符号链接时改写了链接目标：payload=%q err=%v", payload, err)
	}
}

func TestOCRFileRejectsTruncatedImageBeforePrivateIPC(t *testing.T) {
	previousStatus, previousRecognize := currentNativeOCR, recognizeNativeOCR
	defer func() { currentNativeOCR, recognizeNativeOCR = previousStatus, previousRecognize }()
	currentNativeOCR = func(bool) nativeocr.Status {
		return nativeocr.Status{Available: true, PrivateIPC: true}
	}
	invoked := false
	recognizeNativeOCR = func(context.Context, string) (nativeocr.Result, error) {
		invoked = true
		return nativeocr.Result{}, nil
	}
	path := filepath.Join(t.TempDir(), "truncated.png")
	if err := os.WriteFile(path, []byte{0x89, 'P', 'N', 'G'}, 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errorOutput := runForTest("ocr-file", path)
	if code == 0 || errorOutput["error"].(map[string]any)["type"] != "ocr_input_invalid" || invoked {
		t.Fatalf("损坏图片不应进入私有 OCR：code=%d error=%v invoked=%v", code, errorOutput, invoked)
	}
}

func TestVoiceStatusDoesNotInstallDependency(t *testing.T) {
	t.Setenv("V_LOCAL_CLI_WHISPER_BIN", filepath.Join(t.TempDir(), "missing-whisper-cli"))
	t.Setenv("V_LOCAL_CLI_WHISPER_MODEL", filepath.Join(t.TempDir(), "missing-model.bin"))
	code, output, errorOutput := runForTest("voice-status")
	if code != 0 {
		t.Fatalf("voice-status 异常：code=%d error=%v", code, errorOutput)
	}
	data := output["data"].(map[string]any)
	if data["transcription_backend_ready"] != false || data["automatic_download"] != false || data["install_consent_required"] != true {
		t.Fatalf("voice-status 未保留用户安装选择权：%v", data)
	}
	if _, found := data["engine_path"]; found {
		t.Fatalf("voice-status 默认不应暴露本机路径：%v", data)
	}
}

func TestVoiceModelLanguageCompatibility(t *testing.T) {
	if voiceModelSupportsLanguage("ggml-base.en.bin", "zh") {
		t.Fatal("中文转写不应接受英文专用模型")
	}
	if !voiceModelSupportsLanguage("ggml-base.en.bin", "en") {
		t.Fatal("英文转写应接受英文专用模型")
	}
	if !voiceModelSupportsLanguage("ggml-base.bin", "zh") {
		t.Fatal("中文转写应接受多语言模型")
	}
}

func TestVoiceSearchRequestsDependencyConsentAndSupportsCachedOnly(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	t.Setenv("V_LOCAL_CLI_WHISPER_BIN", filepath.Join(t.TempDir(), "missing-whisper-cli"))
	t.Setenv("V_LOCAL_CLI_WHISPER_MODEL", filepath.Join(t.TempDir(), "missing-model.bin"))
	source := filepath.Join(t.TempDir(), "snapshot")
	chat := "wxid_voice_search"
	contactPath := filepath.Join(source, "contact", "contact.db")
	if err := os.MkdirAll(filepath.Dir(contactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	contactDB, err := sql.Open("sqlite", contactPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contactDB.Exec("CREATE TABLE contact(username TEXT,alias TEXT,remark TEXT,nick_name TEXT); INSERT INTO contact VALUES(?,'','','')", chat); err != nil {
		t.Fatal(err)
	}
	_ = contactDB.Close()
	messagePath := filepath.Join(source, "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(messagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	messageDB, err := sql.Open("sqlite", messagePath)
	if err != nil {
		t.Fatal(err)
	}
	tableDigest := md5.Sum([]byte(chat))
	table := "Msg_" + hex.EncodeToString(tableDigest[:])
	if _, err := messageDB.Exec("CREATE TABLE [" + table + "](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT); INSERT INTO [" + table + "] VALUES(1,9001,34,1700000000000,1700000000,'voice')"); err != nil {
		t.Fatal(err)
	}
	_ = messageDB.Close()
	accountID := state.AccountID(t.TempDir())
	snapshotPath := privateTestSnapshot(t, home, accountID, source)
	initialized := state.AccountState{AccountID: accountID, AccountName: "voice-test", SnapshotPath: snapshotPath, Storage: "snapshot-only"}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	code, _, errorOutput := runForTest("voice-search", "--all", "关键词")
	if code == 0 || errorOutput["error"].(map[string]any)["type"] != "voice_dependency_required" {
		t.Fatalf("语音搜索未请求可选依赖授权：code=%d error=%v", code, errorOutput)
	}
	code, output, errorOutput := runForTest("voice-search", "--all", "--cached-only", "关键词")
	if code != 0 || output["data"].(map[string]any)["count"].(float64) != 0 {
		t.Fatalf("语音缓存降级搜索异常：code=%d output=%v error=%v", code, output, errorOutput)
	}
	cachePath, err := state.VoiceTranscriptPath(accountID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("--cached-only 不应创建空暂存库：%v", err)
	}
}

func TestVoiceAndOCRPreferWeChatExistingIndexes(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	t.Setenv("V_LOCAL_CLI_WHISPER_BIN", filepath.Join(t.TempDir(), "missing-whisper-cli"))
	t.Setenv("V_LOCAL_CLI_WHISPER_MODEL", filepath.Join(t.TempDir(), "missing-model.bin"))
	source := filepath.Join(t.TempDir(), "snapshot")
	chat := "wxid_existing_index"
	contactPath := filepath.Join(source, "contact", "contact.db")
	if err := os.MkdirAll(filepath.Dir(contactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	contactDB, err := sql.Open("sqlite", contactPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contactDB.Exec("CREATE TABLE contact(username TEXT,alias TEXT,remark TEXT,nick_name TEXT); INSERT INTO contact VALUES(?,'','','')", chat); err != nil {
		t.Fatal(err)
	}
	_ = contactDB.Close()
	messagePath := filepath.Join(source, "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(messagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	messageDB, err := sql.Open("sqlite", messagePath)
	if err != nil {
		t.Fatal(err)
	}
	tableDigest := md5.Sum([]byte(chat))
	table := "Msg_" + hex.EncodeToString(tableDigest[:])
	if _, err := messageDB.Exec("CREATE TABLE [" + table + "](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT);" +
		"INSERT INTO [" + table + "] VALUES(11,9001,34,1700000002000,1700000002,'voice');" +
		"INSERT INTO [" + table + "] VALUES(12,9002,3,1700000001000,1700000001,'image')"); err != nil {
		t.Fatal(err)
	}
	_ = messageDB.Close()
	indexDB, err := sql.Open("sqlite", filepath.Join(source, "message", "message_fts.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexDB.Exec(`
		CREATE TABLE name2id(username TEXT);
		INSERT INTO name2id(rowid,username) VALUES(7,?);
		CREATE TABLE message_fts_v4_0(acontent TEXT,local_type INTEGER,message_local_id INTEGER,session_id INTEGER);
		CREATE TABLE ImgFts0V0(acontent TEXT,local_type INTEGER,message_local_id INTEGER,session_id INTEGER);
		INSERT INTO message_fts_v4_0 VALUES('已有语音关键词转写',34,11,7);
		INSERT INTO ImgFts0V0 VALUES('已有图片关键词文字',3,12,7);
	`, chat); err != nil {
		t.Fatal(err)
	}
	_ = indexDB.Close()
	accountID := state.AccountID(t.TempDir())
	snapshotPath := privateTestSnapshot(t, home, accountID, source)
	initialized := state.AccountState{AccountID: accountID, AccountName: "existing-index-test", SnapshotPath: snapshotPath, Storage: "snapshot-only"}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}

	code, output, errorOutput := runForTest("voice-search", "--all", "关键词")
	if code != 0 || output["data"].(map[string]any)["count"].(float64) != 1 {
		t.Fatalf("语音搜索没有优先复用微信索引：code=%d output=%v error=%v", code, output, errorOutput)
	}
	voiceCoverage := output["data"].(map[string]any)["transcript_source_coverage"].(map[string]any)
	if voiceCoverage["wechat_existing_index"].(float64) != 1 || voiceCoverage["wechat_private_ipc_invoked"] != false {
		t.Fatalf("语音索引来源标记异常：%v", voiceCoverage)
	}
	voiceEvidence := output["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["evidence_id"].(string)
	code, output, errorOutput = runForTest("voice-transcribe", voiceEvidence)
	if code != 0 || output["data"].(map[string]any)["source"] != "wechat_existing_index" {
		t.Fatalf("单条语音未复用微信已有转写：code=%d output=%v error=%v", code, output, errorOutput)
	}

	code, output, errorOutput = runForTest("ocr-search", "--all", "关键词")
	if code != 0 || output["data"].(map[string]any)["count"].(float64) != 1 {
		t.Fatalf("OCR 搜索没有读取微信已有索引：code=%d output=%v error=%v", code, output, errorOutput)
	}
	imageEvidence := output["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["evidence_id"].(string)
	code, output, errorOutput = runForTest("ocr-read", imageEvidence)
	if code != 0 || output["data"].(map[string]any)["backend"] != "wechat_existing_index" {
		t.Fatalf("单张图片未读取微信已有 OCR：code=%d output=%v error=%v", code, output, errorOutput)
	}
}

func TestMomentsAndOfficialCommands(t *testing.T) {
	home := testHome(t)
	snapshot := filepath.Join(t.TempDir(), "snapshot")
	accountPath := t.TempDir()
	contactPath := filepath.Join(snapshot, "contact", "contact.db")
	if err := os.MkdirAll(filepath.Dir(contactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	contactDB, err := sql.Open("sqlite", contactPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contactDB.Exec("CREATE TABLE contact(username TEXT, alias TEXT, remark TEXT, nick_name TEXT, delete_flag INTEGER); INSERT INTO contact VALUES('wxid_author','','朋友圈作者','作者',0); INSERT INTO contact VALUES('gh_example','example','公众号备注','公众号',0)"); err != nil {
		t.Fatal(err)
	}
	_ = contactDB.Close()

	imageValue := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageValue.Set(0, 0, color.RGBA{R: 0x25, G: 0x50, B: 0x75, A: 0xff})
	var imageBuffer bytes.Buffer
	if err := png.Encode(&imageBuffer, imageValue); err != nil {
		t.Fatal(err)
	}
	plain := imageBuffer.Bytes()
	digestValue := md5.Sum(plain)
	digest := hex.EncodeToString(digestValue[:])
	momentXML := fmt.Sprintf(`<SnsDataItem><TimelineObject><id>9</id><username>wxid_author</username><createTime>1700000000</createTime><contentDesc>测试朋友圈关键词</contentDesc><ContentObject><type>1</type><mediaList><media><SnsDataItem><id>%s</id><type>2</type><url md5="%s">https://cdn.invalid/resource/%s/0</url></SnsDataItem></media></mediaList></ContentObject></TimelineObject><LocalExtraInfo><like_flag>1</like_flag><like_user_list><user_comment><comment_id>1</comment_id><comment_64id>11</comment_64id><type>1</type><username>wxid_liker</username><nickname>点赞者</nickname><create_time>1700000010</create_time><ref_comment_id>0</ref_comment_id><ref_comment_64id>0</ref_comment_64id></user_comment></like_user_list><comment_user_list><user_comment><comment_id>2</comment_id><comment_64id>22</comment_64id><type>2</type><username>wxid_commenter</username><nickname>评论者</nickname><create_time>1700000020</create_time><content>评论搜索词</content><ref_comment_id>0</ref_comment_id><ref_comment_64id>0</ref_comment_64id></user_comment></comment_user_list></LocalExtraInfo></SnsDataItem>`, digest, digest, digest)
	snsPath := filepath.Join(snapshot, "sns", "sns.db")
	if err := os.MkdirAll(filepath.Dir(snsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	snsDB, err := sql.Open("sqlite", snsPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snsDB.Exec("CREATE TABLE SnsTimeLine(tid INTEGER,user_name TEXT,content BLOB,pack_info_buf BLOB); INSERT INTO SnsTimeLine VALUES(9,'wxid_author',?,X'00')", momentXML); err != nil {
		t.Fatal(err)
	}
	_ = snsDB.Close()
	mediaPath := filepath.Join(accountPath, "cache", digest+".png")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, plain, 0o600); err != nil {
		t.Fatal(err)
	}

	officialXML := `<msg><appmsg><type>5</type><mmreader><publisher><nickname>公众号</nickname><username>gh_example</username></publisher><category count="1"><item><title>搜索标题</title><digest>公众号摘要</digest><url>https://mp.weixin.qq.com/s?__biz=MzA1234%3D&amp;mid=2247483647&amp;idx=1&amp;sn=0123456789abcdef0123456789abcdef</url><pub_time>1700000100</pub_time></item></category></mmreader></appmsg></msg>`
	bizPath := filepath.Join(snapshot, "message", "biz_message_0.db")
	if err := os.MkdirAll(filepath.Dir(bizPath), 0o700); err != nil {
		t.Fatal(err)
	}
	bizDB, err := sql.Open("sqlite", bizPath)
	if err != nil {
		t.Fatal(err)
	}
	tableSum := md5.Sum([]byte("gh_example"))
	table := "Msg_" + hex.EncodeToString(tableSum[:])
	statements := []string{
		"CREATE TABLE Name2Id(user_name TEXT)",
		"INSERT INTO Name2Id(rowid,user_name) VALUES(1,'gh_example')",
		"CREATE TABLE [" + table + "](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT)",
	}
	for _, statement := range statements {
		if _, err := bizDB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := bizDB.Exec("INSERT INTO ["+table+"] VALUES(1,101,21474836529,1700000200000,1700000200,?)", officialXML); err != nil {
		t.Fatal(err)
	}
	_ = bizDB.Close()

	t.Setenv("V_LOCAL_CLI_HOME", home)
	accountID := state.AccountID("content-account")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	account := state.AccountState{AccountID: accountID, AccountName: "test", AccountPath: accountPath, SnapshotPath: snapshot, Storage: "snapshot-only"}
	if err := state.Save(&account); err != nil {
		t.Fatal(err)
	}

	code, output, errorOutput := runForTest("moments", "--all", "--resolve-media", "wxid_author")
	if code != 0 {
		t.Fatalf("moments 退出码=%d output=%v error=%v", code, output, errorOutput)
	}
	data := output["data"].(map[string]any)
	meta := output["meta"].(map[string]any)
	coverage := data["moment_source_coverage"].(map[string]any)
	resolution := coverage["media_resolution"].(map[string]any)
	if data["count"].(float64) != 1 || resolution["verified_local_media"].(float64) != 1 || coverage["visible_likes"].(float64) != 1 || coverage["visible_comments"].(float64) != 1 || meta["unbounded_by_limit"] != true || meta["result_limit"] != nil {
		t.Fatalf("朋友圈命令结果异常：%v", data)
	}
	serialized, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	// 同时检查正斜杠形式：Windows 上路径以反斜杠出现、JSON 又转义成双反斜杠，
	// 单反斜杠的 accountPath 会匹配不到而假通过；正斜杠形式能跨平台抓到泄露。
	for _, leak := range []string{mediaPath, accountPath, filepath.ToSlash(mediaPath), filepath.ToSlash(accountPath), "source_path"} {
		if bytes.Contains(serialized, []byte(leak)) {
			t.Fatalf("朋友圈查询输出泄露本地媒体路径：%s", leak)
		}
	}
	mediaEvidenceID := data["items"].([]any)[0].(map[string]any)["media"].([]any)[0].(map[string]any)["evidence_id"].(string)
	momentImageOutput := filepath.Join(t.TempDir(), "moment.png")
	code, output, errorOutput = runForTest("export-moment-media", "--output", momentImageOutput, mediaEvidenceID)
	momentExport := output["data"].(map[string]any)
	if code != 0 || momentExport["media_kind"] != "image" || momentExport["network_access_performed"] != false || momentExport["container_validation"] != "full_decode" || momentExport["decryption_scope"] != "local_cache" || momentExport["descriptor_md5_status"] != "not_applicable" || momentExport["descriptor_size_status"] != "not_applicable" {
		t.Fatalf("朋友圈本地图片导出异常：code=%d output=%v error=%v", code, output, errorOutput)
	}
	exported, err := os.ReadFile(momentImageOutput)
	if err != nil || !bytes.Equal(exported, plain) {
		t.Fatalf("朋友圈本地图片内容异常：bytes=%d err=%v", len(exported), err)
	}
	code, _, errorOutput = runForTest("export-moment-media", "--output", momentImageOutput, mediaEvidenceID)
	if code == 0 || errorOutput["error"].(map[string]any)["type"] != "output_exists" {
		t.Fatalf("朋友圈图片导出未保护已有输出：code=%d error=%v", code, errorOutput)
	}
	code, output, errorOutput = runForTest("moments-search", "--all", "--contact", "wxid_author", "关键词")
	if code != 0 || output["data"].(map[string]any)["count"].(float64) != 1 {
		t.Fatalf("moments-search 异常：code=%d output=%v error=%v", code, output, errorOutput)
	}
	code, output, errorOutput = runForTest("moments-search", "--all", "--contact", "wxid_author", "评论搜索词")
	if code != 0 || output["data"].(map[string]any)["count"].(float64) != 1 {
		t.Fatalf("朋友圈评论搜索异常：code=%d output=%v error=%v", code, output, errorOutput)
	}
	matched := output["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["matched_fields"].([]any)
	if len(matched) != 1 || !strings.Contains(matched[0].(string), "interactions.comments.") {
		t.Fatalf("朋友圈评论搜索字段异常：%v", matched)
	}
	code, output, errorOutput = runForTest("official-history", "--all", "gh_example")
	if code != 0 || output["data"].(map[string]any)["count"].(float64) != 1 || output["meta"].(map[string]any)["unbounded_by_limit"] != true {
		t.Fatalf("official-history 异常：code=%d output=%v error=%v", code, output, errorOutput)
	}
	officialEvidenceID := output["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["evidence_id"].(string)
	code, _, errorOutput = runForTest("official-article", officialEvidenceID)
	if code == 0 || errorOutput["error"].(map[string]any)["type"] != "official_article_network_authorization_required" {
		t.Fatalf("公众号正文命令未先请求逐篇联网授权：code=%d error=%v", code, errorOutput)
	}
	code, _, errorOutput = runForTest("official-article", "--allow-network=false", officialEvidenceID)
	if code == 0 || errorOutput["error"].(map[string]any)["type"] != "official_article_network_authorization_required" {
		t.Fatalf("公众号正文显式 false 不得取得联网授权：code=%d error=%v", code, errorOutput)
	}
	code, output, errorOutput = runForTest("official-search", "--all", "--publisher", "gh_example", "搜索标题")
	if code != 0 || output["data"].(map[string]any)["count"].(float64) != 1 {
		t.Fatalf("official-search 异常：code=%d output=%v error=%v", code, output, errorOutput)
	}
	if strings.Contains(fmt.Sprint(output), "文章正文") {
		t.Fatalf("公众号搜索不应声称包含文章正文：%v", output)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, output := runForTest("not-real")
	if code != 2 {
		t.Fatalf("退出码=%d", code)
	}
	errorValue := output["error"].(map[string]any)
	if errorValue["type"] != "unknown_command" {
		t.Fatalf("错误=%v", errorValue)
	}
}

func TestResolveTimeWindowDefaults(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 34, 56, 0, time.FixedZone("test", 8*60*60))

	contact, err := resolveTimeWindow("wxid_contact", "", "", false, now)
	if err != nil {
		t.Fatal(err)
	}
	if contact.Mode != "default_contact_month" || contact.ChatType != "contact" || !contact.DefaultApplied ||
		contact.Start == nil || *contact.Start != "2026-08-01" || contact.End == nil || *contact.End != "2026-08-10" {
		t.Fatalf("联系人默认时间窗异常：%+v", contact)
	}

	group, err := resolveTimeWindow("123@chatroom", "", "", false, now)
	if err != nil {
		t.Fatal(err)
	}
	if group.Mode != "default_group_day" || group.Start == nil || *group.Start != "2026-08-10" {
		t.Fatalf("群聊默认时间窗异常：%+v", group)
	}

	crossChat, err := resolveTimeWindow("", "", "", false, now)
	if err != nil {
		t.Fatal(err)
	}
	if crossChat.Mode != "default_cross_chat_day" || crossChat.ChatType != "cross_chat" {
		t.Fatalf("跨会话默认时间窗异常：%+v", crossChat)
	}
}

func TestResolveTimeWindowExplicitAndAll(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 34, 56, 0, time.Local)
	explicit, err := resolveTimeWindow("wxid_contact", "2026-07-02", "", false, now)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Mode != "explicit" || explicit.DefaultApplied || explicit.Start == nil || *explicit.Start != "2026-07-02" || explicit.End != nil {
		t.Fatalf("显式时间窗异常：%+v", explicit)
	}
	all, err := resolveTimeWindow("wxid_contact", "", "", true, now)
	if err != nil {
		t.Fatal(err)
	}
	if all.Mode != "all" || all.StartTimestamp != nil || all.EndTimestamp != nil {
		t.Fatalf("全部时间窗异常：%+v", all)
	}
}

func TestResolveTimeWindowRejectsInvalidValues(t *testing.T) {
	now := time.Now()
	_, err := resolveTimeWindow("wxid_contact", "2026-08-01", "", true, now)
	var commandErr *commandError
	if !errors.As(err, &commandErr) || commandErr.typeName != "conflicting_time_window" {
		t.Fatalf("冲突时间窗错误异常：%v", err)
	}
	_, err = resolveTimeWindow("wxid_contact", "2026-08-11", "2026-08-10", false, now)
	if !errors.As(err, &commandErr) || commandErr.typeName != "invalid_time_window" {
		t.Fatalf("反向时间窗错误异常：%v", err)
	}
	_, err = resolveTimeWindow("wxid_contact", "2026/08/01", "", false, now)
	if !errors.As(err, &commandErr) || commandErr.typeName != "invalid_date" {
		t.Fatalf("日期格式错误异常：%v", err)
	}
}

func TestHistoryReturnsTimeWindowMetadata(t *testing.T) {
	home := testHome(t)
	snapshot := filepath.Join(t.TempDir(), "snapshot")
	messagePath := filepath.Join(snapshot, "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(messagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", messagePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum([]byte("alice"))
	table := "Msg_" + hex.EncodeToString(sum[:])
	timestamp := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.Local).Unix()
	statement := "CREATE TABLE [" + table + "](local_id INTEGER, server_id INTEGER, local_type INTEGER, sort_seq INTEGER, create_time INTEGER, message_content TEXT)"
	if _, err := database.Exec(statement); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO ["+table+"] VALUES(1,2,1,?,?,?)", timestamp*1000, timestamp, "时间范围消息"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("V_LOCAL_CLI_HOME", home)
	accountID := state.AccountID("test-account")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	account := state.AccountState{AccountID: accountID, AccountName: "test", SnapshotPath: snapshot, Storage: "snapshot-only"}
	if err := state.Save(&account); err != nil {
		t.Fatal(err)
	}
	code, output, errorOutput := runForTest("history", "--start", "2026-08-01", "--end", "2026-08-01", "--limit", "10", "alice")
	if code != 0 {
		t.Fatalf("history 退出码=%d output=%v error=%v", code, output, errorOutput)
	}
	data := output["data"].(map[string]any)
	if data["count"].(float64) != 1 {
		t.Fatalf("history 数量异常：%v", data)
	}
	meta := output["meta"].(map[string]any)
	window := meta["time_window"].(map[string]any)
	if window["mode"] != "explicit" || window["start"] != "2026-08-01" || window["end"] != "2026-08-01" || meta["untrusted"] != true {
		t.Fatalf("history 元数据异常：%v", meta)
	}
}

func TestAllWithoutExplicitLimitIsUnbounded(t *testing.T) {
	home := testHome(t)
	snapshot := filepath.Join(t.TempDir(), "snapshot")
	messagePath := filepath.Join(snapshot, "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(messagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", messagePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum([]byte("alice"))
	table := "Msg_" + hex.EncodeToString(sum[:])
	statement := "CREATE TABLE [" + table + "](local_id INTEGER, server_id INTEGER, local_type INTEGER, sort_seq INTEGER, create_time INTEGER, message_content TEXT)"
	if _, err := database.Exec(statement); err != nil {
		t.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.Local).Unix()
	for index := 1; index <= 1005; index++ {
		if _, err := transaction.Exec("INSERT INTO ["+table+"] VALUES(?,?,?,?,?,?)", index, index, 1, index, timestamp, "消息"); err != nil {
			t.Fatal(err)
		}
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("V_LOCAL_CLI_HOME", home)
	accountID := state.AccountID("all-account")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	account := state.AccountState{AccountID: accountID, AccountName: "test", SnapshotPath: snapshot, Storage: "snapshot-only"}
	if err := state.Save(&account); err != nil {
		t.Fatal(err)
	}

	code, output, errorOutput := runForTest("history", "--all", "alice")
	if code != 0 {
		t.Fatalf("history --all 退出码=%d output=%v error=%v", code, output, errorOutput)
	}
	data := output["data"].(map[string]any)
	meta := output["meta"].(map[string]any)
	if data["count"].(float64) != 1005 || meta["unbounded_by_limit"] != true || meta["result_limit"] != nil {
		t.Fatalf("history --all 未全量返回：data=%v meta=%v", data, meta)
	}

	code, output, errorOutput = runForTest("history", "--all", "--limit", "50", "alice")
	if code != 0 {
		t.Fatalf("history --all --limit 退出码=%d output=%v error=%v", code, output, errorOutput)
	}
	data = output["data"].(map[string]any)
	meta = output["meta"].(map[string]any)
	if data["count"].(float64) != 50 || meta["unbounded_by_limit"] != false || meta["result_limit"].(float64) != 50 {
		t.Fatalf("显式 limit 未生效：data=%v meta=%v", data, meta)
	}

	code, output, errorOutput = runForTest("search", "--all", "--chat", "alice", "消息")
	if code != 0 {
		t.Fatalf("search --all 退出码=%d output=%v error=%v", code, output, errorOutput)
	}
	data = output["data"].(map[string]any)
	if data["count"].(float64) != 1005 {
		t.Fatalf("search --all 未全量返回：%v", data)
	}
	searchStatus := data["search_backend_status"].(map[string]any)
	if searchStatus["message_coverage_status"] != "partial" || searchStatus["index_present"] != false {
		t.Fatalf("search fallback 没有明确领域覆盖状态：%v", searchStatus)
	}

	exportPath := filepath.Join(t.TempDir(), "all.jsonl")
	if err := os.WriteFile(exportPath, []byte("existing-output"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, output, errorOutput = runForTest("export", "--all", "--output", exportPath, "history", "alice")
	if code == 0 || errorOutput["error"].(map[string]any)["type"] != "output_exists" {
		t.Fatalf("export 默认覆盖了已有输出：code=%d error=%v", code, errorOutput)
	}
	preserved, err := os.ReadFile(exportPath)
	if err != nil || string(preserved) != "existing-output" {
		t.Fatalf("export 拒绝覆盖时改写了已有输出：payload=%q err=%v", preserved, err)
	}
	code, output, errorOutput = runForTest("export", "--all", "--force", "--output", exportPath, "history", "alice")
	if code != 0 {
		t.Fatalf("export --all 退出码=%d output=%v error=%v", code, output, errorOutput)
	}
	data = output["data"].(map[string]any)
	exported, readErr := os.ReadFile(exportPath)
	if data["count"].(float64) != 1005 || data["streamed"] != true || readErr != nil || bytes.Count(exported, []byte("\n")) != 1005 {
		t.Fatalf("export --all 未全量返回：%v", data)
	}

	code, output, errorOutput = runForTest("stats", "--all", "alice")
	if code != 0 {
		t.Fatalf("stats 退出码=%d output=%v error=%v", code, output, errorOutput)
	}
	stats := output["data"].(map[string]any)["stats"].(map[string]any)
	if stats["total_messages"].(float64) != 1005 {
		t.Fatalf("stats 未覆盖全部时间范围：%v", stats)
	}
}

func TestSetupRequiresAuthorization(t *testing.T) {
	t.Setenv("V_LOCAL_CLI_ACCOUNT_DIR", t.TempDir())
	code, _, _ := runForTest("setup")
	if code == 0 {
		t.Fatal("没有账号目录结构时 setup 不应成功")
	}
}

func TestSetupSnapshotOnlyAndContacts(t *testing.T) {
	account := t.TempDir()
	databaseDirectory := filepath.Join(account, "db_storage", "contact")
	if err := os.MkdirAll(databaseDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(databaseDirectory, "contact.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE contact(username TEXT, alias TEXT, remark TEXT, nick_name TEXT); INSERT INTO contact VALUES('alice','','阿丽','Alice')"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "keys.json")
	payload := `{"database_keys":{"*":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`
	if err := os.WriteFile(keyFile, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("V_LOCAL_CLI_ACCOUNT_DIR", account)
	t.Setenv("V_LOCAL_CLI_HOME", testHome(t))
	code, _, errorOutput := runForTest("setup", "--keys", keyFile, "--storage", "snapshot-only")
	if code == 0 || errorOutput["error"].(map[string]any)["type"] != "media_key_unverified" {
		t.Fatalf("setup 默认应要求完整图片密钥：code=%d error=%v", code, errorOutput)
	}
	code, output, errors := runForTest("setup", "--keys", keyFile, "--storage", "snapshot-only", "--database-only")
	if code != 0 {
		t.Fatalf("setup 退出码=%d output=%v error=%v", code, output, errors)
	}
	setupData := output["data"].(map[string]any)
	if setupData["status"] != "ready" || setupData["database_only"] != true || setupData["database_keys_persisted"] != false || setupData["image_keys_persisted"] != false {
		t.Fatalf("database-only setup 状态不明确：%v", setupData)
	}
	accountData := setupData["account"].(map[string]any)
	if accountData["version"].(float64) != 2 || accountData["updated_at"] == "" || accountData["generation_id"] == "" || accountData["snapshot_manifest_sha256"] == "" {
		t.Fatalf("setup 状态元数据未同步：%v", accountData)
	}
	code, output, errors = runForTest("contacts", "阿丽")
	if code != 0 {
		t.Fatalf("contacts 退出码=%d output=%v error=%v", code, output, errors)
	}
	data := output["data"].(map[string]any)
	if data["count"].(float64) != 1 {
		t.Fatalf("联系人数量异常：%v", data)
	}
}

func TestRefreshUsesStoredSecretsWithoutProcessAccess(t *testing.T) {
	account := t.TempDir()
	databaseDirectory := filepath.Join(account, "db_storage", "contact")
	if err := os.MkdirAll(databaseDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(databaseDirectory, "contact.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE contact(username TEXT, alias TEXT, remark TEXT, nick_name TEXT); INSERT INTO contact VALUES('alice','','阿丽','Alice')"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("V_LOCAL_CLI_ACCOUNT_DIR", account)
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	accountID := state.AccountID(account)
	snapshotPath := emptyTestGeneration(t, home, accountID)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: filepath.Base(account), AccountPath: account,
		SnapshotPath: snapshotPath, Storage: "keychain",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	loaded := false
	result, err := runRefreshWithSecrets(nil, func(requestedAccountID string) (provider.CandidateBundle, error) {
		loaded = true
		if requestedAccountID != accountID {
			t.Fatalf("refresh 读取了错误账号的凭据：%s", requestedAccountID)
		}
		return provider.CandidateBundle{DatabaseKeys: map[string]string{"*": strings.Repeat("a", 64)}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data := result.(map[string]any)
	if !loaded || data["credential_source"] != "saved_keychain" || data["process_access_performed"] != false || data["secrets_persisted"] != false || data["storage"] != "keychain" {
		t.Fatalf("refresh 安全边界异常：%v", data)
	}
	report := data["database"].(snapshot.Report)
	if report.Summary.Decrypted != 1 {
		t.Fatalf("refresh 未发布数据库快照：%+v", report)
	}
	code, output, errorOutput := runForTest("contacts", "阿丽")
	if code != 0 || output["data"].(map[string]any)["count"].(float64) != 1 {
		t.Fatalf("refresh 后联系人不可读：code=%d output=%v error=%v", code, output, errorOutput)
	}
}

func TestRefreshRejectsSnapshotOnlyWithoutReadingSecrets(t *testing.T) {
	account := t.TempDir()
	if err := os.MkdirAll(filepath.Join(account, "db_storage"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("V_LOCAL_CLI_ACCOUNT_DIR", account)
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	accountID := state.AccountID(account)
	snapshotPath := emptyTestGeneration(t, home, accountID)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: filepath.Base(account), AccountPath: account,
		SnapshotPath: snapshotPath, Storage: "snapshot-only",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	loaded := false
	_, err := runRefreshWithSecrets(nil, func(string) (provider.CandidateBundle, error) {
		loaded = true
		return provider.CandidateBundle{}, errors.New("不应读取")
	})
	var commandErr *commandError
	if loaded || !errors.As(err, &commandErr) || commandErr.typeName != "refresh_credentials_unavailable" {
		t.Fatalf("snapshot-only refresh 未安全拒绝：loaded=%v err=%v", loaded, err)
	}
}

func TestRefreshRejectsChangedAccountPathWithoutReadingSecrets(t *testing.T) {
	initializedAccount := t.TempDir()
	currentAccount := t.TempDir()
	if err := os.MkdirAll(filepath.Join(initializedAccount, "db_storage"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(currentAccount, "db_storage"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("V_LOCAL_CLI_ACCOUNT_DIR", currentAccount)
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	accountID := state.AccountID(initializedAccount)
	snapshotPath := emptyTestGeneration(t, home, accountID)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: filepath.Base(initializedAccount), AccountPath: initializedAccount,
		SnapshotPath: snapshotPath, Storage: "keychain",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	loaded := false
	_, err := runRefreshWithSecrets(nil, func(string) (provider.CandidateBundle, error) {
		loaded = true
		return provider.CandidateBundle{}, errors.New("不应读取")
	})
	var commandErr *commandError
	if loaded || !errors.As(err, &commandErr) || commandErr.typeName != "refresh_account_unavailable" {
		t.Fatalf("账号目录变化时 refresh 未安全拒绝：loaded=%v err=%v", loaded, err)
	}
}

func TestRefreshRejectsConcurrentSnapshotTransactionBeforeReadingSecrets(t *testing.T) {
	account := t.TempDir()
	if err := os.MkdirAll(filepath.Join(account, "db_storage"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("V_LOCAL_CLI_ACCOUNT_DIR", account)
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	accountID := state.AccountID(account)
	snapshotPath := emptyTestGeneration(t, home, accountID)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: filepath.Base(account), AccountPath: account,
		SnapshotPath: snapshotPath, Storage: "keychain",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	lock, err := state.AcquireAccountLock(accountID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	loaded := false
	_, err = runRefreshWithSecrets(nil, func(string) (provider.CandidateBundle, error) {
		loaded = true
		return provider.CandidateBundle{}, errors.New("不应读取")
	})
	var commandErr *commandError
	if loaded || !errors.As(err, &commandErr) || commandErr.typeName != "snapshot_busy" {
		t.Fatalf("并发 refresh 未在读取凭据前拒绝：loaded=%v err=%v", loaded, err)
	}
}

func TestAccountMetadataRedactsPathsByDefault(t *testing.T) {
	account := t.TempDir()
	if err := os.MkdirAll(filepath.Join(account, "db_storage"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("V_LOCAL_CLI_ACCOUNT_DIR", account)
	code, output, errorOutput := runForTest("accounts")
	if code != 0 {
		t.Fatalf("accounts 失败：%v", errorOutput)
	}
	accountData := output["data"].(map[string]any)["accounts"].([]any)[0].(map[string]any)
	if _, found := accountData["path"]; found {
		t.Fatalf("默认账号输出泄露路径：%v", accountData)
	}
	if _, found := accountData["db_dir"]; found {
		t.Fatalf("默认账号输出泄露数据库路径：%v", accountData)
	}
	code, output, errorOutput = runForTest("accounts", "--show-paths")
	if code != 0 {
		t.Fatalf("accounts --show-paths 失败：%v", errorOutput)
	}
	accountData = output["data"].(map[string]any)["accounts"].([]any)[0].(map[string]any)
	if accountData["path"] != filepath.Clean(account) {
		t.Fatalf("显式路径诊断未返回路径：%v", accountData)
	}
}

func TestForgetRequiresDryRunAndExplicitConfirmation(t *testing.T) {
	home := testHome(t)
	accountPath := t.TempDir()
	t.Setenv("V_LOCAL_CLI_HOME", home)
	accountID := state.AccountID(accountPath)
	snapshotPath := filepath.Join(home, "accounts", accountID, "snapshots", "generation")
	if err := os.MkdirAll(snapshotPath, 0o700); err != nil {
		t.Fatal(err)
	}
	value := state.AccountState{
		AccountID: accountID, AccountName: "test", AccountPath: accountPath,
		SnapshotPath: snapshotPath, GenerationID: "generation", Storage: "snapshot-only",
	}
	if err := state.Save(&value); err != nil {
		t.Fatal(err)
	}
	code, _, errorOutput := runForTest("forget", "--account", accountID)
	if code != 3 || errorOutput["error"].(map[string]any)["type"] != "confirmation_required" {
		t.Fatalf("forget 未要求确认：code=%d error=%v", code, errorOutput)
	}
	code, output, errorOutput := runForTest("forget", "--account", accountID, "--dry-run")
	if code != 0 || output["data"].(map[string]any)["status"] != "planned" {
		t.Fatalf("forget dry-run 异常：code=%d output=%v error=%v", code, output, errorOutput)
	}
	if _, err := os.Stat(snapshotPath); err != nil {
		t.Fatalf("dry-run 删除了快照：%v", err)
	}
	code, output, errorOutput = runForTest("forget", "--account", accountID, "--yes")
	if code != 0 || output["data"].(map[string]any)["status"] != "deleted" {
		t.Fatalf("forget 删除失败：code=%d output=%v error=%v", code, output, errorOutput)
	}
	if _, err := state.Load(accountID); !os.IsNotExist(err) {
		t.Fatalf("forget 后状态仍存在：%v", err)
	}
}

func TestDoctorBundleExcludesNamesPathsAndContent(t *testing.T) {
	home := testHome(t)
	accountPath := filepath.Join(t.TempDir(), "sensitive-account-name")
	if err := os.MkdirAll(filepath.Join(accountPath, "db_storage"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("V_LOCAL_CLI_HOME", home)
	t.Setenv("V_LOCAL_CLI_ACCOUNT_DIR", accountPath)
	accountID := state.AccountID(accountPath)
	snapshotPath := filepath.Join(home, "accounts", accountID, "snapshots", "generation")
	if err := os.MkdirAll(snapshotPath, 0o700); err != nil {
		t.Fatal(err)
	}
	value := state.AccountState{
		AccountID: accountID, AccountName: "sensitive-account-name", AccountPath: accountPath,
		SnapshotPath: snapshotPath, GenerationID: "generation", Storage: "snapshot-only",
	}
	if err := state.Save(&value); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "doctor.json")
	code, output, errorOutput := runForTest("doctor", "--bundle", outputPath)
	if code != 0 {
		t.Fatalf("doctor --bundle 失败：output=%v error=%v", output, errorOutput)
	}
	if _, found := output["data"].(map[string]any)["diagnostic_bundle"].(map[string]any)["output"]; found {
		t.Fatalf("doctor --bundle 默认输出泄露路径：%v", output)
	}
	payload, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"sensitive-account-name", accountPath, snapshotPath, "account_path", "snapshot_path"} {
		if bytes.Contains(payload, []byte(forbidden)) {
			t.Fatalf("脱敏诊断包含敏感字段 %q：%s", forbidden, payload)
		}
	}
	var bundle map[string]any
	if err := json.Unmarshal(payload, &bundle); err != nil {
		t.Fatal(err)
	}
	privacy := bundle["privacy"].(map[string]any)
	if privacy["contains_paths"] != false || privacy["contains_content"] != false || privacy["contains_secrets"] != false {
		t.Fatalf("诊断隐私声明异常：%v", privacy)
	}
}

func TestSnapshotMetadataAndFreshArgumentRouting(t *testing.T) {
	value := state.AccountState{
		GenerationID: "generation", SnapshotManifestSHA256: "digest",
		SnapshotCreatedAt: time.Now().Add(-5 * time.Second).UTC().Format(time.RFC3339),
	}
	output := withGeneration(commandOutput{data: map[string]any{}}, value)
	if output.meta["snapshot_created_at"] == "" {
		t.Fatal("查询响应缺少 snapshot_created_at")
	}
	age, ok := output.meta["snapshot_age_seconds"].(int64)
	if !ok || age < 4 || age > 10 {
		t.Fatalf("snapshot_age_seconds 异常：%v", output.meta)
	}
	filtered, fresh, err := prepareFreshQuery([]string{"history", "--fresh=false", "chat"})
	if err != nil || fresh || len(filtered) != 2 || filtered[1] != "chat" {
		t.Fatalf("--fresh=false 路由错误：filtered=%v fresh=%v err=%v", filtered, fresh, err)
	}
	if _, _, err := prepareFreshQuery([]string{"accounts", "--fresh"}); err == nil {
		t.Fatal("不读取快照的命令不应接受 --fresh")
	}
	code, _, failure := runForTest("unknown-for-snapshot-meta")
	if code == 0 {
		t.Fatal("未知命令不应成功")
	}
	failureMeta, ok := failure["meta"].(map[string]any)
	if !ok {
		t.Fatalf("失败响应缺少 meta：%v", failure)
	}
	if _, found := failureMeta["snapshot_created_at"]; !found {
		t.Fatalf("失败响应缺少 snapshot_created_at：%v", failureMeta)
	}
	if _, found := failureMeta["snapshot_age_seconds"]; !found {
		t.Fatalf("失败响应缺少 snapshot_age_seconds：%v", failureMeta)
	}
}
