package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
)

type shadowClock struct{ now uint64 }

func (value *shadowClock) NowNS() (uint64, error) { return value.now, nil }

func shadowVectors(t *testing.T) contract.GoldenVectors {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "testdata", "shadow-contract-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors contract.GoldenVectors
	if err := contract.DecodeStrict(payload, &vectors); err != nil || vectors.Validate() != nil {
		t.Fatalf("invalid vectors: %v", err)
	}
	return vectors
}

func TestCandidateBundlePreservesTypedShadowIntegersAcrossTransientMarshal(t *testing.T) {
	vectors := shadowVectors(t)
	result := vectors.ReadyResult
	receipt := *result.Receipt
	receipt.Resources = append([]contract.ResourceBinding(nil), receipt.Resources...)
	receipt.Resources[0].Device = 9_007_199_254_740_993
	result.Receipt = &receipt
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(fmt.Sprintf(`{"protocol":%q,"request_id":%q,"diagnostics":{"shadow_attempt":%s}}`,
		Protocol, result.RequestID, resultJSON))
	var bundle CandidateBundle
	if err := json.Unmarshal(payload, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.ShadowAttempt == nil || bundle.ShadowAttempt.Receipt.Resources[0].Device != 9_007_199_254_740_993 {
		t.Fatal("typed Shadow device identity lost uint64 precision")
	}
	roundTrip, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var decoded CandidateBundle
	if err := json.Unmarshal(roundTrip, &decoded); err != nil ||
		decoded.ShadowAttempt.Receipt.Resources[0].Device != 9_007_199_254_740_993 {
		t.Fatalf("transient candidate marshal lost typed identity: err=%v", err)
	}
	unknown := bytes.Replace(payload, []byte(`{"protocol":`), []byte(`{"unknown":true,"protocol":`), 1)
	if err := json.Unmarshal(unknown, &bundle); err == nil {
		t.Fatal("custom Shadow decoder bypassed top-level unknown-field rejection")
	}
}

func TestShadowOptionsDigestBindsCanonicalAccountScopesAndMachineCatalogKey(t *testing.T) {
	account := localplatform.Account{Name: "test", Path: "/account", DBDir: "/account/db"}
	key := strings.Repeat("c", 64)
	first, err := shadowOptionsDigest(account, []string{"database", "media"}, key)
	second, err2 := shadowOptionsDigest(account, []string{"database", "media"}, key)
	if err != nil || err2 != nil || len(first) != 64 || first != second {
		t.Fatalf("stable options binding failed: %q err=%v err2=%v", first, err, err2)
	}
	changedAccount, _ := shadowOptionsDigest(localplatform.Account{Name: "test", Path: "/other", DBDir: "/other/db"}, []string{"database", "media"}, key)
	changedScopes, _ := shadowOptionsDigest(account, []string{"database"}, key)
	changedKey, _ := shadowOptionsDigest(account, []string{"database", "media"}, strings.Repeat("d", 64))
	if first == changedAccount || first == changedScopes || first == changedKey {
		t.Fatal("Shadow options digest did not bind every command input")
	}
}

func TestShadowClientSeparatesTransientEvidenceFromMinimalCredential(t *testing.T) {
	vectors := shadowVectors(t)
	clock := &shadowClock{now: vectors.ExecuteRequest.Deadline.T0NS + 1}
	response := CandidateBundle{
		Protocol: Protocol, RequestID: vectors.ExecuteRequest.RequestID,
		DatabaseKeys:  map[string]string{"message.db": strings.Repeat("a", 64)},
		Diagnostics:   map[string]any{"requested_scopes": []any{"database"}},
		ShadowAttempt: &vectors.ReadyResult,
	}
	client := &ShadowClient{
		path: "/synthetic/provider", account: localplatform.Account{Name: "test", Path: "/account", DBDir: "/account/db"},
		scopes: []string{"database"}, catalogKey: strings.Repeat("c", 64), clock: clock,
		exchange: func(_ context.Context, _ string, request acquireRequest) (CandidateBundle, error) {
			if request.Workflow.Shadow == nil || request.Workflow.Shadow.Deadline.T0NS != vectors.ExecuteRequest.Deadline.T0NS ||
				request.DeadlineMS <= 0 || request.DeadlineMS > 120_000 {
				t.Fatalf("outer Shadow request reset or lost its deadline: %+v", request)
			}
			return response, nil
		},
	}
	result, credential, err := client.Execute(context.Background(), vectors.ExecuteRequest)
	if err != nil || result.Status != "ready" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	defer clearSensitiveBytes(credential.Candidate)
	validator := ShadowCandidateValidator{
		Account: localplatform.Account{Name: "test", Path: "/account", DBDir: "/account/db"},
		Scopes:  []string{"database"}, CatalogKey: strings.Repeat("c", 64),
		ValidateSelected: func(context.Context, *CandidateBundle) error { return nil },
	}
	minimalPayload, err := validator.ValidateAndDerive(context.Background(), vectors.ExecuteRequest, result, credential.Candidate)
	if err != nil {
		t.Fatal(err)
	}
	defer clearSensitiveBytes(minimalPayload)
	if !bytes.Contains(credential.Candidate, []byte(`"diagnostics"`)) ||
		bytes.Contains(minimalPayload, []byte(`"diagnostics"`)) ||
		bytes.Contains(minimalPayload, []byte(`"shadow_attempt"`)) {
		t.Fatalf("candidate/minimal boundary failed: candidate=%s minimal=%s", credential.Candidate, minimalPayload)
	}
	var minimal minimalShadowCredential
	if err := json.Unmarshal(minimalPayload, &minimal); err != nil || minimal.DatabaseKeys["message.db"] != strings.Repeat("a", 64) {
		t.Fatalf("minimal credential is incomplete: %+v err=%v", minimal, err)
	}
}

func TestShadowQualificationRejectsEveryAcquisitionPayloadField(t *testing.T) {
	vectors := shadowVectors(t)
	qualified := contract.Result{
		Version: contract.Version, RequestID: vectors.QualifyRequest.RequestID,
		Status: "qualified", ErrorCode: contract.ErrorNone, Qualification: &vectors.Qualification,
	}
	if err := qualified.Validate(); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*CandidateBundle){
		"catalog_id":          func(value *CandidateBundle) { value.CatalogID = strings.Repeat("a", 64) },
		"catalog_entries":     func(value *CandidateBundle) { value.CatalogEntries = []CatalogEntry{{}} },
		"database_keys":       func(value *CandidateBundle) { value.DatabaseKeys = map[string]string{"db": "key"} },
		"database_profiles":   func(value *CandidateBundle) { value.DatabaseProfiles = map[string]string{"db": "profile"} },
		"database_credential": func(value *CandidateBundle) { value.DatabaseCredential = &DatabaseCredential{} },
		"image_keys":          func(value *CandidateBundle) { value.ImageKeys = &ImageKeys{} },
		"profiles":            func(value *CandidateBundle) { value.Profiles = []ProfileSummary{{}} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			response := CandidateBundle{
				Protocol: Protocol, RequestID: vectors.QualifyRequest.RequestID,
				ShadowAttempt: &qualified,
			}
			mutate(&response)
			client := &ShadowClient{
				path: "/synthetic/provider", clock: &shadowClock{now: 1},
				exchange: func(context.Context, string, acquireRequest) (CandidateBundle, error) { return response, nil },
			}
			if _, err := client.Qualify(context.Background(), vectors.QualifyRequest); err == nil {
				t.Fatal("qualification accepted acquisition payload data")
			}
		})
	}
}

func TestShadowCandidateValidatorRequiresTypedReadyEvidenceScopesAndSelectedInput(t *testing.T) {
	vectors := shadowVectors(t)
	bundle := CandidateBundle{
		Protocol: Protocol, RequestID: vectors.ReadyResult.RequestID,
		DatabaseKeys: map[string]string{"message.db": strings.ToUpper(strings.Repeat("ab", 32))},
		Diagnostics:  map[string]any{"requested_scopes": []any{"database"}}, ShadowAttempt: &vectors.ReadyResult,
	}
	payload, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	validator := ShadowCandidateValidator{
		Account: localplatform.Account{Name: "test", Path: "/account", DBDir: "/account/db"},
		Scopes:  []string{"database"}, CatalogKey: strings.Repeat("c", 64),
		ValidateSelected: func(_ context.Context, candidate *CandidateBundle) error {
			called = true
			if candidate.DatabaseKeys["message.db"] != strings.Repeat("ab", 32) {
				return fmt.Errorf("candidate was not normalized")
			}
			return nil
		},
	}
	minimal, err := validator.ValidateAndDerive(context.Background(), vectors.ExecuteRequest, vectors.ReadyResult, payload)
	defer clearSensitiveBytes(minimal)
	if err != nil || !called || len(minimal) == 0 {
		t.Fatalf("selected candidate validation err=%v called=%v", err, called)
	}
	validator.ValidateSelected = nil
	if _, err := validator.ValidateAndDerive(context.Background(), vectors.ExecuteRequest, vectors.ReadyResult, payload); err == nil {
		t.Fatal("Shadow candidate was accepted without selected-input verification")
	}
	validator.ValidateSelected = func(context.Context, *CandidateBundle) error { return nil }
	tampered := bundle
	tamperedResult := vectors.ReadyResult
	tamperedReceipt := *tamperedResult.Receipt
	tamperedReceipt.BuildSetDigest = strings.Repeat("f", 64)
	tamperedResult.Receipt = &tamperedReceipt
	tampered.ShadowAttempt = &tamperedResult
	tamperedPayload, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validator.ValidateAndDerive(
		context.Background(), vectors.ExecuteRequest, vectors.ReadyResult, tamperedPayload,
	); err == nil {
		t.Fatal("candidate from a different Shadow result was accepted")
	}
}
