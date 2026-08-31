package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
)

func TestOpaquePathIDDarwinSystemAliasIsStable(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS system aliases are Darwin-specific")
	}
	if opaquePathID("/var/folders/example/provider") != opaquePathID("/private/var/folders/example/provider") {
		t.Fatal("macOS system alias changed the anonymous checkpoint identity")
	}
}

func decodeDaemonTestRequest(connection net.Conn, request *acquisitionDaemonRequest) error {
	payload, err := bufio.NewReader(connection).ReadBytes('\n')
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	return decoder.Decode(request)
}

func TestExternalProviderDaemonLifecycle(t *testing.T) {
	providerPath := os.Getenv("V_LOCAL_TEST_PROVIDER")
	if providerPath == "" {
		t.Skip("V_LOCAL_TEST_PROVIDER is not set")
	}
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account")
	dbDir := filepath.Join(accountPath, "db_storage")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	page := make([]byte, 4096)
	copy(page, []byte("SQLite format 3\x00"))
	copy(page[100:], bytes.Repeat([]byte{0x42}, 128))
	if err := os.WriteFile(filepath.Join(dbDir, "message.db"), page, 0o600); err != nil {
		t.Fatal(err)
	}
	account := localplatform.Account{Name: "account", Path: accountPath, DBDir: dbDir}
	endpointPath, resumePath, err := acquisitionPaths(root, providerPath, accountPath)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := AcquireScopesWithRoot(context.Background(), providerPath, account, []string{"database"}, root)
	if err != nil {
		t.Fatal(err)
	}
	if diagnosticString(bundle.Diagnostics, "result_code") != "complete" || bundle.CatalogID == "" || len(bundle.DatabaseKeys) != 0 {
		t.Fatalf("unexpected plaintext-only acquisition: %+v", bundle)
	}
	if _, err := os.Stat(resumePath); !os.IsNotExist(err) {
		t.Fatalf("external daemon resume remained after finalize: %v", err)
	}
	if endpoint, err := loadAcquisitionEndpoint(endpointPath, providerPath); err == nil {
		_, _ = acquisitionDaemonExchange(context.Background(), endpoint, "shutdown", nil)
	}
}

func TestAcquireViaDaemonRunsPrepareObserveFinalizeAndRemovesResume(t *testing.T) {
	providerPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	providerPath, err = filepath.EvalSymlinks(providerPath)
	if err != nil {
		t.Fatal(err)
	}
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account")
	dbDir := filepath.Join(accountPath, "db_storage")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	account := localplatform.Account{Name: "account", Path: accountPath, DBDir: dbDir}
	endpointPath, resumePath, err := acquisitionPaths(root, providerPath, accountPath)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	endpoint := acquisitionDaemonEndpoint{
		SchemaVersion: acquisitionDaemonSchemaVersion, Address: listener.Addr().String(), Transport: "tcp4-development",
		Token: strings.Repeat("a", 64), PID: os.Getpid(), Version: "test",
		ProviderPath: providerPath, ClientPath: providerPath, StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := saveAcquisitionJSON(endpointPath, ".endpoint-test-*.tmp", endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAcquisitionEndpoint(endpointPath, providerPath); err != nil {
		t.Fatalf("test daemon endpoint does not satisfy production trust checks: %v", err)
	}
	originalStart := startAcquisitionDaemonProcess
	startAcquisitionDaemonProcess = func(_, _ string) (*exec.Cmd, error) {
		return nil, errors.New("test fixture unexpectedly attempted daemon fallback")
	}
	t.Cleanup(func() { startAcquisitionDaemonProcess = originalStart })
	serverDone := make(chan error, 1)
	var observedCatalogKeys []string
	go func() {
		for count := 0; count < 5; count++ {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				serverDone <- acceptErr
				return
			}
			var request acquisitionDaemonRequest
			decodeErr := decodeDaemonTestRequest(connection, &request)
			if decodeErr != nil {
				_ = connection.Close()
				serverDone <- decodeErr
				return
			}
			response := acquisitionDaemonResponse{SchemaVersion: acquisitionDaemonSchemaVersion}
			if request.Command == "ping" {
				response.Status = "ready"
			} else {
				observedCatalogKeys = append(observedCatalogKeys, request.Acquire.CatalogKey)
				result := CandidateBundle{
					Protocol: Protocol, RequestID: request.Acquire.RequestID, CatalogID: strings.Repeat("e", 64),
					CatalogEntries: []CatalogEntry{{
						DatabaseID: strings.Repeat("b", 64), RelativePath: "message.db",
						CanonicalFileID: strings.Repeat("c", 64), Size: 4096, MTimeNS: 1,
						FirstPageSHA256: strings.Repeat("d", 64), Classification: "encrypted_eligible", RequiredForKeyCoverage: true,
						ProfileID: "wcdb-v4-sha512-256000-r80",
					}},
					Profiles: []ProfileSummary{{
						ID: "wcdb-v4-sha512-256000-r80", CipherAlgorithm: "aes-256-cbc", KeySize: 32,
						PageSize: 4096, PlaintextHeaderSize: 16, ReserveSize: 80,
						KDFAlgorithm: "pbkdf2", KDFPRF: "hmac-sha512", KDFIterations: 256000,
						HMACAlgorithm: "hmac-sha512", HMACKDFAlgorithm: "pbkdf2", HMACKDFIterations: 2,
						HMACInputLayout: "page_without_salt_and_hmac_then_page_number", PageNumberEndian: "little-endian",
					}},
					Diagnostics: completeDiagnosticDefaults(map[string]any{
						"session_id": "session-test", "next_action": "none", "requested_scopes": []any{"database"},
						"database_target_status": "present", "database_coverage_status": "none", "media_coverage_status": "not_requested",
						"security_posture_status": "not_applicable", "shadow_route_status": "not_applicable", "route_priority": []any{}, "routes_attempted": []any{},
						"target_binding_status":  "unknown",
						"session_account_status": "unknown", "candidate_mode": "none", "blocking_reasons": []any{},
					}),
				}
				switch request.Acquire.Workflow.Operation {
				case "prepare":
					result.Diagnostics["workflow_status"] = "running"
					result.Diagnostics["result_code"] = "partial"
				case "observe":
					result.Diagnostics["workflow_status"] = "terminal"
					result.Diagnostics["result_code"] = "complete"
					result.Diagnostics["database_coverage_status"] = "complete"
					result.Diagnostics["target_binding_status"] = "hmac_verified"
					result.Diagnostics["candidate_mode"] = "per_database_enc_key"
				case "finalize":
					result.DatabaseKeys = map[string]string{"message.db": strings.Repeat("a", 64)}
					result.DatabaseProfiles = map[string]string{"message.db": "wcdb-v4-sha512-256000-r80"}
					result.Diagnostics["workflow_status"] = "terminal"
					result.Diagnostics["result_code"] = "complete"
					result.Diagnostics["database_coverage_status"] = "complete"
					result.Diagnostics["target_binding_status"] = "hmac_verified"
					result.Diagnostics["candidate_mode"] = "per_database_enc_key"
				}
				response.Result = &result
			}
			encodeErr := json.NewEncoder(connection).Encode(response)
			_ = connection.Close()
			if encodeErr != nil {
				serverDone <- encodeErr
				return
			}
		}
		serverDone <- nil
	}()
	if ping, err := acquisitionDaemonExchange(context.Background(), endpoint, "ping", nil); err != nil || ping.Status != "ready" {
		t.Fatalf("test daemon preflight failed: response=%+v err=%v", ping, err)
	}
	catalogKey, err := catalogKeyForPrivateRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	bundle, used, err := acquireViaDaemon(context.Background(), providerPath, account, []string{"database"}, root, "", catalogKey)
	if err != nil {
		t.Fatal(err)
	}
	if !used || bundle.CatalogID != strings.Repeat("e", 64) || len(bundle.DatabaseKeys) != 1 {
		t.Fatalf("unexpected daemon acquisition: used=%v bundle=%+v", used, bundle)
	}
	if _, err := os.Stat(resumePath); !os.IsNotExist(err) {
		t.Fatalf("resume metadata remained after finalize: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if len(observedCatalogKeys) != 3 {
		t.Fatalf("unexpected acquisition request count: %d", len(observedCatalogKeys))
	}
	for _, observed := range observedCatalogKeys {
		if observed != catalogKey {
			t.Fatal("daemon workflow did not reuse the machine catalog key")
		}
	}
}

func completeWithheldDaemonObservation() CandidateBundle {
	bundle := phaseRegressionPartialBundle()
	bundle.DatabaseKeys = nil
	bundle.DatabaseProfiles = nil
	bundle.DatabaseCredential = nil
	bundle.ImageKeys = nil
	bundle.Diagnostics["result_code"] = "complete"
	bundle.Diagnostics["workflow_status"] = "terminal"
	bundle.Diagnostics["requested_scopes"] = []any{"database", "media"}
	bundle.Diagnostics["database_coverage_status"] = "complete"
	bundle.Diagnostics["media_coverage_status"] = "complete"
	bundle.Diagnostics["next_action"] = "none"
	bundle.Diagnostics["matched_database_count"] = float64(2)
	bundle.Diagnostics["missing_database_count"] = float64(0)
	bundle.Diagnostics["missing_database_ids"] = []any{}
	bundle.Diagnostics["session_id"] = "session-1"
	bundle.Diagnostics["action_stage"] = "observe"
	return bundle
}

func TestDaemonObservationWithholdsSecretsButFinalStillRequiresThem(t *testing.T) {
	request := acquireRequest{Workflow: workflowRequest{
		Operation: "observe", SessionID: "session-1", ExpectedCatalogID: strings.Repeat("c", 64),
	}}
	bundle := completeWithheldDaemonObservation()
	account := localplatform.Account{Path: t.TempDir()}
	if err := validateDaemonObservationResponse(&bundle, []string{"database", "media"}, account, strings.Repeat("a", 64), request); err != nil {
		t.Fatalf("不含凭据但证明完整的 observe 响应被拒绝：%v", err)
	}
	withoutCatalogProfile := completeWithheldDaemonObservation()
	for index := range withoutCatalogProfile.CatalogEntries {
		withoutCatalogProfile.CatalogEntries[index].ProfileID = ""
	}
	if err := validateDaemonObservationResponse(&withoutCatalogProfile, []string{"database", "media"}, account, strings.Repeat("a", 64), request); err != nil {
		t.Fatalf("唯一已登记 profile 未能确定 observe 覆盖槽位：%v", err)
	}
	withoutProfileRegistry := withoutCatalogProfile
	withoutProfileRegistry.Profiles = nil
	if err := validateDaemonObservationResponse(&withoutProfileRegistry, []string{"database", "media"}, account, strings.Repeat("a", 64), request); err == nil {
		t.Fatal("缺少唯一 profile registry 的 observe 响应被接受")
	}
	if err := validateFinalAcquisitionResponse(&bundle, []string{"database", "media"}, account, strings.Repeat("a", 64)); err == nil {
		t.Fatal("不含凭据的 observe 响应被当成 finalize 结果接受")
	}

	withSecret := completeWithheldDaemonObservation()
	withSecret.ImageKeys = &ImageKeys{AES: strings.Repeat("0", 16)}
	if err := validateDaemonObservationResponse(&withSecret, []string{"database", "media"}, account, strings.Repeat("a", 64), request); err == nil {
		t.Fatal("意外携带凭据的 observe 响应被接受")
	}

	wrongSession := completeWithheldDaemonObservation()
	wrongSession.Diagnostics["session_id"] = "other-session"
	if err := validateDaemonObservationResponse(&wrongSession, []string{"database", "media"}, account, strings.Repeat("a", 64), request); err == nil {
		t.Fatal("未绑定当前 session 的 observe 响应被接受")
	}

	foreignMissing := completeWithheldDaemonObservation()
	foreignMissing.Diagnostics["matched_database_count"] = float64(1)
	foreignMissing.Diagnostics["missing_database_count"] = float64(1)
	foreignMissing.Diagnostics["missing_database_ids"] = []any{strings.Repeat("9", 64)}
	foreignMissing.Diagnostics["database_coverage_status"] = "partial"
	foreignMissing.Diagnostics["result_code"] = "partial"
	if err := validateDaemonObservationResponse(&foreignMissing, []string{"database", "media"}, account, strings.Repeat("a", 64), request); err == nil {
		t.Fatal("含外部 missing database ID 的 observe 响应被接受")
	}
}

func TestAcquisitionResumeRecoversBackupAndRemovalCleansBothFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.json")
	value := acquisitionResume{Version: acquisitionResumeVersion, SessionID: "session", CatalogID: "catalog"}
	if err := saveAcquisitionResume(path, value); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	var recovered acquisitionResume
	if err := loadAcquisitionResume(path, &recovered); err != nil || recovered.SessionID != value.SessionID {
		t.Fatalf("resume backup was not recovered: value=%+v err=%v", recovered, err)
	}
	removeAcquisitionResume(path)
	if _, err := os.Stat(path + ".old"); !os.IsNotExist(err) {
		t.Fatalf("resume backup remained after cleanup: %v", err)
	}
}

func TestAcquisitionStartupLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.lock")
	release, err := acquireAcquisitionStartupLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	secondRelease, err := acquireAcquisitionStartupLock(path)
	if secondRelease != nil {
		secondRelease()
	}
	if !errors.Is(err, errAcquisitionStartupBusy) {
		t.Fatalf("second startup lock should be busy, got %v", err)
	}
}

func TestAcquisitionEndpointRequiresAdvertisedProviderVersion(t *testing.T) {
	providerPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	providerPath, err = filepath.EvalSymlinks(providerPath)
	if err != nil {
		t.Fatal(err)
	}
	root := privateProviderTestRoot(t)
	endpointPath := filepath.Join(root, "endpoint.json")
	endpoint := acquisitionDaemonEndpoint{
		SchemaVersion: acquisitionDaemonSchemaVersion,
		Address:       "127.0.0.1:1",
		Transport:     "tcp4-development",
		Token:         strings.Repeat("a", 64),
		PID:           os.Getpid(),
		ProviderPath:  providerPath,
		ClientPath:    providerPath,
		StartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := saveAcquisitionJSON(endpointPath, ".endpoint-test-*.tmp", endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAcquisitionEndpoint(endpointPath, providerPath); err == nil {
		t.Fatal("endpoint without Provider version was accepted")
	}
}

func pendingActionExchange(t *testing.T, confirmedAction string) *actionReceipt {
	t.Helper()
	providerPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	providerPath, err = filepath.EvalSymlinks(providerPath)
	if err != nil {
		t.Fatal(err)
	}
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account")
	dbDir := filepath.Join(accountPath, "db_storage")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	account := localplatform.Account{Name: "account", Path: accountPath, DBDir: dbDir}
	endpointPath, resumePath, err := acquisitionPaths(root, providerPath, accountPath)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	endpoint := acquisitionDaemonEndpoint{
		SchemaVersion: acquisitionDaemonSchemaVersion, Address: listener.Addr().String(), Transport: "tcp4-development", Token: strings.Repeat("b", 64),
		PID: os.Getpid(), Version: "test", ProviderPath: providerPath, ClientPath: providerPath, StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := saveAcquisitionJSON(endpointPath, ".endpoint-test-*.tmp", endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAcquisitionEndpoint(endpointPath, providerPath); err != nil {
		t.Fatalf("test daemon endpoint does not satisfy production trust checks: %v", err)
	}
	resume := acquisitionResume{
		Version: acquisitionResumeVersion, ProviderPath: providerPath, EndpointStartedAt: endpoint.StartedAt,
		AccountDir: accountPath, DBDir: dbDir, Scopes: []string{"database"}, SessionID: "session-pending",
		CatalogID: strings.Repeat("e", 64), NextAction: "trigger_database", ProcessInstanceID: "process-before",
		Route: "route-server", ActionStage: "trigger_database", ExpiresAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
	}
	if err := saveAcquisitionResume(resumePath, resume); err != nil {
		t.Fatal(err)
	}
	receipts := make(chan *actionReceipt, 1)
	serverDone := make(chan error, 1)
	go func() {
		for count := 0; count < 2; count++ {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				serverDone <- acceptErr
				return
			}
			var request acquisitionDaemonRequest
			if err := decodeDaemonTestRequest(connection, &request); err != nil {
				_ = connection.Close()
				serverDone <- err
				return
			}
			response := acquisitionDaemonResponse{SchemaVersion: acquisitionDaemonSchemaVersion}
			if request.Command == "ping" {
				response.Status = "ready"
			} else {
				receipts <- request.Acquire.Workflow.ActionReceipt
				response.Result = &CandidateBundle{
					Protocol: Protocol, RequestID: request.Acquire.RequestID, CatalogID: resume.CatalogID,
					CatalogEntries: []CatalogEntry{{
						DatabaseID: strings.Repeat("b", 64), RelativePath: "message.db", CanonicalFileID: strings.Repeat("c", 64),
						Size: 4096, MTimeNS: 1, FirstPageSHA256: strings.Repeat("d", 64), Classification: "encrypted_eligible",
						RequiredForKeyCoverage: true, ProfileID: "wcdb-v4-sha512-256000-r80",
					}},
					Profiles: []ProfileSummary{phaseRegressionProfile()},
					Diagnostics: completeDiagnosticDefaults(map[string]any{
						"result_code": "action_required", "workflow_status": "waiting_action", "requested_scopes": []any{"database"},
						"database_target_status": "present", "database_coverage_status": "none", "media_coverage_status": "not_requested",
						"security_posture_status": "not_applicable", "shadow_route_status": "not_applicable", "route_priority": []any{}, "routes_attempted": []any{},
						"target_binding_status":  "unknown",
						"session_account_status": "unknown", "candidate_mode": "none", "blocking_reasons": []any{"hook_not_triggered"},
						"next_action": "trigger_database", "session_id": resume.SessionID,
						"process_instance_id": resume.ProcessInstanceID, "route_selected": "", "action_stage": resume.ActionStage,
					}),
				}
			}
			encodeErr := json.NewEncoder(connection).Encode(response)
			_ = connection.Close()
			if encodeErr != nil {
				serverDone <- encodeErr
				return
			}
		}
		serverDone <- nil
	}()
	var acquireErr error
	if confirmedAction == "" {
		_, acquireErr = AcquireScopesWithRoot(context.Background(), providerPath, account, []string{"database"}, root)
	} else {
		_, acquireErr = AcquireScopesWithRootAndAction(context.Background(), providerPath, account, []string{"database"}, root, confirmedAction)
	}
	if acquireErr == nil {
		t.Fatal("pending action should return a structured acquisition error")
	}
	var receipt *actionReceipt
	select {
	case receipt = <-receipts:
	case <-time.After(2 * time.Second):
		t.Fatal("test daemon did not receive the expected acquisition request")
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestPendingActionIsNotAutomaticallyConfirmed(t *testing.T) {
	if receipt := pendingActionExchange(t, ""); receipt != nil {
		t.Fatalf("CLI fabricated an action receipt: %+v", receipt)
	}
}

func TestPendingActionUsesOnlyExplicitMatchingConfirmation(t *testing.T) {
	receipt := pendingActionExchange(t, "trigger_database")
	if receipt == nil || !receipt.UserConfirmed || receipt.Action != "trigger_database" ||
		receipt.ProcessInstanceID != "process-before" || receipt.Route != "route-server" || receipt.ActionStage != "trigger_database" {
		t.Fatalf("explicit confirmation was not bound to the pending server state: %+v", receipt)
	}
}

func TestStopAndReportFinalizesPendingPartialWithoutActionReceipt(t *testing.T) {
	providerPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	providerPath, err = filepath.EvalSymlinks(providerPath)
	if err != nil {
		t.Fatal(err)
	}
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account")
	dbDir := filepath.Join(accountPath, "db_storage")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	account := localplatform.Account{Name: "account", Path: accountPath, DBDir: dbDir}
	endpointPath, resumePath, err := acquisitionPaths(root, providerPath, accountPath)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	endpoint := acquisitionDaemonEndpoint{
		SchemaVersion: acquisitionDaemonSchemaVersion, Address: listener.Addr().String(), Transport: "tcp4-development", Token: strings.Repeat("c", 64),
		PID: os.Getpid(), Version: "test", ProviderPath: providerPath, ClientPath: providerPath, StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := saveAcquisitionJSON(endpointPath, ".endpoint-test-*.tmp", endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAcquisitionEndpoint(endpointPath, providerPath); err != nil {
		t.Fatalf("test daemon endpoint does not satisfy production trust checks: %v", err)
	}
	resume := acquisitionResume{
		Version: acquisitionResumeVersion, ProviderPath: providerPath, EndpointStartedAt: endpoint.StartedAt,
		AccountDir: accountPath, DBDir: dbDir, Scopes: []string{"database"}, SessionID: "session-partial",
		CatalogID: strings.Repeat("e", 64), NextAction: "trigger_database", ProcessInstanceID: "process-before",
		Route: "route-server", ActionStage: "trigger_database", ExpiresAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
	}
	if err := saveAcquisitionResume(resumePath, resume); err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	requestSeen := make(chan *acquireRequest, 1)
	go func() {
		for count := 0; count < 2; count++ {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				serverDone <- acceptErr
				return
			}
			var request acquisitionDaemonRequest
			if err := decodeDaemonTestRequest(connection, &request); err != nil {
				_ = connection.Close()
				serverDone <- err
				return
			}
			response := acquisitionDaemonResponse{SchemaVersion: acquisitionDaemonSchemaVersion}
			if request.Command == "ping" {
				response.Status = "ready"
			} else {
				requestSeen <- request.Acquire
				response.Result = &CandidateBundle{
					Protocol: Protocol, RequestID: request.Acquire.RequestID, CatalogID: resume.CatalogID,
					CatalogEntries: []CatalogEntry{{
						DatabaseID: strings.Repeat("b", 64), RelativePath: "message.db", CanonicalFileID: strings.Repeat("c", 64),
						Size: 4096, MTimeNS: 1, FirstPageSHA256: strings.Repeat("d", 64),
						Classification: "encrypted_eligible", RequiredForKeyCoverage: true, ProfileID: "wcdb-v4-sha512-256000-r80",
					}},
					DatabaseKeys:     map[string]string{"message.db": strings.Repeat("a", 64)},
					DatabaseProfiles: map[string]string{"message.db": "wcdb-v4-sha512-256000-r80"},
					Profiles: []ProfileSummary{{
						ID: "wcdb-v4-sha512-256000-r80", CipherAlgorithm: "aes-256-cbc", KeySize: 32,
						PageSize: 4096, PlaintextHeaderSize: 16, ReserveSize: 80,
						KDFAlgorithm: "pbkdf2", KDFPRF: "hmac-sha512", KDFIterations: 256000,
						HMACAlgorithm: "hmac-sha512", HMACKDFAlgorithm: "pbkdf2", HMACKDFIterations: 2,
						HMACInputLayout: "page_without_salt_and_hmac_then_page_number", PageNumberEndian: "little-endian",
					}},
					Diagnostics: completeDiagnosticDefaults(map[string]any{
						"result_code": "partial", "workflow_status": "terminal", "requested_scopes": []any{"database"},
						"database_target_status": "present", "database_coverage_status": "partial", "media_coverage_status": "not_requested",
						"security_posture_status": "not_applicable", "shadow_route_status": "not_applicable", "route_priority": []any{}, "routes_attempted": []any{},
						"next_action": "none", "target_binding_status": "hmac_verified",
						"session_account_status": "unknown", "candidate_mode": "per_database_enc_key", "blocking_reasons": []any{},
					}),
				}
			}
			encodeErr := json.NewEncoder(connection).Encode(response)
			_ = connection.Close()
			if encodeErr != nil {
				serverDone <- encodeErr
				return
			}
		}
		serverDone <- nil
	}()
	bundle, err := AcquireScopesWithRootAndAction(context.Background(), providerPath, account, []string{"database"}, root, "stop_and_report")
	if err != nil {
		t.Fatal(err)
	}
	request := <-requestSeen
	if request.Workflow.Operation != "finalize" || request.Workflow.ActionReceipt != nil || len(bundle.DatabaseKeys) != 1 {
		t.Fatalf("stop_and_report was not a receipt-free partial finalize: request=%+v bundle=%+v", request.Workflow, bundle)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(resumePath); !os.IsNotExist(err) {
		t.Fatalf("partial finalize left resume metadata: %v", err)
	}
}

func TestSensitiveActionsAreNotDaemonResumable(t *testing.T) {
	for _, action := range []string{"approve_shadow_mode", "disable_sip", "reenable_sip"} {
		if resumableAcquisitionAction(action) {
			t.Fatalf("%s must stay outside the Phase 2 daemon session", action)
		}
	}
}

func TestExternalCheckpointPersistsProgressWithoutAuthorityOrPaths(t *testing.T) {
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account-secret-name")
	providerPath := filepath.Join(root, "provider-secret-name")
	if err := os.MkdirAll(accountPath, 0o700); err != nil {
		t.Fatal(err)
	}
	failure := &AcquisitionError{
		NextAction: "disable_sip", CatalogID: strings.Repeat("a", 64), RouteSelected: "private-route-value",
		SecurityPostureStatus: "sip_enabled_verified",
	}
	returned := reconcileExternalCheckpoint(root, providerPath, localplatform.Account{Path: accountPath}, []string{"database"}, CandidateBundle{}, failure)
	if !errors.Is(returned, failure) || failure.ExternalCheckpointStatus != "persisted" || failure.ExternalWorkflowID == "" {
		t.Fatalf("external workflow was not checkpointed: returned=%v failure=%+v", returned, failure)
	}
	values, err := ListExternalCheckpoints(root)
	if err != nil || len(values) != 1 {
		t.Fatalf("external checkpoint was not discoverable: values=%+v err=%v", values, err)
	}
	value := values[0]
	if value.PriorRequestedAction != "disable_sip" || value.RevalidationStage != "external_change_revalidation_required" || !value.MachineRevalidationRequired ||
		value.AccountID != opaquePathID(accountPath) || value.ProviderID != opaquePathID(providerPath) {
		t.Fatalf("external checkpoint semantics are incomplete: %+v", value)
	}
	path, err := externalCheckpointPath(root, accountPath)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(payload)
	for _, forbidden := range []string{accountPath, providerPath, "private-route-value", "action_receipt", "user_confirmed", "session_id", "authorization_carried_forward", `"next_action"`, `"stage"`} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("checkpoint persisted authority or sensitive process state %q: %s", forbidden, serialized)
		}
	}
}

func TestUnavailableShadowSIPFallbackProducesAValidatedCheckpointedAction(t *testing.T) {
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account")
	providerPath := filepath.Join(root, "provider")
	if err := os.MkdirAll(accountPath, 0o700); err != nil {
		t.Fatal(err)
	}
	bundle := validMediaOnlyBundle()
	bundle.ImageKeys = nil
	for name, value := range phase3DarwinDiagnosticDefaults(nil) {
		bundle.Diagnostics[name] = value
	}
	bundle.Diagnostics["result_code"] = "action_required"
	bundle.Diagnostics["workflow_status"] = "waiting_action"
	bundle.Diagnostics["media_coverage_status"] = "none"
	bundle.Diagnostics["security_posture_status"] = "sip_enabled_verified"
	bundle.Diagnostics["shadow_route_status"] = "unavailable_in_build"
	bundle.Diagnostics["route_priority"] = []any{"standard", "shadow", "sip_disabled"}
	bundle.Diagnostics["next_action"] = "disable_sip"
	bundle.Diagnostics["process_access_status"] = "denied"
	bundle.Diagnostics["process_access_error"] = "sip_enabled"
	bundle.Diagnostics["blocking_reasons"] = []any{"standard_route_unavailable", "shadow_route_unavailable_in_build"}
	if err := ValidateBundle(&bundle); err != nil {
		t.Fatalf("current production-disabled Shadow response failed protocol validation: %v", err)
	}
	stateErr := acquisitionStateError(bundle.Diagnostics)
	if stateErr == nil {
		t.Fatal("SIP fallback diagnostics did not become an external action error")
	}
	returned := reconcileExternalCheckpoint(root, providerPath, localplatform.Account{Path: accountPath}, []string{"media"}, CandidateBundle{}, stateErr)
	var acquisition *AcquisitionError
	if !errors.As(returned, &acquisition) || acquisition.Reason != "sip_required" ||
		acquisition.ShadowRouteStatus != "unavailable_in_build" || acquisition.ExternalCheckpointStatus != "persisted" {
		t.Fatalf("SIP fallback was not converted into a checkpointed external action: %+v", returned)
	}
	values, err := ListExternalCheckpoints(root)
	if err != nil || len(values) != 1 || values[0].PriorRequestedAction != "disable_sip" {
		t.Fatalf("SIP fallback checkpoint was not discoverable after handoff: values=%+v err=%v", values, err)
	}
}

func TestExternalCheckpointSurvivesStageTransitionButNeverBecomesAReceipt(t *testing.T) {
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account")
	providerPath := filepath.Join(root, "provider")
	if err := os.MkdirAll(accountPath, 0o700); err != nil {
		t.Fatal(err)
	}
	account := localplatform.Account{Path: accountPath}
	disable := &AcquisitionError{NextAction: "disable_sip", SecurityPostureStatus: "sip_enabled_verified"}
	_ = reconcileExternalCheckpoint(root, providerPath, account, []string{"database", "media"}, CandidateBundle{}, disable)
	first, err := ListExternalCheckpoints(root)
	if err != nil || len(first) != 1 {
		t.Fatalf("initial checkpoint missing: values=%+v err=%v", first, err)
	}
	restore := &AcquisitionError{NextAction: "reenable_sip", SecurityPostureStatus: "restoration_required"}
	_ = reconcileExternalCheckpoint(root, providerPath, account, []string{"database", "media"}, CandidateBundle{}, restore)
	second, err := ListExternalCheckpoints(root)
	if err != nil || len(second) != 1 {
		t.Fatalf("restoration checkpoint missing: values=%+v err=%v", second, err)
	}
	if second[0].WorkflowID != first[0].WorkflowID || second[0].CreatedAt != first[0].CreatedAt ||
		second[0].RevalidationStage != "security_restoration_revalidation_required" || second[0].PriorRequestedAction != "reenable_sip" {
		t.Fatalf("cross-reboot workflow continuity was lost: first=%+v second=%+v", first[0], second[0])
	}
	if err := reconcileExternalCheckpoint(root, providerPath, account, []string{"database", "media"}, CandidateBundle{}, nil); err != nil {
		t.Fatal(err)
	}
	if retained, err := ListExternalCheckpoints(root); err != nil || len(retained) != 1 {
		t.Fatalf("unrelated nil acquisition cleared restoration checkpoint: values=%+v err=%v", retained, err)
	}
	restored := CandidateBundle{Diagnostics: map[string]any{
		"platform": "darwin", "session_id": "fresh-session", "result_code": "complete", "workflow_status": "terminal",
		"requested_scopes": []any{"database", "media"}, "database_coverage_status": "complete", "media_coverage_status": "complete",
		"security_posture_status": "sip_enabled_verified", "next_action": "none",
	}}
	if err := reconcileExternalCheckpoint(root, providerPath, account, []string{"database", "media"}, restored, nil); err != nil {
		t.Fatal(err)
	}
	if final, err := ListExternalCheckpoints(root); err != nil || len(final) != 0 {
		t.Fatalf("verified fresh restoration session did not clear checkpoint: values=%+v err=%v", final, err)
	}
}

func TestCompletedSIPEnabledAcquisitionClearsUnperformedDisableCheckpoint(t *testing.T) {
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account")
	providerPath := filepath.Join(root, "provider")
	if err := os.MkdirAll(accountPath, 0o700); err != nil {
		t.Fatal(err)
	}
	account := localplatform.Account{Path: accountPath}
	disable := &AcquisitionError{NextAction: "disable_sip", SecurityPostureStatus: "sip_enabled_verified"}
	_ = reconcileExternalCheckpoint(root, providerPath, account, []string{"media"}, CandidateBundle{}, disable)

	partial := CandidateBundle{Diagnostics: map[string]any{
		"platform": "darwin", "requested_scopes": []any{"media"}, "database_coverage_status": "not_requested",
		"media_coverage_status": "none", "result_code": "partial", "workflow_status": "terminal",
		"security_posture_status": "sip_enabled_verified", "next_action": "none",
	}}
	if err := reconcileExternalCheckpoint(root, providerPath, account, []string{"media"}, partial, nil); err != nil {
		t.Fatal(err)
	}
	if retained, err := ListExternalCheckpoints(root); err != nil || len(retained) != 1 {
		t.Fatalf("partial acquisition cleared disable checkpoint: values=%+v err=%v", retained, err)
	}

	complete := CandidateBundle{Diagnostics: map[string]any{
		"platform": "darwin", "action_stage": "finalize", "requested_scopes": []any{"media"},
		"database_coverage_status": "not_requested", "media_coverage_status": "complete",
		"result_code": "complete", "workflow_status": "terminal",
		"security_posture_status": "sip_enabled_verified", "next_action": "none",
	}}
	if err := reconcileExternalCheckpoint(root, providerPath, account, []string{"media"}, complete, nil); err != nil {
		t.Fatal(err)
	}
	if remaining, err := ListExternalCheckpoints(root); err != nil || len(remaining) != 0 {
		t.Fatalf("verified normal success left an unperformed disable checkpoint: values=%+v err=%v", remaining, err)
	}
}

func TestExternalChangeCheckpointRejectsScopeDrift(t *testing.T) {
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account")
	providerPath := filepath.Join(root, "provider")
	if err := os.MkdirAll(accountPath, 0o700); err != nil {
		t.Fatal(err)
	}
	account := localplatform.Account{Path: accountPath}
	disable := &AcquisitionError{NextAction: "disable_sip", SecurityPostureStatus: "sip_enabled_verified"}
	_ = reconcileExternalCheckpoint(root, providerPath, account, []string{"database", "media"}, CandidateBundle{}, disable)
	if _, pending, err := pendingExternalChangeCheckpoint(root, providerPath, account, []string{"database", "media"}); err != nil || !pending {
		t.Fatalf("matching external checkpoint was not found: pending=%v err=%v", pending, err)
	}
	_, pending, err := pendingExternalChangeCheckpoint(root, providerPath, account, []string{"database"})
	var acquisition *AcquisitionError
	if pending || !errors.As(err, &acquisition) || acquisition.Reason != "external_workflow_scope_mismatch" ||
		acquisition.ExternalCheckpointStatus != "persisted" || acquisition.ExternalWorkflowID == "" {
		t.Fatalf("scope drift did not fail closed with checkpoint evidence: pending=%v err=%+v", pending, err)
	}
	if remaining, listErr := ListExternalCheckpoints(root); listErr != nil || len(remaining) != 1 {
		t.Fatalf("scope drift modified the existing checkpoint: values=%+v err=%v", remaining, listErr)
	}
}

func TestPostureOnlyRevalidationClearsRestorationCheckpointWithoutAcquisition(t *testing.T) {
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account")
	providerPath := filepath.Join(root, "provider")
	if err := os.MkdirAll(accountPath, 0o700); err != nil {
		t.Fatal(err)
	}
	account := localplatform.Account{Path: accountPath}
	disable := &AcquisitionError{NextAction: "disable_sip", SecurityPostureStatus: "sip_enabled_verified"}
	_ = reconcileExternalCheckpoint(root, providerPath, account, []string{"database"}, CandidateBundle{}, disable)
	restore := &AcquisitionError{NextAction: "reenable_sip", SecurityPostureStatus: "restoration_required"}
	_ = reconcileExternalCheckpoint(root, providerPath, account, []string{"database"}, CandidateBundle{}, restore)
	checkpoint, pending, err := pendingSecurityRestorationCheckpoint(root, providerPath, account)
	if err != nil || !pending || checkpoint.PriorRequestedAction != "reenable_sip" {
		t.Fatalf("restoration checkpoint was not selected for posture-only validation: checkpoint=%+v pending=%v err=%v", checkpoint, pending, err)
	}
	bundle := CandidateBundle{Diagnostics: map[string]any{
		"platform": "darwin", "action_stage": "security_posture_revalidation", "requested_scopes": []any{"database"},
		"database_coverage_status": "not_requested", "media_coverage_status": "not_requested",
		"result_code": "complete", "workflow_status": "terminal", "security_posture_status": "sip_enabled_verified", "next_action": "none",
	}}
	if err := reconcileExternalCheckpoint(root, providerPath, account, checkpoint.Scopes, bundle, nil); err != nil {
		t.Fatal(err)
	}
	if remaining, err := ListExternalCheckpoints(root); err != nil || len(remaining) != 0 {
		t.Fatalf("posture-only verification did not clear restoration checkpoint: remaining=%+v err=%v", remaining, err)
	}
}

func TestExternalCheckpointRecoversFromAtomicBackupAfterInterruptedPublish(t *testing.T) {
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account")
	if err := os.MkdirAll(accountPath, 0o700); err != nil {
		t.Fatal(err)
	}
	failure := &AcquisitionError{NextAction: "disable_sip", SecurityPostureStatus: "sip_enabled_verified"}
	_ = reconcileExternalCheckpoint(root, filepath.Join(root, "provider"), localplatform.Account{Path: accountPath}, []string{"database"}, CandidateBundle{}, failure)
	path, err := externalCheckpointPath(root, accountPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	values, err := ListExternalCheckpoints(root)
	if err != nil || len(values) != 1 || values[0].WorkflowID != failure.ExternalWorkflowID {
		t.Fatalf("interrupted checkpoint publish did not recover from backup: values=%+v err=%v", values, err)
	}
}

func TestVerifiedBundlePendingSIPRestorationIsCheckpointedWithoutBecomingAnError(t *testing.T) {
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account")
	if err := os.MkdirAll(accountPath, 0o700); err != nil {
		t.Fatal(err)
	}
	bundle := CandidateBundle{CatalogID: strings.Repeat("a", 64), Diagnostics: map[string]any{
		"result_code": "action_required", "workflow_status": "waiting_action", "requested_scopes": []any{"database"},
		"database_coverage_status": "complete", "media_coverage_status": "not_requested",
		"security_posture_status": "restoration_required", "next_action": "reenable_sip",
	}}
	if err := reconcileExternalCheckpoint(root, filepath.Join(root, "provider"), localplatform.Account{Path: accountPath}, []string{"database"}, bundle, nil); err != nil {
		t.Fatalf("verified credentials pending restoration became a command failure: %v", err)
	}
	if bundle.Diagnostics["external_checkpoint_status"] != "persisted" || bundle.Diagnostics["external_workflow_id"] == "" {
		t.Fatalf("restoration handoff was not attached to the successful bundle: %+v", bundle.Diagnostics)
	}
}

func TestExternalCheckpointRejectsExpiredOrAuthorityBearingState(t *testing.T) {
	root := privateProviderTestRoot(t)
	path := filepath.Join(root, "external-aaaaaaaaaaaaaaaa.checkpoint.json")
	expired := ExternalCheckpointStatus{
		Version: externalCheckpointVersion, WorkflowID: strings.Repeat("a", 32), ProviderID: strings.Repeat("b", 16),
		AccountID: strings.Repeat("c", 16), Scopes: []string{"database"}, RevalidationStage: "external_change_revalidation_required",
		PriorRequestedAction: "disable_sip", LastSecurityPostureStatus: "sip_enabled_verified",
		CreatedAt: time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339Nano),
		ExpiresAt: time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano), MachineRevalidationRequired: true,
	}
	payload, _ := json.Marshal(expired)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	// 过期是 7 天生命周期的正常终点：记录必须从结果中消失，但不能让整次列举失败，
	// 否则一个账号的过期记录会挡住其余账号仍然有效的 handoff。
	values, err := ListExternalCheckpoints(root)
	if err != nil {
		t.Fatalf("过期的跨重启 checkpoint 让整次列举失败：%v", err)
	}
	if len(values) != 0 {
		t.Fatalf("expired cross-reboot checkpoint was trusted: %+v", values)
	}
	expired.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	expired.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	payload, _ = json.Marshal(expired)
	payload = bytes.TrimSuffix(payload, []byte("}"))
	payload = append(payload, []byte(`,"user_confirmed":true}`)...)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListExternalCheckpoints(root); err == nil {
		t.Fatal("authority-bearing cross-reboot checkpoint was trusted")
	}
}

func TestListExternalCheckpointsSkipsExpiredWithoutHidingValidHandoffs(t *testing.T) {
	root := privateProviderTestRoot(t)
	base := ExternalCheckpointStatus{
		Version: externalCheckpointVersion, WorkflowID: strings.Repeat("a", 32), ProviderID: strings.Repeat("b", 16),
		AccountID: strings.Repeat("c", 16), Scopes: []string{"database"},
		RevalidationStage: "external_change_revalidation_required", PriorRequestedAction: "disable_sip",
		LastSecurityPostureStatus: "sip_enabled_verified", MachineRevalidationRequired: true,
	}
	write := func(name string, value ExternalCheckpointStatus) {
		t.Helper()
		payload, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := os.WriteFile(filepath.Join(root, name), payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	expired := base
	expired.CreatedAt = time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	expired.ExpiresAt = time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)
	write("external-"+strings.Repeat("a", 16)+".checkpoint.json", expired)

	pending := base
	pending.WorkflowID = strings.Repeat("d", 32)
	pending.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	pending.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	write("external-"+strings.Repeat("b", 16)+".checkpoint.json", pending)

	values, err := ListExternalCheckpoints(root)
	if err != nil {
		t.Fatalf("另一个账号的过期记录挡住了整次列举：%v", err)
	}
	if len(values) != 1 || values[0].WorkflowID != pending.WorkflowID {
		t.Fatalf("仍然有效的跨重启 handoff 没有被列出：%+v", values)
	}
}

func TestClearExternalCheckpointsIsNarrowAndDoesNotRequireDecoding(t *testing.T) {
	root := privateProviderTestRoot(t)
	for name, payload := range map[string]string{
		"external-aaaaaaaaaaaaaaaa.checkpoint.json":     `{"authorization":true}`,
		"external-aaaaaaaaaaaaaaaa.checkpoint.json.old": `not-json`,
		"provider-keep.resume.json":                     `keep`,
		"provider-keep.json":                            `keep`,
		"external-not-an-id.checkpoint.json":            `keep`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := ClearExternalCheckpoints(root)
	if err != nil || removed != 2 {
		t.Fatalf("narrow checkpoint cleanup failed: removed=%d err=%v", removed, err)
	}
	for _, name := range []string{"provider-keep.resume.json", "provider-keep.json", "external-not-an-id.checkpoint.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("checkpoint cleanup touched unrelated acquisition state %s: %v", name, err)
		}
	}
}

func TestActionConfirmationErrorPreservesPendingServerState(t *testing.T) {
	resume := acquisitionResume{
		NextAction: "restart_wechat", SessionID: "session", CatalogID: "catalog",
		ProcessInstanceID: "process", Route: "route", ActionStage: "restart_wechat",
	}
	err := actionConfirmationError(resume, "confirmed_action_mismatch")
	if err.Reason != "action_confirmation_mismatch" || err.NextAction != resume.NextAction ||
		err.SessionID != resume.SessionID || err.ProcessInstanceID != resume.ProcessInstanceID || err.BlockingReasons[0] != "confirmed_action_mismatch" {
		t.Fatalf("confirmation error lost pending state: %+v", err)
	}
}

func TestCancelAcquisitionRemovesInvalidOrDeadResume(t *testing.T) {
	providerPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	providerPath, err = filepath.EvalSymlinks(providerPath)
	if err != nil {
		t.Fatal(err)
	}
	root := privateProviderTestRoot(t)
	accountPath := filepath.Join(root, "account")
	dbDir := filepath.Join(accountPath, "db_storage")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	account := localplatform.Account{Name: "account", Path: accountPath, DBDir: dbDir}
	_, resumePath, err := acquisitionPaths(root, providerPath, accountPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveAcquisitionResume(resumePath, acquisitionResume{
		Version: acquisitionResumeVersion, ProviderPath: providerPath, AccountDir: accountPath, DBDir: dbDir,
		Scopes: []string{"database"}, SessionID: "dead-session", CatalogID: "catalog", EndpointStartedAt: "dead",
		ExpiresAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	cancelled, err := CancelAcquisition(context.Background(), providerPath, account, root)
	if err != nil || !cancelled {
		t.Fatalf("dead resume was not safely cleaned: cancelled=%v err=%v", cancelled, err)
	}
	if _, err := os.Stat(resumePath); !os.IsNotExist(err) {
		t.Fatalf("cancel left resume metadata: %v", err)
	}
}
