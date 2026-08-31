package app

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

var documentedFlagPattern = regexp.MustCompile(`--([a-z][a-z0-9-]+)`)

func schemaCommands(t *testing.T) map[string]any {
	t.Helper()
	value, err := runSchema(nil)
	if err != nil {
		t.Fatal(err)
	}
	return value.(map[string]any)["commands"].(map[string]any)
}

func usageFlags(usage string) map[string]bool {
	flags := make(map[string]bool)
	for _, match := range documentedFlagPattern.FindAllStringSubmatch(usage, -1) {
		flags[match[1]] = true
	}
	return flags
}

func implementationFlagSets(t *testing.T, root string) map[string]map[string]bool {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "internal", "app", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]map[string]bool)
	constructors := map[string]bool{
		"Bool": true, "Duration": true, "Float64": true, "Int": true, "Int64": true,
		"String": true, "Uint": true, "Uint64": true, "Var": true,
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			flagSets := make(map[string]string)
			ast.Inspect(function.Body, func(node ast.Node) bool {
				assignment, ok := node.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for index, expression := range assignment.Rhs {
					call, ok := expression.(*ast.CallExpr)
					if !ok || len(call.Args) == 0 || index >= len(assignment.Lhs) {
						continue
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						continue
					}
					owner, ownerOK := selector.X.(*ast.Ident)
					variable, variableOK := assignment.Lhs[index].(*ast.Ident)
					literal, literalOK := call.Args[0].(*ast.BasicLit)
					if !ownerOK || !variableOK || !literalOK || owner.Name != "flag" || selector.Sel.Name != "NewFlagSet" {
						continue
					}
					command, unquoteErr := strconv.Unquote(literal.Value)
					if unquoteErr != nil {
						t.Fatal(unquoteErr)
					}
					flagSets[variable.Name] = command
					if result[command] == nil {
						result[command] = make(map[string]bool)
					}
				}
				return true
			})
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				owner, ownerOK := selector.X.(*ast.Ident)
				if !ownerOK || !constructors[selector.Sel.Name] {
					return true
				}
				command, found := flagSets[owner.Name]
				literal, literalOK := call.Args[0].(*ast.BasicLit)
				if !found || !literalOK {
					return true
				}
				name, unquoteErr := strconv.Unquote(literal.Value)
				if unquoteErr != nil {
					t.Fatal(unquoteErr)
				}
				result[command][name] = true
				return true
			})
		}
	}
	return result
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位 Skill 契约测试")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestSkillContractMatchesCommandSchema(t *testing.T) {
	root := repositoryRoot(t)
	payload, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	skill := string(payload)
	if !strings.HasPrefix(skill, "---\nname: v-local-cli\ndescription:") {
		t.Fatal("SKILL.md frontmatter 必须只以 v-local-cli 名称和 description 开始")
	}
	commands := schemaCommands(t)
	var help bytes.Buffer
	writeHelp(&help)
	for name, rawDefinition := range commands {
		needle := "v-local-cli " + name
		if !strings.Contains(skill, needle) {
			t.Errorf("SKILL.md 缺少命令：%s", name)
		}
		usage := rawDefinition.(map[string]any)["usage"].(string)
		if !strings.Contains(help.String(), "  "+usage+"\n") {
			t.Errorf("帮助文本与 schema 用法不一致：%s", name)
		}
	}
	commandPattern := regexp.MustCompile(`\bv-local-cli ([a-z][a-z0-9-]*)`)
	for _, match := range commandPattern.FindAllStringSubmatch(skill, -1) {
		name := match[1]
		if _, ok := commands[name]; !ok {
			t.Errorf("SKILL.md 引用了 schema 中不存在的命令：%s", name)
		}
	}
	metadata, err := os.ReadFile(filepath.Join(root, "agents", "openai.yaml"))
	if err != nil || !bytes.Contains(metadata, []byte("v-local-cli")) {
		t.Fatal("agents/openai.yaml 缺失或与 v-local-cli 不匹配")
	}
}

func TestChatImageAgentRecoveryContract(t *testing.T) {
	definition := schemaCommands(t)["export-chat-image"].(map[string]any)
	decoderContract := definition["wxgf_decoder_diagnostics_contract"].(map[string]any)
	if decoderContract["failure_field"] != "decoder_diagnostics" || decoderContract["higher_quality_field"] != "higher_quality_decoder_diagnostics" {
		t.Fatalf("WXGF 解码器诊断契约发生漂移：%v", decoderContract)
	}
	assertWXGFDecoderDiagnostics(t, decoderContract["value"])
	recovery := definition["agent_recovery"].(map[string]any)
	if recovery["requires_user_confirmation"] != true || recovery["refresh_command"] != "refresh --require-media" ||
		recovery["retry_evidence_binding"] != "same_image_evidence_id" || recovery["maximum_automatic_retries"] != 1 ||
		recovery["network"] != false || recovery["still_missing_outcome"] != "stop_and_report_remote_may_be_expired_or_unavailable" {
		t.Fatalf("聊天图片 Agent 恢复契约发生漂移：%v", recovery)
	}
	if definition["remote_descriptor_expiry"] != "unknown_without_verified_request; may_already_be_expired" ||
		definition["remote_protocol_qualification"] != "not_qualified" || definition["remote_synthetic_harness_status"] != "crypto_binding_passed" ||
		definition["remote_real_endpoint_enabled"] != false ||
		definition["remote_synthetic_endpoint_scope"] != "literal_loopback_tls_only" ||
		definition["remote_qualification_binding"] != "plaintext_md5_or_size_plus_dimensions" ||
		definition["remote_descriptor_secrets_output"] != false || definition["remote_acquisition_implemented"] != false || definition["network"] != false {
		t.Fatalf("聊天 CDN 时效或联网边界发生漂移：%v", definition)
	}
	parseStatuses := definition["remote_descriptor_parse_statuses"].([]string)
	for _, expected := range []string{"parsed_unverified_protocol", "parsed_partial_unverified_protocol", "present_incomplete", "present_invalid", "not_applicable", "not_evaluated"} {
		found := false
		for _, actual := range parseStatuses {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("聊天 CDN 描述符解析状态缺失：%s", expected)
		}
	}
	if definition["quality_claim_scope"] != "wechat_cache_variant_only" ||
		definition["source_original_dimensions_known"] != false ||
		definition["source_original_quality_status"] != "unknown" ||
		definition["dimensions_role"] != "decoded_output_observation_not_quality_gate" {
		t.Fatalf("聊天图片质量声明超出可用证据：%v", definition)
	}
	recoverDefinition := schemaCommands(t)["recover-chat-image"].(map[string]any)
	if recoverDefinition["network_default"] != false || recoverDefinition["network_authorization"] != "structured_one_time_challenge" ||
		recoverDefinition["account_lock"] != true || recoverDefinition["account_lock_scope"] != "entire_offline_preflight_or_authorized_attempt" ||
		recoverDefinition["authorization_scope"] != "single_account_message_image_candidate_attempt" ||
		recoverDefinition["authorization_consumed_before_network"] != true || recoverDefinition["network_attempts_per_authorization"] != 1 ||
		recoverDefinition["automatic_network_retries"] != 0 || recoverDefinition["wechat_ui_automation"] != false ||
		recoverDefinition["direct_url_source"] != "current_snapshot_descriptor_only" || recoverDefinition["constructed_url_from_opaque_parameter"] != false ||
		recoverDefinition["allowed_destination"] != "novac2c.cdn.weixin.qq.com" || recoverDefinition["https_required"] != true ||
		recoverDefinition["redirects"] != false || recoverDefinition["ambient_proxy"] != false || recoverDefinition["external_dns_fallback"] != false ||
		recoverDefinition["url_stored_in_consent"] != false || recoverDefinition["descriptor_secrets_output"] != false ||
		recoverDefinition["lower_quality_fallback"] != false || recoverDefinition["source_original_quality_status"] != "unknown" {
		t.Fatalf("聊天图片结构化联网恢复契约发生漂移：%v", recoverDefinition)
	}

	root := repositoryRoot(t)
	skill, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"用户确认后由 Agent 自动运行 `refresh --require-media`",
		"仍使用同一个 `image_evidence_id`",
		"最多自动重试一次",
		"远端描述符可能已经过期或资源不可用",
		"不要循环催促用户",
		"synthetic_crypto_binding_harness_only",
		"只有当前快照直接携带",
		"challenge 的作用域固定为 `single_account_message_image_candidate_attempt`",
		"先原子消费后联网",
		"授权聊天 CDN 请求不授权操作微信 UI",
		"不做 DNSPod 回退",
		"source_original_quality_status=unknown",
		"run_recover_chat_image_offline_then_request_structured_consent",
	} {
		if !bytes.Contains(skill, []byte(expected)) {
			t.Errorf("SKILL.md 缺少聊天图片恢复边界：%s", expected)
		}
	}
	script, err := os.ReadFile(filepath.Join(root, "scripts", "accept-windows-chat-image-recovery.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	scriptText := string(script)
	for _, expected := range []string{
		"[ValidateSet('Prompt', 'Skip')]",
		"[string]$LowerTierMissingEvidenceId",
		"[string]$WxgfCandidateEvidenceId",
		"[string]$ExpiryUnknownDescriptorEvidenceId",
		"$ConfirmationToken = \"OPENED-$($Definition.Name)\"",
		"@('refresh', '--account', $AccountName, '--require-media')",
		"maximum_automatic_retries = 1",
		"automatic_retry_attempts = 1",
		"stop_after_single_refresh_remote_may_be_expired_or_unavailable",
		"contains_evidence_ids = $false",
		"contains_urls_tokens_or_keys = $false",
		"generation_changed_by_recovery",
		"recovery_database_coverage_regressed",
		"recovery_preflight_generation_mismatch",
		"recovery_did_not_publish_new_generation",
		"powershell_7_required",
		"if ($ShowPaths)",
		"fixed_dimension_quality_gate = $false",
		"-Width 320 -Height 240",
		"dimensions_role = 'decoded_output_observation_not_quality_gate'",
		"remote_descriptor_parse_status = $RemoteParseStatus",
		"pass_expected_decoder_unavailable",
		"binary_presence_status",
		"public_cli_integration_status",
		"production_qualification_status",
		"self_test_missing_wxgf_decoder_diagnostics_not_rejected",
		"quality_claim_scope = 'wechat_cache_variant_only'",
		"source_original_quality_status = 'unknown'",
		"run_recover_chat_image_offline_then_request_structured_consent",
	} {
		if !strings.Contains(scriptText, expected) {
			t.Errorf("Windows 图片验收脚本缺少恢复边界：%s", expected)
		}
	}
	if strings.Contains(scriptText, "--allow-network") {
		t.Fatal("Windows 图片验收脚本不得启用聊天图片联网")
	}
	consentScript, err := os.ReadFile(filepath.Join(root, "scripts", "test-chat-image-recovery-consent.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	consentText := string(consentScript)
	for _, expected := range []string{
		"structured_one_time_challenge", "single_account_message_image_candidate_attempt",
		"authorization_consumed_before_network", "automatic_network_retries", "current_snapshot_descriptor_only",
		"constructed_url_from_opaque_parameter", "novac2c.cdn.weixin.qq.com", "external_dns_fallback",
		"url_stored_in_consent", "descriptor_secrets_output", "lower_quality_fallback",
		"source_original_quality_status", "observed_at", "retrieved_at", "authorization_expires_at",
	} {
		if !strings.Contains(consentText, expected) {
			t.Errorf("聊天图片联网授权自检缺少边界：%s", expected)
		}
	}
	for _, forbidden := range []string{"Invoke-WebRequest", "Invoke-RestMethod", "Start-BitsTransfer"} {
		if strings.Contains(consentText, forbidden) {
			t.Errorf("聊天图片联网授权 schema 自检不得联网：%s", forbidden)
		}
	}
	for _, forbidden := range []string{"MinImageLongEdge", "MinImageShortEdge", "decodable_high_dimensions_failed", "recovered_high_dimensions_failed"} {
		if strings.Contains(scriptText, forbidden) {
			t.Errorf("Windows 图片验收脚本不得用固定边长判定 high 层级：%s", forbidden)
		}
	}
	for _, obsolete := range []string{"ThumbnailOnlyEvidenceId", "thumbnail_only", "WxgfHighEvidenceId", "wxgf_high", "StaleDescriptorEvidenceId", "stale_descriptor"} {
		if strings.Contains(scriptText, obsolete) {
			t.Errorf("Windows 图片验收脚本保留了会扩大或误述夹具语义的旧名称：%s", obsolete)
		}
	}
	if strings.Count(scriptText, "@('refresh', '--account', $AccountName, '--require-media')") != 1 {
		t.Fatal("Windows 图片验收脚本必须只保留一个受控 refresh 调用点")
	}
	staticEvidenceScript, err := os.ReadFile(filepath.Join(root, "scripts", "inspect-windows-chat-cdn-static-evidence.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	staticEvidenceText := string(staticEvidenceScript)
	for _, expected := range []string{
		"v-local-cli/windows-chat-cdn-static-evidence/v1",
		"current_client_static_stack_present_unbound",
		"sessionized_c2c_static_stack = $SessionizedC2CStack",
		"direct_ilink_https_markers = $DirectIlinkHttpsMarkers",
		"main_to_ilink_wrapper_static_reference = $MainToIlinkWrapperReference",
		"weixin_main_public_c2c_download_entry",
		"not_observed_in_current_client_binaries",
		"delay_import_observed_unbound",
		"not_observed_in_current_weixin_export_table",
		"static_reference_not_observed_in_current_weixin_binary",
		"System.Reflection.PortableExecutable.PEReader",
		"descriptor_to_runtime_request_binding = 'not_observed'",
		"runtime_protocol_selection = 'not_observed'",
		"endpoint_qualification = 'not_qualified'",
		"network_access_performed = $false",
		"process_memory_access_performed = $false",
		"account_data_access_performed = $false",
		"secrets_output = $false",
		"binary_changed_during_scan",
	} {
		if !strings.Contains(staticEvidenceText, expected) {
			t.Errorf("Windows 聊天 CDN 静态证据脚本缺少边界：%s", expected)
		}
	}
	for _, forbidden := range []string{"Invoke-WebRequest", "Invoke-RestMethod", "Start-BitsTransfer", "Get-DnsClientCache", "Get-NetTCPConnection", "netstat", "OpenProcess", "ReadProcessMemory"} {
		if strings.Contains(staticEvidenceText, forbidden) {
			t.Errorf("Windows 聊天 CDN 静态证据脚本不得联网或读取进程内存：%s", forbidden)
		}
	}
	xlogEvidenceScript, err := os.ReadFile(filepath.Join(root, "scripts", "inspect-windows-chat-cdn-xlog-structure.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	xlogEvidenceText := string(xlogEvidenceScript)
	for _, expected := range []string{
		"v-local-cli/windows-chat-cdn-xlog-structure-evidence/v1",
		"encrypted_mars_xlog_private_key_required",
		"no_crypt_mars_xlog_decoder_candidate",
		"mixed_mars_xlog_requires_separate_review",
		"log_path_outside_xwechat_log_root",
		"log_read_access_denied",
		"log_open_failed",
		"log_changed_during_scan",
		"payload_decoding_performed = $false",
		"plaintext_event_binding = 'not_observed'",
		"descriptor_to_runtime_request_binding = 'not_observed'",
		"endpoint_qualification = 'not_qualified'",
		"embedded_key_material_output = $false",
		"secrets_output = $false",
	} {
		if !strings.Contains(xlogEvidenceText, expected) {
			t.Errorf("Windows 聊天 CDN xlog 结构检查器缺少边界：%s", expected)
		}
	}
	for _, forbidden := range []string{"Invoke-WebRequest", "Invoke-RestMethod", "Start-BitsTransfer", "Get-NetTCPConnection", "pktmon", "wpr.exe", "netsh", "OpenProcess", "ReadProcessMemory", "decode_mars_crypt_log_file.py"} {
		if strings.Contains(xlogEvidenceText, forbidden) {
			t.Errorf("Windows 聊天 CDN xlog 结构检查器不得联网、读进程内存或尝试解密：%s", forbidden)
		}
	}
	auditWorkflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "audit-gates.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"inspect-windows-chat-cdn-xlog-structure.ps1 -SelfTest",
		"test-chat-image-recovery-consent.ps1 -SelfTest",
		"test-chat-image-recovery-consent.ps1 -Cli",
	} {
		if !bytes.Contains(auditWorkflow, []byte(expected)) {
			t.Errorf("Windows audit gate 未运行聊天图片自检：%s", expected)
		}
	}
	acceptance, err := os.ReadFile(filepath.Join(root, "references", "windows-amd64-local-acceptance.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(acceptance, []byte("../scripts/accept-windows-chat-image-recovery.ps1")) ||
		!bytes.Contains(acceptance, []byte("../scripts/test-chat-image-recovery-consent.ps1")) ||
		!bytes.Contains(acceptance, []byte("../scripts/inspect-windows-chat-cdn-static-evidence.ps1")) ||
		!bytes.Contains(acceptance, []byte("../scripts/inspect-windows-chat-cdn-xlog-structure.ps1")) ||
		!bytes.Contains(acceptance, []byte("current_client_static_stack_present_unbound")) ||
		!bytes.Contains(acceptance, []byte("direct_ilink_https_markers=not_observed_in_current_client_binaries")) ||
		!bytes.Contains(acceptance, []byte("main_to_ilink_wrapper_static_reference=delay_import_observed_unbound")) ||
		!bytes.Contains(acceptance, []byte("仍不增加聊天图片长期 `--allow-network`")) ||
		!bytes.Contains(acceptance, []byte("encrypted_mars_xlog_private_key_required")) ||
		!bytes.Contains(acceptance, []byte("退出码 `0`")) || !bytes.Contains(acceptance, []byte("`1` 表示")) ||
		!bytes.Contains(acceptance, []byte("`2` 表示")) ||
		!bytes.Contains(acceptance, []byte("不要设置固定像素门槛")) ||
		!bytes.Contains(acceptance, []byte("WXGF 若返回预期的 `chat_image_unavailable/decoder_unavailable`")) ||
		!bytes.Contains(acceptance, []byte("binary_presence_status=not_evaluated")) ||
		!bytes.Contains(acceptance, []byte("最终 v1 宿主绑定结构资格复核")) ||
		!bytes.Contains(acceptance, []byte("每次询问前用当前 generation 重新预检")) {
		t.Fatal("Windows 真机验收文档没有公开脚本入口或退出状态")
	}
	mediaReference, err := os.ReadFile(filepath.Join(root, "references", "media-decrypt.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"不能把字段值本身当作可直接请求的 HTTPS URL",
		"不能据此断言桌面十六进制描述符可复用",
		"synthetic_crypto_binding_harness_only",
		"仅作历史线索",
		"不能证明 2026 年当前 Windows 桌面端",
		"Weixin 4.1.12.55",
		"current_client_static_stack_present_unbound",
		"descriptor_to_runtime_request_binding=not_observed",
		"encrypted_mars_xlog_private_key_required",
		"payload_decoding_performed=false",
		"binary_presence_status=not_evaluated",
		"public_cli_integration_status=not_wired",
		"CdnCore::start_c2c_download -> CdnCore::_startDownloadMedia -> TaskFactory::CreateC2CImageDownloadTask",
		"主模块确实 delay-import `ilink_wrapper.dll`",
		"快照自带 full URL 可做单次、响应后验真的恢复",
		"不透明桌面参数仍不得直连",
		"描述符年龄、字段存在、HTTP 状态、LongEdge、ShortEdge、文件大小、缓存层级和像素尺寸都不能单独判定时效或原始质量",
		"任何非 loopback 端点都会在请求前拒绝",
		"重新取得单次授权",
		"`429` 只表示限流",
		"openclaw-weixin",
		"`full_url`",
	} {
		if !bytes.Contains(mediaReference, []byte(expected)) {
			t.Errorf("聊天 CDN 资格门禁文档缺少边界：%s", expected)
		}
	}
}

func TestWXGFVisualReviewQualificationContract(t *testing.T) {
	root := repositoryRoot(t)
	visualScript, err := os.ReadFile(filepath.Join(root, "scripts", "accept-windows-wxgf-visual-equivalence.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	visualText := string(visualScript)
	for _, expected := range []string{
		"$HelperProtocol = 'v-local-cli/wxgf-visual-review-helper/v1'",
		"$RecordProtocol = 'v-local-cli/wxgf-visual-review-record/v1'",
		"$ReportProtocol = 'v-local-cli/windows-wxgf-visual-equivalence-evidence/v1'",
		"$ProviderProtocol = 'v-local-cli-image-decoder/1'",
		"[ValidateSet('Prompt', 'Skip')]",
		"[string]$BrowserDisplayRoot",
		"$HelperAction = if ($ReviewMode -ceq 'Skip') { 'inspect' } else { 'prepare' }",
		"$Expected = \"CONFIRM-CONTENT-ORIENTATION-CROP-COLOR-$Challenge\"",
		"Start-Process -FilePath $OpenPath",
		"[System.IO.FileMode]::CreateNew",
		"Set-BrowserDisplayDirectoryAcl",
		"Get-BrowserDisplayReadSids",
		"S-1-15-2-1",
		"S-1-15-2-2",
		"browser_display_root_acl_public_reader",
		"$Sid.StartsWith('S-1-5-21-'",
		"explicit_local_root_readers_downgraded_to_read_only",
		"browser_display_root_overlaps_private_root",
		"browser_display_root_not_local_fixed_disk",
		"browser_display_copy_changed",
		"CONFIRM-BROWSER-DISPLAY-$InteractiveChallenge",
		"browser_display_interactive_checked",
		"temporary_browser_display_artifacts_removed",
		"browser_display_path_included = $false",
		"browser_cache_erasure_proven = $false",
		"fixed_dimension_quality_gate = $false",
		"source_producer_version_status = $SourceProducerVersionStatus",
		"provider_binary_trust_status = $ProviderBinaryTrustStatus",
		"host_staged_manifest_bound_provider_and_decoder_sha256",
		"provider_identity_manifest_protocol = $ProviderIdentityManifestProtocol",
		"provider_identity_manifest_sha256 = $ProviderIdentityManifestSHA256",
		"provider_sha256 = $ProviderSHA256",
		"provider_signature_status = $ProviderSignatureStatus",
		"decoder_signature_status = $DecoderSignatureStatus",
		"decoder_distribution_license_status = $DecoderDistributionLicenseStatus",
		"production_ready = $false",
		"temporary_artifact_cleanup_failed",
	} {
		if !strings.Contains(visualText, expected) {
			t.Errorf("WXGF 人工复审脚本缺少边界：%s", expected)
		}
	}

	manifestScript, err := os.ReadFile(filepath.Join(root, "scripts", "new-windows-wxgf-provider-identity-manifest.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	manifestText := string(manifestScript)
	for _, expected := range []string{
		"v-local-cli/wxgf-provider-identity-manifest/v1",
		"[System.IO.FileMode]::CreateNew",
		"Get-StableSHA256",
		"Assert-NoReparsePoint",
		"StartsWith('//')",
		"provider_decoder_not_adjacent",
		"provider_decoder_same_file",
		"decoder_file_name_invalid",
		"self_test_manifest_contains_trust_claim",
		"identity_only = $true",
		"proves_provenance = $false",
		"qualifies_signatures = $false",
		"qualifies_distribution_license = $false",
		"network = $false",
	} {
		if !strings.Contains(manifestText, expected) {
			t.Errorf("WXGF provider 身份清单脚本缺少边界：%s", expected)
		}
	}
	for _, forbidden := range []string{"Invoke-WebRequest", "Invoke-RestMethod", "Start-BitsTransfer", "Copy-Item"} {
		if strings.Contains(manifestText, forbidden) {
			t.Errorf("WXGF provider 身份清单脚本包含越界能力：%s", forbidden)
		}
	}
	auditWorkflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "audit-gates.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"new-windows-wxgf-provider-identity-manifest.ps1 -SelfTest",
		"new-windows-wxgf-visual-review-session.ps1 -SelfTest",
		"accept-windows-wxgf-visual-equivalence.ps1 -SelfTest",
	} {
		if !bytes.Contains(auditWorkflow, []byte(expected)) {
			t.Errorf("Windows audit gate 未运行 WXGF 自检：%s", expected)
		}
	}
	for _, forbidden := range []string{
		"--allow-network", "Invoke-WebRequest", "Invoke-RestMethod", "Start-BitsTransfer", "Copy-Item",
		"MinImageLongEdge", "MinImageShortEdge", "source_original_quality_known = $true",
	} {
		if strings.Contains(visualText, forbidden) {
			t.Errorf("WXGF 人工复审脚本包含越界能力或固定尺寸门槛：%s", forbidden)
		}
	}

	sessionScript, err := os.ReadFile(filepath.Join(root, "scripts", "new-windows-wxgf-visual-review-session.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	sessionText := string(sessionScript)
	for _, expected := range []string{
		"V_LOCAL_TEST_WXGF_REVIEW_ROOT",
		"SetAccessRuleProtection($true, $false)",
		"Assert-PrivateDirectoryAcl $RootBase",
		"temporary_images_present = $false",
		"reads_wechat_data = $false",
		"opens_wechat_ui = $false",
	} {
		if !strings.Contains(sessionText, expected) {
			t.Errorf("WXGF 私有复审目录脚本缺少边界：%s", expected)
		}
	}

	reference, err := os.ReadFile(filepath.Join(root, "references", "wxgf-decoder-qualification.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"installed_package_at_review_not_source_provenance",
		"hardlink_cache_filename_variant_not_source_quality",
		"provider_binary_trust_status=unverified",
		"host_staged_manifest_bound_provider_and_decoder_sha256",
		"pre_binding_records_excluded",
		"provider_signature_status=not_qualified",
		"decoder_signature_status=not_qualified",
		"decoder_distribution_license_status=not_qualified",
		"当前最终 v1 矩阵尚未用真实样本评估",
		"项目本身只提供源码",
		"不启用 GPL/nonfree 组件",
		"browser_cache_erasure_proven=false",
		"`production_ready=false`、`fixed_dimension_quality_gate=false`",
		"本流程不操作微信 UI、不请求 CDN",
		"两者仍都只命中 `medium`，没有观察到 `high` 本地缓存",
		"`inconclusive/skipped`",
		"`sample_review.status=confirmed`",
		"`samples_confirmed=2`",
		"`distinct_wxgf_samples=2`",
		"`high=0`、`medium=2`",
		"`-BrowserDisplayRoot`",
		"`browser_display_path_included=false`",
	} {
		if !bytes.Contains(reference, []byte(expected)) {
			t.Errorf("WXGF 人工复审文档缺少证据边界：%s", expected)
		}
	}
	acceptance, err := os.ReadFile(filepath.Join(root, "references", "windows-amd64-local-acceptance.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(acceptance, []byte("wxgf-decoder-qualification.md#人工视觉等价复审")) ||
		!bytes.Contains(acceptance, []byte("只查看解码图但跳过参考图")) ||
		!bytes.Contains(acceptance, []byte("pre_binding_records_excluded")) {
		t.Fatal("Windows 真机验收没有链接 WXGF 人工复审及其跳过边界")
	}
}

func TestImplementedFlagsMatchCommandSchema(t *testing.T) {
	root := repositoryRoot(t)
	commands := schemaCommands(t)
	implemented := implementationFlagSets(t, root)
	for command, flags := range implemented {
		if strings.HasPrefix(command, "__") {
			continue
		}
		rawDefinition, found := commands[command]
		if !found {
			t.Errorf("实现包含 schema 未声明的命令：%s", command)
			continue
		}
		definition := rawDefinition.(map[string]any)
		declared := usageFlags(definition["usage"].(string))
		for name := range flags {
			if !declared[name] {
				t.Errorf("%s 实现了 schema 未声明的选项 --%s", command, name)
			}
		}
		for name := range declared {
			if name == "fresh" {
				if definition["fresh_snapshot"] != true {
					t.Errorf("%s 声明 --fresh 但未声明 fresh_snapshot=true", command)
				}
				continue
			}
			if !flags[name] {
				t.Errorf("%s schema 声明了实现中不存在的选项 --%s", command, name)
			}
		}
	}
}

func TestInternalFlagSetsStayExplicitAndOutsidePublicSchema(t *testing.T) {
	root := repositoryRoot(t)
	commands := schemaCommands(t)
	implemented := implementationFlagSets(t, root)
	expected := map[string]map[string]bool{
		"__shadow-qualify":         {"account": true, "database-only": true},
		"__shadow-synthetic-owner": {"confirm": true},
	}
	for command, flags := range implemented {
		if !strings.HasPrefix(command, "__") {
			continue
		}
		want, found := expected[command]
		if !found {
			t.Errorf("实现包含未审查的内部命令：%s", command)
			continue
		}
		if !reflect.DeepEqual(flags, want) {
			t.Errorf("内部命令 %s 的选项漂移：got=%v want=%v", command, flags, want)
		}
		if _, exposed := commands[command]; exposed {
			t.Errorf("内部命令 %s 泄漏到公开 command schema", command)
		}
		delete(expected, command)
	}
	for command := range expected {
		t.Errorf("受控内部命令未实现：%s", command)
	}
}

func TestDocumentationCommandOptionsMatchSchema(t *testing.T) {
	root := repositoryRoot(t)
	commands := schemaCommands(t)
	paths := []string{
		filepath.Join(root, "README.md"), filepath.Join(root, "SECURITY.md"), filepath.Join(root, "SKILL.md"),
	}
	references, err := filepath.Glob(filepath.Join(root, "references", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, references...)
	commandPattern := regexp.MustCompile(`\bv-local-cli[ \t]+([a-z][a-z0-9-]*)([^\x60\r\n]*)`)
	for _, path := range paths {
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, match := range commandPattern.FindAllStringSubmatch(string(payload), -1) {
			command := match[1]
			rawDefinition, found := commands[command]
			if !found {
				t.Errorf("%s 引用了 schema 中不存在的命令：%s", filepath.Base(path), command)
				continue
			}
			valid := usageFlags(rawDefinition.(map[string]any)["usage"].(string))
			for _, option := range documentedFlagPattern.FindAllStringSubmatch(match[2], -1) {
				if !valid[option[1]] {
					t.Errorf("%s 为 %s 记录了 schema 中不存在的选项 --%s", filepath.Base(path), command, option[1])
				}
			}
		}
	}
}

func TestExternalCommandErrorsHaveRecoveryDocumentation(t *testing.T) {
	root := repositoryRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, "internal", "app", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var source strings.Builder
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		source.Write(payload)
	}
	var documentation strings.Builder
	for _, path := range []string{filepath.Join(root, "SKILL.md"), filepath.Join(root, "references", "troubleshooting.md")} {
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		documentation.Write(payload)
	}
	errorPattern := regexp.MustCompile(`typeName:\s*"([a-z][a-z0-9_]+)"`)
	externalErrors := make(map[string]bool)
	for _, match := range errorPattern.FindAllStringSubmatch(source.String(), -1) {
		externalErrors[match[1]] = true
	}
	publicMappers := map[string]bool{"momentMediaCommandError": true, "officialArticleCommandError": true}
	identifierPattern := regexp.MustCompile(`^[a-z][a-z0-9_]+$`)
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil || !publicMappers[function.Name.Name] {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, unquoteErr := strconv.Unquote(literal.Value)
				if unquoteErr != nil {
					t.Fatal(unquoteErr)
				}
				if identifierPattern.MatchString(value) {
					externalErrors[value] = true
				}
				return true
			})
		}
	}
	for name := range externalErrors {
		if !strings.Contains(documentation.String(), "`"+name+"`") {
			t.Errorf("外部错误缺少恢复文档：%s", name)
		}
	}
}

func TestPackageVersionMatchesRuntimeVersion(t *testing.T) {
	root := repositoryRoot(t)
	payload, err := os.ReadFile(filepath.Join(root, "npm", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var packageMetadata struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(payload, &packageMetadata); err != nil {
		t.Fatal(err)
	}
	if packageMetadata.Version != Version {
		t.Fatalf("npm 版本 %s 与运行时版本 %s 不一致", packageMetadata.Version, Version)
	}
	for _, path := range []string{filepath.Join(root, "README.md"), filepath.Join(root, "SECURITY.md")} {
		documentation, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.Contains(string(documentation), "`"+Version+"`") {
			t.Errorf("%s 没有声明当前运行时版本 %s", filepath.Base(path), Version)
		}
	}
}

func TestDocumentationRelativeLinksResolve(t *testing.T) {
	root := repositoryRoot(t)
	paths := []string{filepath.Join(root, "README.md"), filepath.Join(root, "SECURITY.md"), filepath.Join(root, "SKILL.md")}
	references, err := filepath.Glob(filepath.Join(root, "references", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, references...)
	linkPattern := regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	for _, path := range paths {
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, match := range linkPattern.FindAllStringSubmatch(string(payload), -1) {
			target := strings.TrimSpace(match[1])
			if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "http://") ||
				strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			target = strings.SplitN(target, "#", 2)[0]
			if _, statErr := os.Stat(filepath.Join(filepath.Dir(path), filepath.FromSlash(target))); statErr != nil {
				t.Errorf("%s 包含无效相对链接 %s：%v", filepath.Base(path), target, statErr)
			}
		}
	}
}

func TestOfficialArticleDNSFallbackDocumentationMatchesImplementation(t *testing.T) {
	root := repositoryRoot(t)
	expectations := map[string]string{
		filepath.Join(root, "SKILL.md"):                          "公众号正文不启用外部 DNS 回退",
		filepath.Join(root, "references", "troubleshooting.md"):  "公众号正文不使用外部 DNS 回退",
		filepath.Join(root, "references", "moments-official.md"): "公众号正文不启用朋友圈 CDN 使用的 DNSPod DoT 回退",
	}
	for path, expected := range expectations {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(payload), expected) {
			t.Errorf("%s 没有声明公众号正文的真实 DNS 回退边界", filepath.Base(path))
		}
	}
	definition := schemaCommands(t)["official-article"].(map[string]any)
	if value, found := definition["tun_fake_ip_dns_fallback"]; !found || value != false {
		t.Fatalf("official-article schema 未声明不使用 fake-IP DNS 回退：%v", definition)
	}
}

func TestNativeOCRSandboxBoundaryIsDocumented(t *testing.T) {
	root := repositoryRoot(t)
	for _, path := range []string{
		filepath.Join(root, "README.md"), filepath.Join(root, "SECURITY.md"), filepath.Join(root, "references", "architecture.md"),
	} {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(payload), "no-sandbox") {
			t.Errorf("%s 没有披露实验 OCR 子进程的沙箱边界", filepath.Base(path))
		}
	}
	for _, command := range []string{"ocr-file", "ocr-recognize"} {
		definition := schemaCommands(t)[command].(map[string]any)
		if definition["subprocess_sandboxed"] != false || definition["vendor_no_sandbox_switch"] != true {
			t.Errorf("%s schema 没有披露实验 OCR 子进程的沙箱边界：%v", command, definition)
		}
	}
}

func TestMacOSAcceptanceBoundaryMatchesCapabilities(t *testing.T) {
	result, err := runCapabilities(nil)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := result.(map[string]any)
	validation := capabilities["validation_evidence"].(map[string]any)
	if validation["status"] != "not_embedded" || validation["release_manifest_required_for_real_device_claims"] != true {
		t.Fatalf("capabilities emitted an unbound live validation claim: %v", validation)
	}
	root := repositoryRoot(t)
	acceptancePath := filepath.Join(root, "references", "macos-acceptance.md")
	acceptance, err := os.ReadFile(acceptancePath)
	if err != nil {
		t.Fatal(err)
	}
	documentation := string(acceptance)
	for _, expected := range []string{
		"unverified_legacy_record", "validation_evidence.status=not_embedded", "macOS 不支持微信原生 OCR", "darwin/arm64` 必须继续保持 `build_only`",
	} {
		if !strings.Contains(documentation, expected) {
			t.Errorf("macOS 验收文档缺少边界：%s", expected)
		}
	}
	for _, path := range []string{filepath.Join(root, "README.md"), filepath.Join(root, "SKILL.md"), filepath.Join(root, "references", "architecture.md")} {
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.Contains(string(payload), "macos-acceptance.md") {
			t.Errorf("%s 没有链接 macOS 真机验收边界", filepath.Base(path))
		}
	}
}

func TestDocumentedEnvironmentVariablesMatchImplementation(t *testing.T) {
	root := repositoryRoot(t)
	variablePattern := regexp.MustCompile(`\bV_LOCAL_CLI_[A-Z0-9_]+\b`)
	implemented := make(map[string]bool)
	for _, pattern := range []string{
		filepath.Join(root, "cmd", "**", "*.go"),
		filepath.Join(root, "internal", "**", "*.go"),
		filepath.Join(root, "npm", "scripts", "*.js"),
	} {
		var paths []string
		if strings.Contains(pattern, "**") {
			base := strings.Split(pattern, "**")[0]
			walkErr := filepath.WalkDir(filepath.Clean(base), func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
					paths = append(paths, path)
				}
				return nil
			})
			if walkErr != nil {
				t.Fatal(walkErr)
			}
		} else {
			var globErr error
			paths, globErr = filepath.Glob(pattern)
			if globErr != nil {
				t.Fatal(globErr)
			}
		}
		for _, path := range paths {
			payload, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, name := range variablePattern.FindAllString(string(payload), -1) {
				implemented[name] = true
			}
		}
	}
	documented := make(map[string]bool)
	documentPaths := []string{filepath.Join(root, "README.md"), filepath.Join(root, "SECURITY.md"), filepath.Join(root, "SKILL.md")}
	references, err := filepath.Glob(filepath.Join(root, "references", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	documentPaths = append(documentPaths, references...)
	for _, path := range documentPaths {
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, name := range variablePattern.FindAllString(string(payload), -1) {
			documented[name] = true
		}
	}
	for name := range implemented {
		if !documented[name] {
			t.Errorf("生产代码环境变量缺少文档：%s", name)
		}
	}
	for name := range documented {
		if !implemented[name] {
			t.Errorf("文档环境变量在生产代码中不存在：%s", name)
		}
	}
}
