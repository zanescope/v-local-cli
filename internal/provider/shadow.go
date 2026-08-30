package provider

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
	clockmodel "github.com/zanescope/v-local-cli/internal/shadowclock"
	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
	ownermodel "github.com/zanescope/v-local-cli/internal/shadowowner"
)

type shadowExchange func(context.Context, string, acquireRequest) (CandidateBundle, error)

type ShadowClient struct {
	path       string
	account    localplatform.Account
	scopes     []string
	catalogKey string
	options    string
	clock      clockmodel.Clock
	exchange   shadowExchange
}

func shadowOptionsDigest(account localplatform.Account, scopes []string, catalogKey string) (string, error) {
	key, err := hex.DecodeString(catalogKey)
	if err != nil || len(key) != 32 {
		clearSensitiveBytes(key)
		return "", errors.New("Shadow options binding key is invalid")
	}
	defer clearSensitiveBytes(key)
	return credentialCatalogHMAC(key, "v-local-shadow-options/v1", account.Path, account.DBDir, strings.Join(scopes, "\x00")), nil
}

func NewShadowClient(explicit string, account localplatform.Account, scopes []string, privateRoot string, clock clockmodel.Clock) (*ShadowClient, error) {
	if clock == nil {
		return nil, errors.New("Shadow client clock is missing")
	}
	normalized, err := normalizeRequestedScopes(scopes)
	if err != nil {
		return nil, err
	}
	requestAccount, err := canonicalAcquisitionRequestAccount(account)
	if err != nil {
		return nil, err
	}
	path, source := Resolve(explicit)
	if path == "" {
		if source == "override_rejected" || strings.HasPrefix(source, "untrusted_") {
			return nil, ErrComponentUntrusted
		}
		return nil, ErrComponentMissing
	}
	catalogKey, err := catalogKeyForPrivateRoot(privateRoot)
	if err != nil {
		return nil, err
	}
	options, err := shadowOptionsDigest(requestAccount, normalized, catalogKey)
	if err != nil {
		return nil, err
	}
	return &ShadowClient{
		path: path, account: requestAccount, scopes: normalized, catalogKey: catalogKey,
		options: options, clock: clock, exchange: executeShadowDaemonRequest,
	}, nil
}

func (value *ShadowClient) OptionsDigest() string {
	if value == nil {
		return ""
	}
	return value.options
}

func executeShadowDaemonRequest(parent context.Context, path string, request acquireRequest) (CandidateBundle, error) {
	if _, err := validateProviderExecutableTrust(path); err != nil {
		return CandidateBundle{}, ErrComponentUntrusted
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return CandidateBundle{}, errors.New("could not encode Shadow Provider request")
	}
	payload = append(payload, '\n')
	markSensitiveBytes(payload)
	defer clearSensitiveBytes(payload)
	command := exec.CommandContext(parent, path, "daemon")
	command.Dir = filepath.Dir(path)
	configureProviderCommandEnvironment(command)
	command.Stdin = bytes.NewReader(payload)
	stdout := &limitedBuffer{limit: maxResponseBytes}
	stderr := &limitedBuffer{limit: 16 * 1024}
	defer stdout.Clear()
	defer stderr.Clear()
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if parent.Err() != nil {
			return CandidateBundle{}, errors.New("Shadow Provider deadline exhausted")
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return CandidateBundle{}, &ExecutionError{Stage: "shadow_daemon", ExitCode: exitErr.ExitCode()}
		}
		return CandidateBundle{}, &ExecutionError{Stage: "shadow_daemon", ExitCode: -1}
	}
	if stdout.over || len(stdout.Bytes()) == 0 {
		return CandidateBundle{}, &ProtocolContractError{Cause: errors.New("Shadow Provider response is empty or oversized"), Stage: "shadow_response"}
	}
	var response CandidateBundle
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return CandidateBundle{}, &ProtocolContractError{Cause: errors.New("Shadow Provider response is invalid"), Stage: "shadow_response"}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return CandidateBundle{}, &ProtocolContractError{Cause: errors.New("Shadow Provider response has trailing data"), Stage: "shadow_response"}
	}
	return response, nil
}

func (value *ShadowClient) outerRequest(inner contract.Request, deadlineMS int64) acquireRequest {
	return acquireRequest{
		Protocol: Protocol, RequestID: inner.RequestID, Action: "acquire", CatalogKey: value.catalogKey,
		AccountDir: value.account.Path, DBDir: value.account.DBDir, Scopes: append([]string(nil), value.scopes...),
		DeadlineMS: deadlineMS, Workflow: workflowRequest{Operation: "shadow", Shadow: &inner},
	}
}

func (value *ShadowClient) call(parent context.Context, request contract.Request) (CandidateBundle, error) {
	if value == nil || value.clock == nil || value.exchange == nil || parent == nil || request.Validate() != nil {
		return CandidateBundle{}, errors.New("Shadow client request is invalid")
	}
	budget := 30 * time.Second
	deadlineMS := budget.Milliseconds()
	if request.Deadline != nil {
		remaining, err := clockmodel.Remaining(value.clock, request.Deadline.ReturnNS)
		if err != nil || remaining <= 0 {
			return CandidateBundle{}, errors.New("Shadow client return deadline exhausted")
		}
		budget = remaining
		deadlineMS = remaining.Milliseconds()
		if deadlineMS < 1 {
			deadlineMS = 1
		}
		if deadlineMS > 120_000 {
			deadlineMS = 120_000
		}
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	response, err := value.exchange(ctx, value.path, value.outerRequest(request, deadlineMS))
	if err != nil {
		return CandidateBundle{}, err
	}
	if response.Protocol != Protocol || response.RequestID != request.RequestID || response.ShadowAttempt == nil ||
		response.ShadowAttempt.RequestID != request.RequestID {
		return CandidateBundle{}, &ProtocolContractError{Cause: errors.New("Shadow response binding is invalid"), Stage: "shadow_response"}
	}
	return response, nil
}

func (value *ShadowClient) Qualify(ctx context.Context, request contract.Request) (contract.Result, error) {
	response, err := value.call(ctx, request)
	if err != nil {
		return contract.Result{}, err
	}
	if response.ShadowAttempt.Status != "qualified" || len(response.DatabaseKeys) != 0 ||
		response.DatabaseCredential != nil || response.ImageKeys != nil {
		return contract.Result{}, &ProtocolContractError{Cause: errors.New("Shadow qualification carried credential data"), Stage: "shadow_qualification"}
	}
	return *response.ShadowAttempt, nil
}

type minimalShadowCredential struct {
	CatalogID          string              `json:"catalog_id,omitempty"`
	DatabaseKeys       map[string]string   `json:"database_keys,omitempty"`
	DatabaseProfiles   map[string]string   `json:"database_profiles,omitempty"`
	DatabaseCredential *DatabaseCredential `json:"database_credential,omitempty"`
	ImageKeys          *ImageKeys          `json:"image_keys,omitempty"`
	Profiles           []ProfileSummary    `json:"profiles,omitempty"`
}

func marshalMinimalShadowCredential(response CandidateBundle) ([]byte, error) {
	minimal := minimalShadowCredential{
		CatalogID: response.CatalogID, DatabaseKeys: response.DatabaseKeys,
		DatabaseProfiles: response.DatabaseProfiles, DatabaseCredential: response.DatabaseCredential,
		ImageKeys: response.ImageKeys, Profiles: response.Profiles,
	}
	if minimal.DatabaseCredential != nil {
		minimal.DatabaseKeys = nil
		minimal.DatabaseProfiles = nil
	}
	if len(minimal.DatabaseKeys) == 0 && minimal.DatabaseCredential == nil && minimal.ImageKeys == nil {
		return nil, errors.New("Shadow response has no minimal credential")
	}
	return json.Marshal(minimal)
}

func (value *ShadowClient) Execute(ctx context.Context, request contract.Request) (contract.Result, ownermodel.Credential, error) {
	response, err := value.call(ctx, request)
	if err != nil {
		return contract.Result{}, ownermodel.Credential{}, err
	}
	result := *response.ShadowAttempt
	if result.Status != "ready" {
		return result, ownermodel.Credential{}, nil
	}
	candidate, err := json.Marshal(response)
	if err != nil {
		return contract.Result{}, ownermodel.Credential{}, errors.New("could not retain transient Shadow candidate evidence")
	}
	return result, ownermodel.Credential{Candidate: candidate}, nil
}

type ShadowCandidateValidator struct {
	Account          localplatform.Account
	CatalogKey       string
	Scopes           []string
	ValidateSelected func(context.Context, *CandidateBundle) error
}

func (value ShadowCandidateValidator) ValidateAndDerive(
	ctx context.Context,
	request contract.Request,
	expectedResult contract.Result,
	payload []byte,
) ([]byte, error) {
	if ctx == nil || request.Validate() != nil || expectedResult.Validate() != nil || expectedResult.Status != "ready" ||
		expectedResult.RequestID != request.RequestID {
		return nil, errors.New("transient Shadow candidate binding is invalid")
	}
	var bundle CandidateBundle
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		bundle.ShadowAttempt == nil || bundle.RequestID != request.RequestID ||
		!reflect.DeepEqual(*bundle.ShadowAttempt, expectedResult) {
		return nil, errors.New("transient Shadow candidate evidence is invalid")
	}
	shape := bundle
	shape.Protocol = ""
	shape.Diagnostics = nil
	if err := ValidateBundle(&shape); err != nil {
		return nil, err
	}
	if err := validateProviderAccountBinding(shape.DatabaseCredential, value.Account, value.CatalogKey); err != nil {
		return nil, err
	}
	expected, err := normalizeRequestedScopes(value.Scopes)
	if err != nil {
		return nil, err
	}
	actual, err := diagnosticStringList(bundle.Diagnostics, "requested_scopes")
	if err != nil || strings.Join(actual, "\x00") != strings.Join(expected, "\x00") {
		return nil, errors.New("Shadow candidate scopes do not match the current command")
	}
	if value.ValidateSelected == nil {
		return nil, errors.New("Shadow candidate has no selected-input validator")
	}
	if err := value.ValidateSelected(ctx, &shape); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return marshalMinimalShadowCredential(shape)
}
