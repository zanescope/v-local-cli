package provider

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
)

func writeDatabaseOnlyProviderFixture(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if runtime.GOOS == "windows" {
		providerPath := filepath.Join(directory, "provider.cmd")
		powerShellPath := filepath.Join(directory, "provider.ps1")
		const powerShell = `$ErrorActionPreference = 'Stop'
$request = [Console]::In.ReadToEnd() | ConvertFrom-Json
$scopes = @($request.scopes)
if ($scopes.Count -ne 1 -or $scopes[0] -ne 'database') { exit 42 }
$response = [ordered]@{
  protocol = 'v-local-key-provider/v1'
  request_id = $request.request_id
  catalog_id = 'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee'
  catalog_entries = @([ordered]@{database_id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';relative_path='message.db';canonical_file_id='bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';size=4096;mtime_ns=1;first_page_sha256='cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc';classification='encrypted_eligible';required_for_key_coverage=$true;profile_id='wcdb-v4-sha512-256000-r80'})
  database_keys = [ordered]@{'message.db' = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'}
  database_profiles = [ordered]@{'message.db' = 'wcdb-v4-sha512-256000-r80'}
  profiles = @([ordered]@{profile_id='wcdb-v4-sha512-256000-r80';cipher_algorithm='aes-256-cbc';key_size=32;page_size=4096;plaintext_header_size=16;reserve_size=80;kdf_algorithm='pbkdf2';kdf_prf='hmac-sha512';kdf_iterations=256000;hmac_algorithm='hmac-sha512';hmac_kdf_algorithm='pbkdf2';hmac_kdf_iterations=2;hmac_input_layout='page_without_salt_and_hmac_then_page_number';page_number_endian='little-endian'})
  diagnostics = [ordered]@{platform='windows';result_code='complete';workflow_status='terminal';requested_scopes=@('database');database_target_status='present';database_coverage_status='complete';media_coverage_status='not_requested';security_posture_status='not_applicable';shadow_route_status='not_applicable';route_priority=@();routes_attempted=@();next_action='none';target_binding_status='hmac_verified';session_account_status='unknown';candidate_mode='per_database_enc_key';candidate_sources=@();blocking_reasons=@();binary_fingerprint_status='unavailable';binary_signing_status='unavailable';process_architecture='unknown';process_architecture_status='unavailable';compatibility_registry_status='not_evaluated';config_cipher_route_status='not_evaluated';windows_route_evidence=@();static_scan_fallback=$false;process_count=0;selected_process_count=0;target_bound_process_count=0;other_account_process_count=0;unknown_account_process_count=0;opened_process_count=0;access_denied_count=0;per_process_collector_count=0;config_cipher_structure_count=0;config_cipher_invalid_structure_count=0;config_cipher_candidate_count=0;config_cipher_verified_candidate_count=0;fallback_candidate_count=0;fallback_stage_counts=[ordered]@{}}
}

[Console]::Out.WriteLine(($response | ConvertTo-Json -Compress -Depth 4))
`
		if err := os.WriteFile(powerShellPath, []byte(powerShell), 0o600); err != nil {
			t.Fatal(err)
		}
		const wrapper = "@echo off\r\npowershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File \"%~dp0provider.ps1\"\r\n"
		if err := os.WriteFile(providerPath, []byte(wrapper), 0o700); err != nil {
			t.Fatal(err)
		}
		return providerPath
	}
	providerPath := filepath.Join(directory, "provider")
	const script = `#!/bin/sh
payload=$(cat)
case "$payload" in *'"scopes":["database"]'*) ;; *) exit 42 ;; esac
request_id=$(printf '%s' "$payload" | sed -n 's/.*"request_id":"\([0-9a-f]*\)".*/\1/p')
printf '{"protocol":"v-local-key-provider/v1","request_id":"%s","catalog_id":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","catalog_entries":[{"database_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","relative_path":"message.db","canonical_file_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":4096,"mtime_ns":1,"first_page_sha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","classification":"encrypted_eligible","required_for_key_coverage":true,"profile_id":"wcdb-v4-sha512-256000-r80"}],"database_keys":{"message.db":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"database_profiles":{"message.db":"wcdb-v4-sha512-256000-r80"},"profiles":[{"profile_id":"wcdb-v4-sha512-256000-r80","cipher_algorithm":"aes-256-cbc","key_size":32,"page_size":4096,"plaintext_header_size":16,"reserve_size":80,"kdf_algorithm":"pbkdf2","kdf_prf":"hmac-sha512","kdf_iterations":256000,"hmac_algorithm":"hmac-sha512","hmac_kdf_algorithm":"pbkdf2","hmac_kdf_iterations":2,"hmac_input_layout":"page_without_salt_and_hmac_then_page_number","page_number_endian":"little-endian"}],"diagnostics":{"platform":"windows","result_code":"complete","workflow_status":"terminal","requested_scopes":["database"],"database_target_status":"present","database_coverage_status":"complete","media_coverage_status":"not_requested","security_posture_status":"not_applicable","shadow_route_status":"not_applicable","route_priority":[],"routes_attempted":[],"next_action":"none","target_binding_status":"hmac_verified","session_account_status":"unknown","candidate_mode":"per_database_enc_key","candidate_sources":[],"blocking_reasons":[],"binary_fingerprint_status":"unavailable","binary_signing_status":"unavailable","process_architecture":"unknown","process_architecture_status":"unavailable","compatibility_registry_status":"not_evaluated","config_cipher_route_status":"not_evaluated","windows_route_evidence":[],"static_scan_fallback":false,"process_count":0,"selected_process_count":0,"target_bound_process_count":0,"other_account_process_count":0,"unknown_account_process_count":0,"opened_process_count":0,"access_denied_count":0,"per_process_collector_count":0,"config_cipher_structure_count":0,"config_cipher_invalid_structure_count":0,"config_cipher_candidate_count":0,"config_cipher_verified_candidate_count":0,"fallback_candidate_count":0,"fallback_stage_counts":{}}}\n' "$request_id"
`
	if err := os.WriteFile(providerPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return providerPath
}

func writePostureRevalidationProviderFixture(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if runtime.GOOS == "windows" {
		providerPath := filepath.Join(directory, "provider.cmd")
		powerShellPath := filepath.Join(directory, "provider.ps1")
		const powerShell = `$ErrorActionPreference = 'Stop'
$request = [Console]::In.ReadToEnd() | ConvertFrom-Json
if ($request.workflow.operation -ne 'revalidate_security_posture') { exit 42 }
$response = [ordered]@{
  protocol = 'v-local-key-provider/v1'
  request_id = $request.request_id
  diagnostics = [ordered]@{platform='darwin';action_stage='security_posture_revalidation';result_code='complete';workflow_status='terminal';requested_scopes=@('database');database_target_status='not_requested';database_coverage_status='not_requested';media_coverage_status='not_requested';security_posture_status='sip_enabled_verified';routes_attempted=@();candidate_mode='none';candidate_sources=@();next_action='none'}
}
[Console]::Out.WriteLine(($response | ConvertTo-Json -Compress -Depth 4))
`
		if err := os.WriteFile(powerShellPath, []byte(powerShell), 0o600); err != nil {
			t.Fatal(err)
		}
		const wrapper = "@echo off\r\npowershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File \"%~dp0provider.ps1\"\r\n"
		if err := os.WriteFile(providerPath, []byte(wrapper), 0o700); err != nil {
			t.Fatal(err)
		}
		return providerPath
	}
	providerPath := filepath.Join(directory, "provider")
	const script = `#!/bin/sh
payload=$(cat)
case "$payload" in *'"operation":"revalidate_security_posture"'*) ;; *) exit 42 ;; esac
request_id=$(printf '%s' "$payload" | sed -n 's/.*"request_id":"\([0-9a-f]*\)".*/\1/p')
printf '{"protocol":"v-local-key-provider/v1","request_id":"%s","diagnostics":{"platform":"darwin","action_stage":"security_posture_revalidation","result_code":"complete","workflow_status":"terminal","requested_scopes":["database"],"database_target_status":"not_requested","database_coverage_status":"not_requested","media_coverage_status":"not_requested","security_posture_status":"sip_enabled_verified","routes_attempted":[],"candidate_mode":"none","candidate_sources":[],"next_action":"none"}}\n' "$request_id"
`
	if err := os.WriteFile(providerPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return providerPath
}

func TestValidateBundleNormalizesDatabaseKey(t *testing.T) {
	bundle := CandidateBundle{DatabaseKeys: map[string]string{"contact.db": "AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA"}}
	if err := ValidateBundle(&bundle); err != nil {
		t.Fatal(err)
	}
	if got := bundle.DatabaseKeys["contact.db"]; len(got) != 64 || got[:2] != "aa" {
		t.Fatalf("候选密钥未规范化：%q", got)
	}
}

func TestValidateBundleAllowsVerifiedPlaintextOnlyCatalog(t *testing.T) {
	bundle := CandidateBundle{
		Protocol: Protocol, CatalogID: strings.Repeat("e", 64),
		CatalogEntries: []CatalogEntry{{
			DatabaseID: strings.Repeat("a", 64), RelativePath: "plain.db",
			CanonicalFileID: strings.Repeat("b", 64), Size: 4096, MTimeNS: 1,
			FirstPageSHA256: strings.Repeat("c", 64), Classification: "plaintext",
		}},
		Diagnostics: completeDiagnosticDefaults(map[string]any{
			"result_code": "complete", "workflow_status": "terminal", "requested_scopes": []any{"database"},
			"database_target_status": "present", "database_coverage_status": "complete",
			"media_coverage_status": "not_requested", "target_binding_status": "unknown",
		}),
	}
	if err := ValidateBundle(&bundle); err != nil {
		t.Fatalf("plaintext-only complete catalog was rejected: %v", err)
	}
}

func TestValidateBundleAllowsMediaOnlyV1ResponseWithoutDatabaseCatalog(t *testing.T) {
	bundle := validMediaOnlyBundle()
	if err := ValidateBundle(&bundle); err != nil {
		t.Fatalf("media-only response was forced to invent a database catalog: %v", err)
	}
}

func TestFinalAcquisitionResponseDoesNotMaskProtocolErrorAsNoCandidates(t *testing.T) {
	bundle := validMediaOnlyBundle()
	delete(bundle.Diagnostics, "platform")
	err := validateFinalAcquisitionResponse(&bundle, []string{"media"}, localplatform.Account{}, "")
	var contractError *ProtocolContractError
	var acquisitionError *AcquisitionError
	if !errors.As(err, &contractError) {
		t.Fatalf("invalid Provider response was not typed as a protocol contract error: %T %v", err, err)
	}
	if errors.As(err, &acquisitionError) {
		t.Fatalf("protocol contract error was masked as an acquisition outcome: %+v", acquisitionError)
	}
}

func TestValidateBundleRejectsEmptyDatabaseCatalogClaimingComplete(t *testing.T) {
	bundle := CandidateBundle{Protocol: Protocol, CatalogID: strings.Repeat("e", 64), Diagnostics: completeDiagnosticDefaults(map[string]any{
		"result_code": "complete", "workflow_status": "terminal", "requested_scopes": []any{"database"},
		"database_target_status": "none", "database_coverage_status": "complete", "media_coverage_status": "not_requested",
	})}
	if err := ValidateBundle(&bundle); err == nil {
		t.Fatal("empty database catalog was accepted as complete coverage")
	}
}

func validMediaOnlyBundle() CandidateBundle {
	return CandidateBundle{
		Protocol: Protocol, ImageKeys: &ImageKeys{AES: "1234567890abcdef", XOR: 7},
		Diagnostics: completeDiagnosticDefaults(map[string]any{
			"result_code": "complete", "workflow_status": "terminal", "requested_scopes": []any{"media"},
			"database_coverage_status": "not_requested", "media_coverage_status": "complete", "next_action": "none",
		}),
	}
}

func completeDiagnosticDefaults(values map[string]any) map[string]any {
	defaults := map[string]any{
		"platform":               "windows",
		"database_target_status": "not_requested", "security_posture_status": "not_applicable",
		"shadow_route_status": "not_applicable", "route_priority": []any{}, "routes_attempted": []any{},
		"next_action": "none", "target_binding_status": "unknown", "session_account_status": "unknown",
		"candidate_mode": "none", "candidate_sources": []any{}, "blocking_reasons": []any{},
		"binary_fingerprint_status": "unavailable", "binary_signing_status": "unavailable",
		"process_architecture": "unknown", "process_architecture_status": "unavailable",
		"compatibility_registry_status": "not_evaluated", "config_cipher_route_status": "not_evaluated",
		"windows_route_evidence": []any{}, "static_scan_fallback": false,
		"process_count": 0, "selected_process_count": 0, "target_bound_process_count": 0,
		"other_account_process_count": 0, "unknown_account_process_count": 0,
		"opened_process_count": 0, "access_denied_count": 0, "per_process_collector_count": 0,
		"config_cipher_structure_count": 0, "config_cipher_invalid_structure_count": 0,
		"config_cipher_candidate_count": 0, "config_cipher_verified_candidate_count": 0,
		"fallback_candidate_count": 0, "fallback_stage_counts": map[string]any{},
	}
	for name, value := range values {
		defaults[name] = value
	}
	return defaults
}

func TestBlockingReasonAllowlistCoversProviderV1Producers(t *testing.T) {
	// 此列表映射 Provider 有序结果规则和 session 终止路径当前可能生成的全部阻塞原因。
	// 此外可以保留仅供 CLI 使用的原因，但不得把 Provider 生成的值误判为未知协议枚举。
	providerReasons := []string{
		"account_mismatch", "database_targets_not_found", "hook_not_triggered",
		"database_open_required", "login_time_derivation_required", "wechat_not_running",
		"process_access_denied", "process_identity_untrusted", "validator_conflict",
		"candidate_ambiguous", "deadline_exhausted", "action_receipt_required",
		"user_cancelled", "catalog_drift", "acquisition_request_in_progress",
		"action_receipt_rejected", "duplicate_action_without_state_change",
		"action_retry_budget_exhausted", "user_declined_action", "standard_route_unavailable",
		"shadow_route_failed", "shadow_route_unavailable_in_build",
		"shadow_route_unsupported_for_target", "shadow_route_not_evaluated",
		"security_posture_not_verified", "helper_untrusted", "sip_route_failed",
		"sip_disabled_route_not_attempted",
	}
	for _, reason := range providerReasons {
		if !validBlockingReason(reason) {
			t.Errorf("Provider v1 blocking reason %q is not accepted by the CLI", reason)
		}
	}
}

func phase3DarwinDiagnosticDefaults(values map[string]any) map[string]any {
	defaults := map[string]any{
		"platform": "darwin", "wechat_version": "4.1.10", "wechat_build": "31012",
		"executable_sha256": strings.Repeat("a", 64), "binary_fingerprint_status": "verified",
		"binary_signing_status": "verified", "signing_team_id": "TEAM123456",
		"designated_requirement_sha256": strings.Repeat("b", 64),
		"process_architecture":          "arm64", "process_architecture_status": "verified_running_process",
		"process_translation_status": "native", "macos_version": "15.6.1",
		"compatibility_registry_status": "unregistered", "standard_route_status": "eligible_generic_dynamic",
		"standard_route_evidence": []any{"generic_symbol_route_only", "registry_no_exact_match"},
		"shadow_route_status":     "unavailable_in_build", "route_priority": []any{"standard", "shadow", "sip_disabled"},
	}
	for name, value := range values {
		defaults[name] = value
	}
	return defaults
}

func phase4WindowsDiagnosticDefaults(values map[string]any) map[string]any {
	defaults := map[string]any{
		"platform": "windows", "wechat_version": "4.1.2.17", "wechat_build": "2.17",
		"executable_sha256": strings.Repeat("a", 64), "binary_fingerprint_status": "verified",
		"binary_signing_status": "verified", "binary_signer_sha256": strings.Repeat("b", 64),
		"binary_product_identity": "weixin.exe",
		"process_architecture":    "amd64", "process_architecture_status": "verified_running_process",
		"process_translation_status":    "not_applicable",
		"compatibility_registry_status": "unregistered", "config_cipher_route_status": "unavailable_unregistered",
		"windows_route_evidence": []any{"registry_no_exact_match"},
		"process_count":          1, "selected_process_count": 1, "target_bound_process_count": 0,
		"other_account_process_count": 0, "unknown_account_process_count": 1,
		"opened_process_count": 1, "access_denied_count": 0, "per_process_collector_count": 0,
		"config_cipher_structure_count": 0, "config_cipher_invalid_structure_count": 0,
		"config_cipher_candidate_count": 0, "config_cipher_verified_candidate_count": 0,
		"fallback_candidate_count": 0, "fallback_stage_counts": map[string]any{},
		"static_scan_fallback": false, "candidate_sources": []any{},
	}
	for name, value := range values {
		defaults[name] = value
	}
	return defaults
}

func TestValidateBundleRejectsAmbiguousOrContradictoryScopeCoverage(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CandidateBundle)
	}{
		{"legacy unqualified coverage", func(bundle *CandidateBundle) {
			delete(bundle.Diagnostics, "requested_scopes")
			delete(bundle.Diagnostics, "database_coverage_status")
			delete(bundle.Diagnostics, "media_coverage_status")
			bundle.Diagnostics["coverage_status"] = "complete"
		}},
		{"legacy alias retained beside qualified fields", func(bundle *CandidateBundle) {
			bundle.Diagnostics["coverage_status"] = "complete"
		}},
		{"legacy media alias retained beside qualified fields", func(bundle *CandidateBundle) {
			bundle.Diagnostics["media_status"] = "complete"
		}},
		{"unrequested database marked complete", func(bundle *CandidateBundle) {
			bundle.Diagnostics["database_coverage_status"] = "complete"
		}},
		{"requested media incomplete but result complete", func(bundle *CandidateBundle) {
			bundle.ImageKeys = nil
			bundle.Diagnostics["media_coverage_status"] = "none"
		}},
		{"media partial unsupported by atomic v1 image keys", func(bundle *CandidateBundle) {
			bundle.ImageKeys = nil
			bundle.Diagnostics["result_code"] = "partial"
			bundle.Diagnostics["media_coverage_status"] = "partial"
		}},
		{"duplicate requested scope", func(bundle *CandidateBundle) {
			bundle.Diagnostics["requested_scopes"] = []any{"media", "media"}
		}},
		{"missing Shadow route status", func(bundle *CandidateBundle) {
			delete(bundle.Diagnostics, "shadow_route_status")
		}},
		{"missing route priority", func(bundle *CandidateBundle) {
			delete(bundle.Diagnostics, "route_priority")
		}},
		{"missing routes attempted", func(bundle *CandidateBundle) {
			delete(bundle.Diagnostics, "routes_attempted")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := validMediaOnlyBundle()
			test.mutate(&bundle)
			if err := ValidateBundle(&bundle); err == nil {
				t.Fatal("contradictory scope coverage was accepted")
			}
		})
	}
}

func TestRoutePriorityCannotContradictPlatformOrShadowStatus(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CandidateBundle)
	}{
		{"Darwin priority reordered", func(bundle *CandidateBundle) {
			bundle.Diagnostics["platform"] = "darwin"
			bundle.Diagnostics["shadow_route_status"] = "unavailable_in_build"
			bundle.Diagnostics["route_priority"] = []any{"standard", "sip_disabled", "shadow"}
		}},
		{"non-Darwin declares Shadow", func(bundle *CandidateBundle) {
			bundle.Diagnostics["platform"] = "windows"
			bundle.Diagnostics["shadow_route_status"] = "unavailable_in_build"
		}},
		{"unavailable Shadow claims execution", func(bundle *CandidateBundle) {
			bundle.Diagnostics["platform"] = "darwin"
			bundle.Diagnostics["shadow_route_status"] = "unavailable_in_build"
			bundle.Diagnostics["route_priority"] = []any{"standard", "shadow", "sip_disabled"}
			bundle.Diagnostics["routes_attempted"] = []any{"darwin_arm64_shadow_dynamic"}
		}},
		{"failed Shadow lacks execution", func(bundle *CandidateBundle) {
			bundle.Diagnostics["platform"] = "darwin"
			bundle.Diagnostics["shadow_route_status"] = "attempted_failed"
			bundle.Diagnostics["route_priority"] = []any{"standard", "shadow", "sip_disabled"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := validMediaOnlyBundle()
			test.mutate(&bundle)
			if err := ValidateBundle(&bundle); err == nil {
				t.Fatal("contradictory route priority or Shadow status was accepted")
			}
		})
	}
}

func TestRoutesAttemptedRequireStablePlatformSpecificIDs(t *testing.T) {
	bundle := validMediaOnlyBundle()
	for name, value := range phase3DarwinDiagnosticDefaults(nil) {
		bundle.Diagnostics[name] = value
	}
	bundle.Diagnostics["routes_attempted"] = []any{"fabricated_sip_disabled_route"}
	if err := ValidateBundle(&bundle); err == nil {
		t.Fatal("unknown route ID was accepted as SIP-disabled machine evidence")
	}
	bundle.Diagnostics["routes_attempted"] = []any{"windows_memory_fallback"}
	if err := ValidateBundle(&bundle); err == nil {
		t.Fatal("cross-platform route ID was accepted")
	}
	bundle.Diagnostics["routes_attempted"] = []any{"darwin_arm64_standard_dynamic"}
	bundle.Diagnostics["route_selected"] = "darwin_static_fallback"
	if err := ValidateBundle(&bundle); err == nil {
		t.Fatal("route_selected outside routes_attempted was accepted")
	}
	bundle.Diagnostics["route_selected"] = "darwin_arm64_standard_dynamic"
	if err := ValidateBundle(&bundle); err != nil {
		t.Fatalf("stable selected route was rejected: %v", err)
	}
}

func TestValidateBundleRejectsRequestedScopeEchoMismatch(t *testing.T) {
	bundle := validMediaOnlyBundle()
	if err := validateBundleForScopes(&bundle, []string{"database"}); err == nil {
		t.Fatal("provider scope echo mismatch was accepted")
	}
}

func TestDisableSIPActionRequiresTerminalShadowRouteEvidence(t *testing.T) {
	base := func() CandidateBundle {
		return CandidateBundle{Protocol: Protocol, Diagnostics: completeDiagnosticDefaults(phase3DarwinDiagnosticDefaults(map[string]any{
			"result_code": "action_required", "workflow_status": "waiting_action",
			"requested_scopes": []any{"media"}, "database_coverage_status": "not_requested", "media_coverage_status": "none",
			"security_posture_status": "sip_enabled_verified", "next_action": "disable_sip",
			"process_access_status": "denied", "process_access_error": "sip_enabled",
			"route_priority":   []any{"standard", "shadow", "sip_disabled"},
			"routes_attempted": []any{"darwin_static_fallback"},
			"blocking_reasons": []any{"standard_route_unavailable"},
		}))}
	}
	tests := []struct {
		name         string
		shadowStatus string
		shadowReason string
		wantValid    bool
	}{
		{"not evaluated", "not_evaluated", "", false},
		{"implemented and available", "available", "", false},
		{"unavailable in build", "unavailable_in_build", "shadow_route_unavailable_in_build", true},
		{"unsupported target", "unsupported_for_target", "shadow_route_unsupported_for_target", true},
		{"attempted failure", "attempted_failed", "shadow_route_failed", true},
		{"status reason mismatch", "unavailable_in_build", "shadow_route_failed", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := base()
			bundle.Diagnostics["shadow_route_status"] = test.shadowStatus
			if test.shadowStatus == "attempted_failed" {
				bundle.Diagnostics["routes_attempted"] = []any{"darwin_static_fallback", "darwin_arm64_shadow_dynamic"}
			}
			if test.shadowReason != "" {
				bundle.Diagnostics["blocking_reasons"] = []any{"standard_route_unavailable", test.shadowReason}
			}
			err := ValidateBundle(&bundle)
			if test.wantValid && err != nil {
				t.Fatalf("terminal Shadow evidence was rejected: %v", err)
			}
			if !test.wantValid && err == nil {
				t.Fatal("disable_sip was accepted without matching terminal Shadow evidence")
			}
		})
	}
}

func TestDarwinEvidenceCannotMisleadTheAgent(t *testing.T) {
	base := func() CandidateBundle {
		bundle := validMediaOnlyBundle()
		for name, value := range phase3DarwinDiagnosticDefaults(nil) {
			bundle.Diagnostics[name] = value
		}
		bundle.Diagnostics["shadow_route_status"] = "unavailable_in_build"
		bundle.Diagnostics["route_priority"] = []any{"standard", "shadow", "sip_disabled"}
		return bundle
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing architecture status", func(values map[string]any) { delete(values, "process_architecture_status") }},
		{"GOARCH guess presented without process evidence", func(values map[string]any) {
			values["process_architecture_status"] = "not_evaluated"
		}},
		{"verified fingerprint without digest", func(values map[string]any) { delete(values, "executable_sha256") }},
		{"verified signing without requirement", func(values map[string]any) { delete(values, "designated_requirement_sha256") }},
		{"unregistered binary claims registered route", func(values map[string]any) { values["standard_route_status"] = "eligible_registered" }},
		{"route ABI contradicts target process", func(values map[string]any) { values["routes_attempted"] = []any{"darwin_amd64_standard_dynamic"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := base()
			test.mutate(bundle.Diagnostics)
			if err := ValidateBundle(&bundle); err == nil {
				t.Fatal("contradictory Phase 3 evidence was accepted")
			}
		})
	}
	if err := ValidateBundle(func() *CandidateBundle { value := base(); return &value }()); err != nil {
		t.Fatalf("complete Phase 3 evidence was rejected: %v", err)
	}
}

func TestWindowsEvidenceCannotMisleadTheAgent(t *testing.T) {
	base := func() CandidateBundle {
		bundle := validMediaOnlyBundle()
		for name, value := range phase4WindowsDiagnosticDefaults(nil) {
			bundle.Diagnostics[name] = value
		}
		return bundle
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing Config.Cipher status", func(values map[string]any) { delete(values, "config_cipher_route_status") }},
		{"uppercase executable digest", func(values map[string]any) { values["executable_sha256"] = strings.Repeat("A", 64) }},
		{"verified signing without signer digest", func(values map[string]any) { delete(values, "binary_signer_sha256") }},
		{"unregistered binary claims exact route", func(values map[string]any) { values["config_cipher_route_status"] = "eligible_registered" }},
		{"unknown route evidence", func(values map[string]any) { values["windows_route_evidence"] = []any{"C:\\private\\path"} }},
		{"mixed architecture from one process", func(values map[string]any) { values["process_architecture"] = "mixed" }},
		{"selected process count omits unknown account", func(values map[string]any) { values["selected_process_count"] = 0 }},
		{"path binding without target process", func(values map[string]any) {
			values["target_binding_status"] = "path_verified"
			values["session_account_status"] = "known_target"
		}},
		{"mismatch still selects unknown process", func(values map[string]any) {
			values["target_binding_status"] = "mismatch"
			values["session_account_status"] = "known_other"
			values["other_account_process_count"] = 1
		}},
		{"fallback route lacks stage evidence", func(values map[string]any) {
			values["routes_attempted"] = []any{"windows_memory_fallback"}
			values["static_scan_fallback"] = true
		}},
		{"collector without opened process", func(values map[string]any) {
			values["process_count"] = 0
			values["selected_process_count"] = 0
			values["unknown_account_process_count"] = 0
			values["opened_process_count"] = 0
			values["per_process_collector_count"] = 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := base()
			test.mutate(bundle.Diagnostics)
			if err := ValidateBundle(&bundle); err == nil {
				t.Fatal("contradictory Phase 4 evidence was accepted")
			}
		})
	}
	if err := ValidateBundle(func() *CandidateBundle { value := base(); return &value }()); err != nil {
		t.Fatalf("complete Phase 4 evidence was rejected: %v", err)
	}
}

func TestWindowsRegisteredConfigCipherEvidenceIsExactAndOrdered(t *testing.T) {
	values := phase4WindowsDiagnosticDefaults(map[string]any{
		"target_binding_status": "path_verified", "session_account_status": "known_target",
		"target_bound_process_count": 1, "unknown_account_process_count": 0,
		"compatibility_registry_status": "registered_supported", "config_cipher_route_status": "succeeded",
		"windows_route_evidence":        []any{"registry_candidate_entry", "registry_exact_match"},
		"routes_attempted":              []any{"windows_config_cipher", "windows_memory_fallback"},
		"config_cipher_structure_count": 1, "config_cipher_candidate_count": 1,
		"config_cipher_verified_candidate_count": 1,
		"static_scan_fallback":                   true, "fallback_stage_counts": map[string]any{"bounded_writable_heap": 1},
		"per_process_collector_count": 2, "candidate_sources": []any{"windows_config_cipher"},
	})
	if err := validateWindowsEvidence(values, diagnosticStrings(values, "routes_attempted")); err != nil {
		t.Fatalf("exact registered Config.Cipher evidence was rejected: %v", err)
	}
	values["routes_attempted"] = []any{"windows_memory_fallback", "windows_config_cipher"}
	if err := validateWindowsEvidence(values, diagnosticStrings(values, "routes_attempted")); err == nil {
		t.Fatal("fallback-before-Config.Cipher route order was accepted")
	}
	values["routes_attempted"] = []any{"windows_config_cipher", "windows_memory_fallback"}
	values["candidate_sources"] = []any{"bounded_heap"}
	if err := validateWindowsEvidence(values, diagnosticStrings(values, "routes_attempted")); err == nil {
		t.Fatal("verified Config.Cipher candidate without provenance was accepted")
	}
}

func TestWindowsReviewedNoStructureRequiresExactRegisteredFallback(t *testing.T) {
	base := func() map[string]any {
		return phase4WindowsDiagnosticDefaults(map[string]any{
			"target_binding_status": "path_verified", "session_account_status": "known_target",
			"target_bound_process_count": 1, "unknown_account_process_count": 0,
			"compatibility_registry_status": "registered_supported",
			"config_cipher_route_status":    "registered_reviewed_no_structure",
			"windows_route_evidence":        []any{"registry_candidate_entry", "registry_exact_match"},
			"routes_attempted":              []any{"windows_memory_fallback"},
			"static_scan_fallback":          true,
			"fallback_stage_counts":         map[string]any{"structured_key_object": 1},
			"per_process_collector_count":   1,
		})
	}
	if values := base(); validateWindowsEvidence(values, diagnosticStrings(values, "routes_attempted")) != nil {
		t.Fatal("精确登记且已审核无结构的 fallback 证据被拒绝")
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"未登记目标", func(values map[string]any) { values["compatibility_registry_status"] = "unregistered" }},
		{"虚构 Config.Cipher 执行", func(values map[string]any) {
			values["routes_attempted"] = []any{"windows_config_cipher", "windows_memory_fallback"}
		}},
		{"虚构结构计数", func(values map[string]any) { values["config_cipher_structure_count"] = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := base()
			test.mutate(values)
			if err := validateWindowsEvidence(values, diagnosticStrings(values, "routes_attempted")); err == nil {
				t.Fatal("矛盾的已审核无结构证据被接受")
			}
		})
	}
}

func TestReleaseCLIRequiresPromotionBoundRegistryEvidence(t *testing.T) {
	previous := buildMode
	buildMode = "release"
	t.Cleanup(func() { buildMode = previous })
	windows := phase4WindowsDiagnosticDefaults(map[string]any{
		"target_binding_status": "path_verified", "session_account_status": "known_target",
		"target_bound_process_count": 1, "unknown_account_process_count": 0,
		"compatibility_registry_status": "registered_supported", "config_cipher_route_status": "eligible_registered",
		"windows_route_evidence": []any{"registry_candidate_entry", "registry_exact_match"},
	})
	if err := validateWindowsEvidence(windows, nil); err == nil {
		t.Fatal("release CLI accepted candidate-only Windows registry evidence")
	}
	windows["windows_route_evidence"] = []any{"real_device_evidence_present", "registry_exact_match", "release_promotion_verified"}
	if err := validateWindowsEvidence(windows, nil); err != nil {
		t.Fatalf("release CLI rejected promotion-bound Windows registry evidence: %v", err)
	}

	darwin := phase3DarwinDiagnosticDefaults(map[string]any{
		"compatibility_registry_status": "registered_supported", "standard_route_status": "eligible_registered",
		"standard_route_evidence": []any{"registry_candidate_entry", "registry_exact_match"},
	})
	if err := validateDarwinEvidence(darwin); err == nil {
		t.Fatal("release CLI accepted candidate-only Darwin registry evidence")
	}
	darwin["standard_route_evidence"] = []any{"real_device_evidence_present", "registry_exact_match", "release_promotion_verified"}
	if err := validateDarwinEvidence(darwin); err != nil {
		t.Fatalf("release CLI rejected promotion-bound Darwin registry evidence: %v", err)
	}
}

func TestCredentialAccountBindingMatchesCurrentRequest(t *testing.T) {
	accountPath := t.TempDir()
	dbDir := filepath.Join(accountPath, "db_storage")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	keyHex := strings.Repeat("17", 32)
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		t.Fatal(err)
	}
	realPath, err := filepath.EvalSymlinks(accountPath)
	if err != nil {
		t.Fatal(err)
	}
	credential := &DatabaseCredential{AccountBindingID: credentialCatalogHMAC(key, "account", realPath)}
	if err := validateProviderAccountBinding(credential, localplatform.Account{Path: accountPath}, keyHex); err != nil {
		t.Fatalf("matching account binding was rejected: %v", err)
	}
	credential.StorageAccountID = "local-keyring-account"
	if err := validateProviderAccountBinding(credential, localplatform.Account{Path: accountPath}, keyHex); err == nil {
		t.Fatal("Provider wire credential was allowed to set the CLI-local storage account binding")
	}
	credential.StorageAccountID = ""
	credential.AccountBindingID = strings.Repeat("f", 64)
	if err := validateProviderAccountBinding(credential, localplatform.Account{Path: accountPath}, keyHex); err == nil {
		t.Fatal("credential bound to another account was accepted")
	}
	canonical, err := canonicalAcquisitionRequestAccount(localplatform.Account{Path: accountPath, DBDir: dbDir})
	if err != nil || !filepath.IsAbs(canonical.Path) || !filepath.IsAbs(canonical.DBDir) {
		t.Fatalf("acquisition request paths were not canonicalized: account=%+v err=%v", canonical, err)
	}
	if _, err := canonicalAcquisitionRequestAccount(localplatform.Account{Path: accountPath, DBDir: t.TempDir()}); err == nil {
		t.Fatal("database directory outside the requested account was accepted")
	}
}

func TestValidateBundleAllowsPlaintextPartOfDeadlinePartial(t *testing.T) {
	bundle := CandidateBundle{
		Protocol: Protocol, CatalogID: strings.Repeat("e", 64),
		CatalogEntries: []CatalogEntry{{
			DatabaseID: strings.Repeat("a", 64), RelativePath: "plain.db",
			CanonicalFileID: strings.Repeat("b", 64), Size: 4096, MTimeNS: 1,
			FirstPageSHA256: strings.Repeat("c", 64), Classification: "plaintext",
		}},
		Profiles: []ProfileSummary{{
			ID: "wcdb-v4-sha512-256000-r80", CipherAlgorithm: "aes-256-cbc", KeySize: 32,
			PageSize: 4096, PlaintextHeaderSize: 16, ReserveSize: 80,
			KDFAlgorithm: "pbkdf2", KDFPRF: "hmac-sha512", KDFIterations: 256000,
			HMACAlgorithm: "hmac-sha512", HMACKDFAlgorithm: "pbkdf2", HMACKDFIterations: 2,
			HMACInputLayout: "page_without_salt_and_hmac_then_page_number", PageNumberEndian: "little-endian",
		}},
		Diagnostics: completeDiagnosticDefaults(map[string]any{
			"result_code": "deadline_exhausted", "workflow_status": "terminal", "requested_scopes": []any{"database"},
			"database_target_status": "present", "database_coverage_status": "partial", "media_coverage_status": "not_requested",
			"next_action": "stop_and_report", "blocking_reasons": []any{"deadline_exhausted"},
		}),
	}
	if err := ValidateBundle(&bundle); err != nil {
		t.Fatalf("verified plaintext portion of deadline partial was rejected: %v", err)
	}
}

func TestValidateBundleRejectsInvalidImageKeys(t *testing.T) {
	bundle := CandidateBundle{
		DatabaseKeys: map[string]string{"*": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		ImageKeys:    &ImageKeys{AES: "short", XOR: 256},
	}
	if err := ValidateBundle(&bundle); err == nil {
		t.Fatal("预期图片候选格式校验失败")
	}
}

func TestAcquireScopesSendsDatabaseOnlyRequest(t *testing.T) {
	providerPath := writeDatabaseOnlyProviderFixture(t)
	accountPath := filepath.Join(t.TempDir(), "account")
	dbDir := filepath.Join(accountPath, "db_storage")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	bundle, err := AcquireScopes(context.Background(), providerPath, localplatform.Account{Path: accountPath, DBDir: dbDir}, []string{"database"})
	if err != nil {
		t.Fatalf("database-only provider request failed: %v", err)
	}
	if len(bundle.DatabaseKeys) != 1 || bundle.ImageKeys != nil {
		t.Fatalf("unexpected database-only bundle: %+v", bundle)
	}
}

func TestAcquisitionErrorUsesOnlySafeDiagnosticEnums(t *testing.T) {
	err := acquisitionError(map[string]any{
		"platform": "darwin", "process_access_status": "denied",
		"process_access_error": "task_for_pid_denied", "helper_status": "not_installed",
		"process_discovery_method": "ps_then_launchctl",
		"database_keys":            "must-not-be-read",
	})
	if err.Reason != "process_access_denied" || err.Platform != "darwin" || err.HelperStatus != "not_installed" || err.ProcessDiscoveryMethod != "ps_then_launchctl" {
		t.Fatalf("unexpected acquisition error: %+v", err)
	}
}

func TestAcquisitionErrorClassifiesValidatorConflictSeparately(t *testing.T) {
	err := acquisitionError(map[string]any{
		"result_code": "failed", "workflow_status": "blocked",
		"blocking_reasons": []any{"validator_conflict"},
	})
	if err.Reason != "validator_conflict" {
		t.Fatalf("validator conflict was not preserved: %+v", err)
	}
}

func TestConfirmedActionCannotFallBackToOneShot(t *testing.T) {
	err := daemonRequiredForConfirmedAction("trigger_database")
	if err.Reason != "action_confirmation_mismatch" || err.WorkflowStatus != "blocked" ||
		err.NextAction != "trigger_database" || len(err.BlockingReasons) != 1 || err.BlockingReasons[0] != "acquisition_daemon_unavailable" {
		t.Fatalf("confirmed action fallback was not blocked: %+v", err)
	}
}

func TestAcquisitionStateRejectsAccountMismatchEvenWithPartialKeys(t *testing.T) {
	err := acquisitionStateError(map[string]any{
		"result_code": "action_required", "workflow_status": "waiting_action",
		"database_coverage_status": "partial", "next_action": "switch_to_target_account",
		"target_binding_status": "mismatch", "blocking_reasons": []any{"account_mismatch"},
	})
	var acquisition *AcquisitionError
	if !errors.As(err, &acquisition) || acquisition.Reason != "account_mismatch" || acquisition.DatabaseCoverageStatus != "partial" {
		t.Fatalf("account mismatch state was not preserved: %#v", err)
	}
}

func TestAcquisitionStateAllowsTerminalPartialForMissingOnlyPublication(t *testing.T) {
	err := acquisitionStateError(map[string]any{
		"result_code": "partial", "workflow_status": "terminal", "database_coverage_status": "partial",
		"target_binding_status": "hmac_verified", "missing_database_count": float64(1),
	})
	if err != nil {
		t.Fatalf("terminal partial result should reach coverage-regression checks: %v", err)
	}
}

func TestAcquisitionStateAllowsTerminalDeadlinePartialButNotNone(t *testing.T) {
	partial := map[string]any{
		"result_code": "deadline_exhausted", "workflow_status": "terminal", "requested_scopes": []any{"database"},
		"database_coverage_status": "partial", "media_coverage_status": "not_requested",
	}
	if err := acquisitionStateError(partial); err != nil {
		t.Fatalf("terminal deadline partial was discarded: %v", err)
	}
	none := map[string]any{
		"result_code": "deadline_exhausted", "workflow_status": "terminal", "requested_scopes": []any{"database"},
		"database_coverage_status": "none", "media_coverage_status": "not_requested",
	}
	if err := acquisitionStateError(none); err == nil {
		t.Fatal("deadline result without verified coverage was accepted")
	}
}

func TestAcquisitionStateAllowsDeadlineWithCompleteDatabaseAndMissingMedia(t *testing.T) {
	values := map[string]any{
		"result_code": "deadline_exhausted", "workflow_status": "terminal", "requested_scopes": []any{"database", "media"},
		"database_coverage_status": "complete", "media_coverage_status": "none",
	}
	if err := acquisitionStateError(values); err != nil {
		t.Fatalf("verified database coverage was discarded because requested media missed its deadline: %v", err)
	}
}

func TestAcquisitionStateAllowsVerifiedCredentialsPendingSIPRestoration(t *testing.T) {
	values := map[string]any{
		"result_code": "action_required", "workflow_status": "waiting_action", "requested_scopes": []any{"database", "media"},
		"database_coverage_status": "complete", "media_coverage_status": "complete",
		"security_posture_status": "restoration_required", "next_action": "reenable_sip",
	}
	if err := acquisitionStateError(values); err != nil {
		t.Fatalf("verified credentials were discarded before SIP restoration: %v", err)
	}
}

func TestReenableSIPActionRequiresDarwinRestorationEvidence(t *testing.T) {
	bundle := validMediaOnlyBundle()
	bundle.Diagnostics["result_code"] = "action_required"
	bundle.Diagnostics["workflow_status"] = "waiting_action"
	bundle.Diagnostics["media_coverage_status"] = "complete"
	bundle.Diagnostics["security_posture_status"] = "restoration_required"
	bundle.Diagnostics["next_action"] = "reenable_sip"
	if err := ValidateBundle(&bundle); err == nil {
		t.Fatal("non-macOS response was allowed to request SIP restoration")
	}
}

func TestFailedSIPRouteRestorationRequiresAnAttemptedSIPDisabledRoute(t *testing.T) {
	bundle := validMediaOnlyBundle()
	bundle.ImageKeys = nil
	for name, value := range phase3DarwinDiagnosticDefaults(nil) {
		bundle.Diagnostics[name] = value
	}
	bundle.Diagnostics["result_code"] = "action_required"
	bundle.Diagnostics["workflow_status"] = "waiting_action"
	bundle.Diagnostics["media_coverage_status"] = "none"
	bundle.Diagnostics["security_posture_status"] = "restoration_required"
	bundle.Diagnostics["next_action"] = "reenable_sip"
	bundle.Diagnostics["blocking_reasons"] = []any{"sip_route_failed"}
	bundle.Diagnostics["route_priority"] = []any{"standard", "shadow", "sip_disabled"}
	bundle.Diagnostics["shadow_route_status"] = "unavailable_in_build"
	bundle.Diagnostics["routes_attempted"] = []any{"darwin_arm64_sip_disabled"}
	if err := ValidateBundle(&bundle); err != nil {
		t.Fatalf("valid SIP restoration response was rejected: %v", err)
	}
	bundle.Diagnostics["routes_attempted"] = []any{"darwin_arm64_standard_dynamic"}
	if err := ValidateBundle(&bundle); err == nil {
		t.Fatal("SIP restoration was accepted without an attempted SIP-disabled route")
	}
}

func TestSIPDisabledPreflightRestorationRequiresNoAttemptedSIPRoute(t *testing.T) {
	bundle := validMediaOnlyBundle()
	bundle.ImageKeys = nil
	for name, value := range phase3DarwinDiagnosticDefaults(nil) {
		bundle.Diagnostics[name] = value
	}
	bundle.Diagnostics["result_code"] = "action_required"
	bundle.Diagnostics["workflow_status"] = "waiting_action"
	bundle.Diagnostics["media_coverage_status"] = "none"
	bundle.Diagnostics["security_posture_status"] = "restoration_required"
	bundle.Diagnostics["next_action"] = "reenable_sip"
	bundle.Diagnostics["blocking_reasons"] = []any{"sip_disabled_route_not_attempted"}
	bundle.Diagnostics["route_priority"] = []any{"standard", "shadow", "sip_disabled"}
	bundle.Diagnostics["shadow_route_status"] = "unavailable_in_build"
	bundle.Diagnostics["routes_attempted"] = []any{}
	if err := ValidateBundle(&bundle); err != nil {
		t.Fatalf("valid pre-route SIP restoration response was rejected: %v", err)
	}
	bundle.Diagnostics["routes_attempted"] = []any{"darwin_arm64_sip_disabled"}
	if err := ValidateBundle(&bundle); err == nil {
		t.Fatal("SIP route-not-attempted evidence contradicted routes_attempted without rejection")
	}
}

func TestSecurityPostureRevalidationIsSecretFreeAndStrictlyTyped(t *testing.T) {
	bundle := CandidateBundle{Diagnostics: map[string]any{
		"platform": "darwin", "action_stage": "security_posture_revalidation",
		"requested_scopes": []any{"database", "media"}, "database_target_status": "not_requested",
		"database_coverage_status": "not_requested", "media_coverage_status": "not_requested",
		"routes_attempted": []any{}, "security_posture_status": "sip_enabled_verified",
		"candidate_mode": "none", "candidate_sources": []any{},
		"result_code": "complete", "workflow_status": "terminal", "next_action": "none",
	}}
	if err := validateSecurityPostureRevalidation(bundle, []string{"database", "media"}); err != nil {
		t.Fatalf("valid security posture revalidation was rejected: %v", err)
	}
	if !IsSecurityPostureRevalidation(bundle) {
		t.Fatal("successful security posture revalidation was not recognized by setup")
	}
	bundle.DatabaseKeys = map[string]string{"message.db": strings.Repeat("a", 64)}
	if err := validateSecurityPostureRevalidation(bundle, []string{"database", "media"}); err == nil {
		t.Fatal("security posture revalidation was allowed to carry key material")
	}
	bundle.DatabaseKeys = nil
	bundle.Diagnostics["process_access_status"] = "direct_opened"
	if err := validateSecurityPostureRevalidation(bundle, []string{"database", "media"}); err == nil {
		t.Fatal("security posture revalidation was allowed to report process access")
	}
	delete(bundle.Diagnostics, "process_access_status")
	bundle.Diagnostics["scanned_bytes"] = float64(4096)
	if err := validateSecurityPostureRevalidation(bundle, []string{"database", "media"}); err == nil {
		t.Fatal("security posture revalidation was allowed to report process scanning")
	}
	bundle.Diagnostics["scanned_bytes"] = "0"
	if err := validateSecurityPostureRevalidation(bundle, []string{"database", "media"}); err == nil {
		t.Fatal("security posture revalidation accepted a mistyped process counter")
	}
}

func TestRestorationCheckpointUsesPostureOnlyRequestInsteadOfReacquisition(t *testing.T) {
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account")
	dbPath := filepath.Join(accountPath, "db")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	providerPath := writePostureRevalidationProviderFixture(t)
	account := localplatform.Account{Path: accountPath, DBDir: dbPath}
	disable := &AcquisitionError{NextAction: "disable_sip", SecurityPostureStatus: "sip_enabled_verified"}
	_ = reconcileExternalCheckpoint(root, providerPath, account, []string{"database"}, CandidateBundle{}, disable)
	restore := &AcquisitionError{NextAction: "reenable_sip", SecurityPostureStatus: "restoration_required"}
	_ = reconcileExternalCheckpoint(root, providerPath, account, []string{"database"}, CandidateBundle{}, restore)
	if _, err := AcquireScopesWithRootAndAction(context.Background(), providerPath, account, []string{"database"}, root, "reenable_sip"); err == nil {
		t.Fatal("cross-reboot restoration incorrectly accepted an action receipt")
	} else {
		var acquisition *AcquisitionError
		if !errors.As(err, &acquisition) || acquisition.Reason != "action_confirmation_mismatch" ||
			acquisition.ExternalCheckpointStatus != "persisted" || acquisition.ExternalWorkflowID == "" {
			t.Fatalf("rejected restoration receipt lost existing checkpoint evidence: %+v", err)
		}
	}
	if checkpoints, err := ListExternalCheckpoints(root); err != nil || len(checkpoints) != 1 {
		t.Fatalf("rejected restoration receipt changed checkpoint state: checkpoints=%+v err=%v", checkpoints, err)
	}
	bundle, err := AcquireScopesWithRoot(context.Background(), providerPath, account, []string{"database"}, root)
	if err != nil {
		t.Fatalf("posture-only restoration request failed: %v", err)
	}
	if !IsSecurityPostureRevalidation(bundle) || len(bundle.DatabaseKeys) != 0 || bundle.ImageKeys != nil {
		t.Fatalf("restoration unexpectedly performed acquisition: %+v", bundle)
	}
	if checkpoints, err := ListExternalCheckpoints(root); err != nil || len(checkpoints) != 0 {
		t.Fatalf("verified restoration did not clear checkpoint: checkpoints=%+v err=%v", checkpoints, err)
	}
}

func TestAcquireScopesRejectsDuplicateOrUnknownScopeBeforeProviderResolution(t *testing.T) {
	account := localplatform.Account{Path: "unused", DBDir: "unused"}
	for _, scopes := range [][]string{{"database", "database"}, {"unknown"}} {
		_, err := AcquireScopes(context.Background(), "", account, scopes)
		if err == nil || errors.Is(err, ErrComponentMissing) {
			t.Fatalf("invalid scopes reached provider resolution: scopes=%v err=%v", scopes, err)
		}
	}
}

func TestAcquisitionErrorReportsUnavailableProcessList(t *testing.T) {
	err := acquisitionError(map[string]any{
		"platform": "darwin", "process_access_status": "process_list_unavailable",
		"process_access_error":        "process_list_unavailable",
		"process_discovery_method":    "ps_then_launchctl",
		"process_access_error_detail": "/private/secret/path",
	})
	if err.Reason != "process_list_unavailable" {
		t.Fatalf("unexpected process discovery reason: %+v", err)
	}
	if err.ProcessDiscoveryMethod != "ps_then_launchctl" {
		t.Fatalf("discovery method was not preserved: %+v", err)
	}
	if strings.Contains(err.Error(), "/private/secret/path") {
		t.Fatalf("unsafe diagnostic leaked: %s", err.Error())
	}
}

func TestAcquisitionErrorReportsSIPRequirement(t *testing.T) {
	err := acquisitionError(map[string]any{
		"platform": "darwin", "process_access_status": "denied",
		"process_access_error": "sip_enabled", "helper_status": "sip_enabled", "next_action": "disable_sip",
		"shadow_route_status": "unavailable_in_build", "route_priority": []any{"standard", "shadow", "sip_disabled"},
	})
	if err.Reason != "sip_required" || err.ShadowRouteStatus != "unavailable_in_build" || len(err.RoutePriority) != 3 {
		t.Fatalf("unexpected SIP reason: %+v", err)
	}
}

func TestAcquisitionErrorDoesNotPromoteObservedSIPStateIntoDisableRequest(t *testing.T) {
	err := acquisitionError(map[string]any{
		"platform": "darwin", "process_access_status": "denied", "result_code": "unsupported",
		"process_access_error": "sip_enabled", "helper_status": "sip_enabled", "next_action": "stop_and_report",
	})
	if err.Reason != "unsupported" {
		t.Fatalf("observed SIP state was promoted into an unproven disable request: %+v", err)
	}
}

func TestAcquisitionErrorReportsHookTriggerRequirement(t *testing.T) {
	err := acquisitionError(map[string]any{
		"platform": "darwin", "process_access_status": "dynamic_hook_opened",
		"process_access_error": "hook_trigger_required", "version_support": "commoncrypto_dynamic",
	})
	if err.Reason != "hook_trigger_required" || err.VersionSupport != "commoncrypto_dynamic" {
		t.Fatalf("unexpected hook trigger reason: %+v", err)
	}
}

func TestAcquisitionErrorReportsHookRestartRequirement(t *testing.T) {
	err := acquisitionError(map[string]any{
		"platform": "darwin", "process_access_status": "helper_opened",
		"process_access_error": "hook_restart_required",
	})
	if err.Reason != "hook_restart_required" {
		t.Fatalf("unexpected hook restart reason: %+v", err)
	}
}

func phaseRegressionProfile() ProfileSummary {
	return ProfileSummary{
		ID: "wcdb-v4-sha512-256000-r80", CipherAlgorithm: "aes-256-cbc", KeySize: 32,
		PageSize: 4096, PlaintextHeaderSize: 16, ReserveSize: 80,
		KDFAlgorithm: "pbkdf2", KDFPRF: "hmac-sha512", KDFIterations: 256000,
		HMACAlgorithm: "hmac-sha512", HMACKDFAlgorithm: "pbkdf2", HMACKDFIterations: 2,
		HMACInputLayout: "page_without_salt_and_hmac_then_page_number", PageNumberEndian: "little-endian",
	}
}

func phaseRegressionPartialBundle() CandidateBundle {
	firstID := strings.Repeat("a", 64)
	secondID := strings.Repeat("b", 64)
	return CandidateBundle{
		Protocol: Protocol, CatalogID: strings.Repeat("c", 64),
		CatalogEntries: []CatalogEntry{
			{DatabaseID: firstID, RelativePath: "first.db", CanonicalFileID: strings.Repeat("d", 64), Size: 4096, MTimeNS: 1, FirstPageSHA256: strings.Repeat("e", 64), Classification: "encrypted_eligible", RequiredForKeyCoverage: true, ProfileID: "wcdb-v4-sha512-256000-r80"},
			{DatabaseID: secondID, RelativePath: "second.db", CanonicalFileID: strings.Repeat("f", 64), Size: 4096, MTimeNS: 2, FirstPageSHA256: strings.Repeat("1", 64), Classification: "encrypted_eligible", RequiredForKeyCoverage: true, ProfileID: "wcdb-v4-sha512-256000-r80"},
		},
		DatabaseKeys:     map[string]string{"first.db": strings.Repeat("2", 64)},
		DatabaseProfiles: map[string]string{"first.db": "wcdb-v4-sha512-256000-r80"},
		Profiles:         []ProfileSummary{phaseRegressionProfile()},
		Diagnostics: completeDiagnosticDefaults(map[string]any{
			"result_code": "partial", "workflow_status": "terminal", "requested_scopes": []any{"database"},
			"database_target_status": "present", "database_coverage_status": "partial", "media_coverage_status": "not_requested",
			"target_binding_status": "hmac_verified", "candidate_mode": "per_database_enc_key",
			"database_count": float64(2), "required_database_count": float64(2),
			"matched_database_count": float64(1), "missing_database_count": float64(1),
			"plaintext_database_count": float64(0), "unreadable_database_count": float64(0),
			"unstable_database_count": float64(0), "truncated_database_count": float64(0),
			"missing_database_ids": []any{secondID},
		}),
	}
}

func TestValidateBundleRecomputesCoverageDiagnosticsFromCatalog(t *testing.T) {
	bundle := phaseRegressionPartialBundle()
	if err := ValidateBundle(&bundle); err != nil {
		t.Fatalf("consistent partial coverage was rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*CandidateBundle)
	}{
		{"forged matched count", func(value *CandidateBundle) { value.Diagnostics["matched_database_count"] = float64(2) }},
		{"forged missing count", func(value *CandidateBundle) { value.Diagnostics["missing_database_count"] = float64(0) }},
		{"foreign missing ID", func(value *CandidateBundle) {
			value.Diagnostics["missing_database_ids"] = []any{strings.Repeat("9", 64)}
		}},
		{"duplicate missing ID", func(value *CandidateBundle) {
			id := value.CatalogEntries[1].DatabaseID
			value.Diagnostics["missing_database_ids"] = []any{id, id}
		}},
		{"forged complete coverage", func(value *CandidateBundle) { value.Diagnostics["database_coverage_status"] = "complete" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := phaseRegressionPartialBundle()
			test.mutate(&value)
			if err := ValidateBundle(&value); err == nil {
				t.Fatal("inconsistent coverage diagnostics were accepted")
			}
		})
	}
}

func TestValidateBundleRejectsUnknownOrDuplicateProfiles(t *testing.T) {
	unknown := phaseRegressionPartialBundle()
	unknown.Profiles[0].ID = "unknown-profile"
	if err := ValidateBundle(&unknown); err == nil {
		t.Fatal("unknown SQLCipher profile was accepted")
	}
	duplicate := phaseRegressionPartialBundle()
	duplicate.Profiles = append(duplicate.Profiles, duplicate.Profiles[0])
	if err := ValidateBundle(&duplicate); err == nil {
		t.Fatal("duplicate SQLCipher profile was accepted")
	}
}

func TestValidateBundleRejectsCatalogAndProfileCountOverflow(t *testing.T) {
	catalogOverflow := CandidateBundle{CatalogEntries: make([]CatalogEntry, maxCatalogEntries+1)}
	if err := ValidateBundle(&catalogOverflow); err == nil || !strings.Contains(err.Error(), "数量上限") {
		t.Fatalf("oversized catalog response was not rejected at its count boundary: %v", err)
	}
	profileOverflow := CandidateBundle{Profiles: make([]ProfileSummary, maxResponseProfiles+1)}
	if err := ValidateBundle(&profileOverflow); err == nil || !strings.Contains(err.Error(), "数量上限") {
		t.Fatalf("oversized profile registry was not rejected at its count boundary: %v", err)
	}
}

func TestLimitedBufferClearOverwritesSensitiveData(t *testing.T) {
	buffer := limitedBuffer{limit: 64}
	if _, err := buffer.Write([]byte("phase5-secret")); err != nil {
		t.Fatal(err)
	}
	oldBacking := buffer.Bytes()
	if len(oldBacking) == 0 {
		t.Fatal("test buffer did not retain the input")
	}
	if _, err := buffer.Write([]byte("-provider-output-that-forces-a-secure-growth")); err != nil {
		t.Fatal(err)
	}
	for index, value := range oldBacking {
		if value != 0 {
			t.Fatalf("superseded sensitive backing buffer was not overwritten at byte %d", index)
		}
	}
	backing := buffer.Bytes()

	buffer.Clear()
	if len(buffer.Bytes()) != 0 || buffer.over || buffer.sensitive != nil {
		t.Fatal("limited buffer retained state after Clear")
	}
	for index, value := range backing {
		if value != 0 {
			t.Fatalf("sensitive backing buffer was not overwritten at byte %d", index)
		}
	}
}
