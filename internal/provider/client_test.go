package provider

import "testing"

func TestValidateBundleNormalizesDatabaseKey(t *testing.T) {
	bundle := CandidateBundle{DatabaseKeys: map[string]string{"contact.db": "AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA"}}
	if err := ValidateBundle(&bundle); err != nil {
		t.Fatal(err)
	}
	if got := bundle.DatabaseKeys["contact.db"]; len(got) != 64 || got[:2] != "aa" {
		t.Fatalf("候选密钥未规范化：%q", got)
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

func TestAcquisitionErrorUsesOnlySafeDiagnosticEnums(t *testing.T) {
	err := acquisitionError(map[string]any{
		"platform": "darwin", "process_access_status": "denied",
		"process_access_error": "task_for_pid_denied", "helper_status": "not_installed",
		"database_keys": "must-not-be-read",
	})
	if err.Reason != "process_access_denied" || err.Platform != "darwin" || err.HelperStatus != "not_installed" {
		t.Fatalf("unexpected acquisition error: %+v", err)
	}
}

func TestAcquisitionErrorReportsSIPRequirement(t *testing.T) {
	err := acquisitionError(map[string]any{
		"platform": "darwin", "process_access_status": "denied",
		"process_access_error": "sip_enabled", "helper_status": "sip_enabled",
	})
	if err.Reason != "sip_required" {
		t.Fatalf("unexpected SIP reason: %+v", err)
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
