package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
)

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

func TestAcquireScopesSendsDatabaseOnlyRequest(t *testing.T) {
	providerPath := filepath.Join(t.TempDir(), "provider")
	const script = `#!/bin/sh
payload=$(cat)
case "$payload" in *'"scopes":["database"]'*) ;; *) exit 42 ;; esac
request_id=$(printf '%s' "$payload" | sed -n 's/.*"request_id":"\([0-9a-f]*\)".*/\1/p')
printf '{"protocol":"v-local-key-provider/v2","request_id":"%s","database_keys":{"*":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}\n' "$request_id"
`
	if err := os.WriteFile(providerPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	bundle, err := AcquireScopes(context.Background(), providerPath, localplatform.Account{Path: "/tmp/account", DBDir: "/tmp/db"}, []string{"database"})
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
