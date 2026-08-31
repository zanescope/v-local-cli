// Package shadowcontract owns the secret-free, versioned wire contract shared by
// v-local-key-provider and v-local-cli for one-shot macOS Shadow attempts.
package shadowcontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	Version            = "v-local-shadow-ephemeral/v1"
	DeadlineClock      = "darwin_clock_monotonic_raw"
	CleanupRouteDirect = "direct"

	CaptureWindowNS                  uint64 = 75_000_000_000
	TransformationPreparationLimitNS uint64 = 30_000_000_000
	ProviderCleanupWindowNS          uint64 = 100_000_000_000
	CLIVerifyWindowNS                uint64 = 108_000_000_000
	MutationStopWindowNS             uint64 = 115_000_000_000
	ReturnWindowNS                   uint64 = 120_000_000_000
)

const (
	ErrorNone                       = "none"
	ErrorProductionRouteDisabled    = "production_route_disabled"
	ErrorApprovalRequired           = "approval_required"
	ErrorChallengeInvalid           = "challenge_invalid"
	ErrorChallengeExpired           = "challenge_expired"
	ErrorChallengeConsumptionFailed = "challenge_consumption_failed"
	ErrorBuildSetMismatch           = "build_set_mismatch"
	ErrorSourceDrift                = "source_drift"
	ErrorSecurityPostureDrift       = "security_posture_drift"
	ErrorDeadlineCapture            = "deadline_capture"
	ErrorDeadlineProviderCleanup    = "deadline_provider_cleanup"
	ErrorDeadlineCLIVerify          = "deadline_cli_verify"
	ErrorDeadlinePublication        = "deadline_publication"
	ErrorWorkspacePrepare           = "workspace_prepare_failed"
	ErrorTransformationUnsupported  = "transformation_unsupported"
	ErrorStrictVerification         = "strict_verification_failed"
	ErrorSupervisor                 = "supervisor_failed"
	ErrorCapture                    = "capture_failed"
	ErrorCleanup                    = "cleanup_failed"
	ErrorCleanupVerification        = "cleanup_verification_failed"
	ErrorCredentialInvalid          = "credential_invalid"
	ErrorKeychain                   = "keychain_failed"
	ErrorStateCommit                = "state_commit_failed"
	ErrorResidue                    = "residue_detected"
	ErrorInternal                   = "internal_error"
)

var validErrors = map[string]bool{
	ErrorNone: true, ErrorProductionRouteDisabled: true, ErrorApprovalRequired: true,
	ErrorChallengeInvalid: true, ErrorChallengeExpired: true, ErrorChallengeConsumptionFailed: true,
	ErrorBuildSetMismatch: true, ErrorSourceDrift: true, ErrorSecurityPostureDrift: true,
	ErrorDeadlineCapture: true, ErrorDeadlineProviderCleanup: true, ErrorDeadlineCLIVerify: true,
	ErrorDeadlinePublication: true, ErrorWorkspacePrepare: true, ErrorTransformationUnsupported: true,
	ErrorStrictVerification: true, ErrorSupervisor: true, ErrorCapture: true, ErrorCleanup: true,
	ErrorCleanupVerification: true, ErrorCredentialInvalid: true, ErrorKeychain: true,
	ErrorStateCommit: true, ErrorResidue: true, ErrorInternal: true,
}

type Deadline struct {
	Clock             string `json:"clock"`
	T0NS              uint64 `json:"t0_ns"`
	CaptureStopNS     uint64 `json:"capture_stop_ns"`
	ProviderCleanupNS uint64 `json:"provider_cleanup_ns"`
	CLIVerifyNS       uint64 `json:"cli_verify_ns"`
	MutationStopNS    uint64 `json:"mutation_stop_ns"`
	ReturnNS          uint64 `json:"return_ns"`
}

func NewDeadline(t0 uint64) Deadline {
	return Deadline{
		Clock: DeadlineClock, T0NS: t0,
		CaptureStopNS: t0 + CaptureWindowNS, ProviderCleanupNS: t0 + ProviderCleanupWindowNS,
		CLIVerifyNS: t0 + CLIVerifyWindowNS, MutationStopNS: t0 + MutationStopWindowNS,
		ReturnNS: t0 + ReturnWindowNS,
	}
}

func (value Deadline) Validate() error {
	if value.Clock != DeadlineClock || value.T0NS == 0 || value.T0NS > ^uint64(0)-ReturnWindowNS {
		return errors.New("shadow deadline clock or T0 is invalid")
	}
	expected := NewDeadline(value.T0NS)
	if value != expected || !(value.T0NS < value.CaptureStopNS &&
		value.CaptureStopNS < value.ProviderCleanupNS &&
		value.ProviderCleanupNS < value.CLIVerifyNS &&
		value.CLIVerifyNS < value.MutationStopNS &&
		value.MutationStopNS < value.ReturnNS) {
		return errors.New("shadow deadline stages do not share the fixed T0 contract")
	}
	return nil
}

type Challenge struct {
	Version                   string `json:"version"`
	ChallengeID               string `json:"challenge_id"`
	Operation                 string `json:"operation"`
	BuildSetDigest            string `json:"build_set_digest"`
	SourceQualificationDigest string `json:"source_qualification_digest"`
	CleanupRoute              string `json:"cleanup_route"`
	AccountBindingID          string `json:"account_binding_id"`
	OptionsDigest             string `json:"options_digest"`
	IssuedAtUnix              int64  `json:"issued_at_unix"`
	ExpiresAtUnix             int64  `json:"expires_at_unix"`
}

func (value Challenge) Validate() error {
	if value.Version != Version || !validID(value.ChallengeID) || !validDigest(value.BuildSetDigest) ||
		!validDigest(value.SourceQualificationDigest) || !validAccountID(value.AccountBindingID) ||
		!validDigest(value.OptionsDigest) || value.CleanupRoute != CleanupRouteDirect ||
		(value.Operation != "execute" && value.Operation != "synthetic_execute") {
		return errors.New("shadow challenge binding is invalid")
	}
	if value.IssuedAtUnix <= 0 || value.ExpiresAtUnix <= value.IssuedAtUnix || value.ExpiresAtUnix-value.IssuedAtUnix > 300 {
		return errors.New("shadow challenge lifetime is invalid")
	}
	return nil
}

type Qualification struct {
	Version                   string `json:"version"`
	BuildSetDigest            string `json:"build_set_digest"`
	SourceQualificationDigest string `json:"source_qualification_digest"`
	CleanupRoute              string `json:"cleanup_route"`
	AccountBindingID          string `json:"account_binding_id"`
	OptionsDigest             string `json:"options_digest"`
	SourceVersion             string `json:"source_version"`
	SourceBuild               string `json:"source_build"`
	ProductionRouteEnabled    bool   `json:"production_route_enabled"`
}

func (value Qualification) Validate() error {
	if value.Version != Version || !validDigest(value.BuildSetDigest) ||
		!validDigest(value.SourceQualificationDigest) || value.CleanupRoute != CleanupRouteDirect ||
		!validAccountID(value.AccountBindingID) || !validDigest(value.OptionsDigest) ||
		strings.TrimSpace(value.SourceVersion) == "" || strings.TrimSpace(value.SourceBuild) == "" {
		return errors.New("shadow qualification is invalid")
	}
	return nil
}

type Request struct {
	Version                   string    `json:"version"`
	Operation                 string    `json:"operation"`
	RequestID                 string    `json:"request_id"`
	ChallengeID               string    `json:"challenge_id,omitempty"`
	BuildSetDigest            string    `json:"build_set_digest"`
	SourceQualificationDigest string    `json:"source_qualification_digest"`
	CleanupRoute              string    `json:"cleanup_route"`
	AccountBindingID          string    `json:"account_binding_id"`
	OptionsDigest             string    `json:"options_digest"`
	Deadline                  *Deadline `json:"deadline,omitempty"`
}

func (value Request) Validate() error {
	if value.Version != Version || !validID(value.RequestID) || !validDigest(value.BuildSetDigest) ||
		!validDigest(value.SourceQualificationDigest) || value.CleanupRoute != CleanupRouteDirect ||
		!validAccountID(value.AccountBindingID) || !validDigest(value.OptionsDigest) {
		return errors.New("shadow request binding is invalid")
	}
	switch value.Operation {
	case "qualify":
		if value.ChallengeID != "" || value.Deadline != nil {
			return errors.New("shadow qualification request cannot carry approval or T0")
		}
	case "execute", "synthetic_execute":
		if !validID(value.ChallengeID) || value.Deadline == nil {
			return errors.New("shadow execution request lacks a bound approval or deadline")
		}
		if err := value.Deadline.Validate(); err != nil {
			return err
		}
	default:
		return errors.New("shadow operation is invalid")
	}
	return nil
}

type ResourceBinding struct {
	Kind         string `json:"kind"`
	Leaf         string `json:"leaf"`
	Device       uint64 `json:"device"`
	Inode        uint64 `json:"inode"`
	UID          uint32 `json:"uid"`
	Mode         uint32 `json:"mode"`
	LinkCount    uint64 `json:"link_count"`
	DigestSHA256 string `json:"digest_sha256,omitempty"`
}

func (value ResourceBinding) Validate() error {
	validKinds := map[string]bool{
		"workspace": true, "clone_app": true, "container": true, "hook": true,
		"socket": true, "recovery_record": true, "supervisor": true,
	}
	directoryKind := value.Kind == "workspace" || value.Kind == "clone_app" || value.Kind == "container"
	validLinks := directoryKind && value.LinkCount >= 1 || !directoryKind && value.LinkCount == 1
	digestKind := value.Kind == "clone_app" || value.Kind == "supervisor"
	validDigestBinding := digestKind == (value.DigestSHA256 != "") &&
		(value.DigestSHA256 == "" || validDigest(value.DigestSHA256))
	if !validKinds[value.Kind] || !validRelativeLeaf(value.Leaf) || value.Device == 0 || value.Inode == 0 ||
		value.Mode == 0 || value.Mode > 0o7777 || !validLinks || !validDigestBinding {
		return errors.New("shadow resource binding is invalid")
	}
	return nil
}

type ProcessBinding struct {
	PID                        int    `json:"pid"`
	StartMonotonicNS           uint64 `json:"start_monotonic_ns"`
	SupervisorPID              int    `json:"supervisor_pid"`
	SupervisorStartMonotonicNS uint64 `json:"supervisor_start_monotonic_ns"`
	ExecutableLeaf             string `json:"executable_leaf"`
	ExecutableDigest           string `json:"executable_digest"`
	CloneRootLeaf              string `json:"clone_root_leaf"`
	SupervisorDigest           string `json:"supervisor_digest"`
}

func (value ProcessBinding) Validate() error {
	if value.PID <= 0 || value.StartMonotonicNS == 0 || value.SupervisorPID <= 0 ||
		value.SupervisorStartMonotonicNS == 0 || value.SupervisorPID == value.PID || !validRelativeLeaf(value.ExecutableLeaf) ||
		!validRelativeLeaf(value.CloneRootLeaf) || !validDigest(value.ExecutableDigest) ||
		!validDigest(value.SupervisorDigest) {
		return errors.New("shadow process binding is invalid")
	}
	return nil
}

type CleanupFacts struct {
	ProcessAbsent            bool `json:"process_absent"`
	SupervisorAbsent         bool `json:"supervisor_absent"`
	LaunchRegistrationAbsent bool `json:"launch_registration_absent"`
	ContainerAbsent          bool `json:"container_absent"`
	HookAbsent               bool `json:"hook_absent"`
	SocketAbsent             bool `json:"socket_absent"`
	CloneAbsent              bool `json:"clone_absent"`
	WorkspaceAbsent          bool `json:"workspace_absent"`
	RecoveryRecordAbsent     bool `json:"recovery_record_absent"`
	SourceUnchanged          bool `json:"source_unchanged"`
	SecurityPostureExpected  bool `json:"security_posture_expected"`
}

func (value CleanupFacts) Complete() bool {
	return value.ProcessAbsent && value.SupervisorAbsent && value.LaunchRegistrationAbsent &&
		value.ContainerAbsent && value.HookAbsent && value.SocketAbsent && value.CloneAbsent &&
		value.WorkspaceAbsent && value.RecoveryRecordAbsent && value.SourceUnchanged &&
		value.SecurityPostureExpected
}

type Timings struct {
	PrepareMS       int64 `json:"prepare_ms"`
	TransformMS     int64 `json:"transform_ms"`
	LaunchCaptureMS int64 `json:"launch_capture_ms"`
	CleanupMS       int64 `json:"cleanup_ms"`
	ProviderTotalMS int64 `json:"provider_total_ms"`
}

func (value Timings) Validate() error {
	values := []int64{value.PrepareMS, value.TransformMS, value.LaunchCaptureMS, value.CleanupMS, value.ProviderTotalMS}
	for _, duration := range values {
		if duration < 0 || duration > 120_000 {
			return errors.New("shadow timing is outside the attempt bound")
		}
	}
	return nil
}

func (value Timings) readyBound() bool {
	stages := value.PrepareMS + value.TransformMS + value.LaunchCaptureMS + value.CleanupMS
	return value.TransformMS < int64(TransformationPreparationLimitNS/1_000_000) &&
		value.ProviderTotalMS >= stages
}

type CleanupReceipt struct {
	Version                   string            `json:"version"`
	Operation                 string            `json:"operation"`
	AttemptID                 string            `json:"attempt_id"`
	ChallengeID               string            `json:"challenge_id"`
	BuildSetDigest            string            `json:"build_set_digest"`
	SourceQualificationDigest string            `json:"source_qualification_digest"`
	CleanupRoute              string            `json:"cleanup_route"`
	AccountBindingID          string            `json:"account_binding_id"`
	OptionsDigest             string            `json:"options_digest"`
	RootLeaf                  string            `json:"root_leaf"`
	BundleID                  string            `json:"bundle_id"`
	Resources                 []ResourceBinding `json:"resources"`
	Process                   *ProcessBinding   `json:"process,omitempty"`
	Cleanup                   CleanupFacts      `json:"cleanup"`
	Timings                   Timings           `json:"timings"`
}

func (value CleanupReceipt) Validate() error {
	if value.Version != Version || (value.Operation != "execute" && value.Operation != "synthetic_execute") ||
		!validID(value.AttemptID) || !validID(value.ChallengeID) ||
		!validDigest(value.BuildSetDigest) || !validDigest(value.SourceQualificationDigest) ||
		value.CleanupRoute != CleanupRouteDirect || !validAccountID(value.AccountBindingID) ||
		!validDigest(value.OptionsDigest) || value.RootLeaf != "attempt-"+value.AttemptID ||
		value.BundleID != "com.zanescope.vlocal.shadow."+value.AttemptID || len(value.Resources) != 7 {
		return errors.New("shadow cleanup receipt binding is invalid")
	}
	seen := map[string]bool{}
	seenKinds := map[string]ResourceBinding{}
	for _, resource := range value.Resources {
		if err := resource.Validate(); err != nil {
			return err
		}
		key := resource.Kind + "\x00" + resource.Leaf
		if seen[key] {
			return errors.New("shadow cleanup receipt repeats a resource binding")
		}
		if _, found := seenKinds[resource.Kind]; found {
			return errors.New("shadow cleanup receipt repeats a resource class")
		}
		seen[key] = true
		seenKinds[resource.Kind] = resource
	}
	for _, required := range []string{"workspace", "clone_app", "container", "hook", "socket", "recovery_record", "supervisor"} {
		if _, found := seenKinds[required]; !found {
			return errors.New("shadow cleanup receipt omits a required resource class")
		}
	}
	if seenKinds["workspace"].Leaf != value.RootLeaf ||
		seenKinds["clone_app"].Leaf != value.RootLeaf+"/WeChat.app" ||
		seenKinds["container"].Leaf != value.BundleID ||
		seenKinds["recovery_record"].Leaf != "recovery.json" ||
		seenKinds["supervisor"].Leaf != "v-local-shadow-supervisor" ||
		!validDigest(seenKinds["clone_app"].DigestSHA256) ||
		!validDigest(seenKinds["supervisor"].DigestSHA256) {
		return errors.New("shadow cleanup receipt resource identity is invalid")
	}
	for _, kind := range []string{"hook", "socket"} {
		if !strings.HasPrefix(seenKinds[kind].Leaf, value.RootLeaf+"/") {
			return errors.New("shadow cleanup receipt capture leaf is not attempt-bound")
		}
	}
	if value.Process != nil {
		if err := value.Process.Validate(); err != nil {
			return err
		}
		executablePrefix := value.RootLeaf + "/WeChat.app/Contents/MacOS/"
		executableName := strings.TrimPrefix(value.Process.ExecutableLeaf, executablePrefix)
		if value.Process.CloneRootLeaf != value.RootLeaf || executableName == value.Process.ExecutableLeaf ||
			executableName == "" || strings.Contains(executableName, "/") ||
			value.Process.SupervisorDigest != seenKinds["supervisor"].DigestSHA256 {
			return errors.New("shadow cleanup receipt process identity is invalid")
		}
	}
	if err := value.Timings.Validate(); err != nil {
		return err
	}
	return nil
}

type Result struct {
	Version            string          `json:"version"`
	RequestID          string          `json:"request_id"`
	Status             string          `json:"status"`
	Action             string          `json:"action,omitempty"`
	ErrorCode          string          `json:"error_code"`
	CredentialReleased bool            `json:"credential_released"`
	Qualification      *Qualification  `json:"qualification,omitempty"`
	Receipt            *CleanupReceipt `json:"receipt,omitempty"`
}

func (value Result) Validate() error {
	if value.Version != Version || !validID(value.RequestID) || !validErrors[value.ErrorCode] {
		return errors.New("shadow result header is invalid")
	}
	if value.Qualification != nil {
		if err := value.Qualification.Validate(); err != nil {
			return err
		}
	}
	if value.Receipt != nil {
		if err := value.Receipt.Validate(); err != nil {
			return err
		}
	}
	switch value.Status {
	case "qualified":
		if value.Qualification == nil || value.Receipt != nil || value.Action != "" ||
			value.ErrorCode != ErrorNone || value.CredentialReleased {
			return errors.New("shadow qualified result is inconsistent")
		}
	case "action_required":
		if value.Action != "approve_shadow_mode" || value.ErrorCode != ErrorApprovalRequired ||
			value.Qualification == nil || value.Receipt != nil || value.CredentialReleased {
			return errors.New("shadow approval result is inconsistent")
		}
	case "ready":
		if value.Action != "" || value.ErrorCode != ErrorNone || !value.CredentialReleased ||
			value.Qualification != nil || value.Receipt == nil || value.Receipt.Process == nil ||
			!value.Receipt.Cleanup.Complete() || !value.Receipt.Timings.readyBound() {
			return errors.New("shadow ready result lacks complete cleanup evidence")
		}
	case "failed", "cleanup_pending":
		if value.Action != "" || value.ErrorCode == ErrorNone || value.CredentialReleased || value.Qualification != nil {
			return errors.New("shadow failure result is inconsistent")
		}
		if value.Status == "cleanup_pending" && value.Receipt != nil && value.Receipt.Cleanup.Complete() {
			return errors.New("shadow cleanup_pending result claims complete cleanup")
		}
	default:
		return errors.New("shadow result status is invalid")
	}
	return nil
}

type GoldenVectors struct {
	Version        string         `json:"version"`
	Challenge      Challenge      `json:"challenge"`
	QualifyRequest Request        `json:"qualify_request"`
	ExecuteRequest Request        `json:"execute_request"`
	Qualification  Qualification  `json:"qualification"`
	CleanupReceipt CleanupReceipt `json:"cleanup_receipt"`
	ReadyResult    Result         `json:"ready_result"`
	FailureResult  Result         `json:"failure_result"`
}

func (value GoldenVectors) Validate() error {
	if value.Version != Version {
		return errors.New("shadow vector version is invalid")
	}
	validators := []func() error{
		value.Challenge.Validate, value.QualifyRequest.Validate, value.ExecuteRequest.Validate,
		value.Qualification.Validate, value.CleanupReceipt.Validate,
		value.ReadyResult.Validate, value.FailureResult.Validate,
	}
	for _, validate := range validators {
		if err := validate(); err != nil {
			return err
		}
	}
	if value.ExecuteRequest.ChallengeID != value.Challenge.ChallengeID ||
		value.ExecuteRequest.Operation != value.Challenge.Operation ||
		value.CleanupReceipt.ChallengeID != value.Challenge.ChallengeID ||
		value.CleanupReceipt.Operation != value.ExecuteRequest.Operation ||
		value.ReadyResult.RequestID != value.ExecuteRequest.RequestID ||
		value.ReadyResult.Receipt == nil {
		return errors.New("shadow vectors are not cross-bound")
	}
	type binding struct {
		buildSetDigest            string
		sourceQualificationDigest string
		cleanupRoute              string
		accountBindingID          string
		optionsDigest             string
	}
	expected := binding{
		buildSetDigest:            value.Challenge.BuildSetDigest,
		sourceQualificationDigest: value.Challenge.SourceQualificationDigest,
		cleanupRoute:              value.Challenge.CleanupRoute,
		accountBindingID:          value.Challenge.AccountBindingID,
		optionsDigest:             value.Challenge.OptionsDigest,
	}
	bindings := []binding{
		{
			buildSetDigest:            value.QualifyRequest.BuildSetDigest,
			sourceQualificationDigest: value.QualifyRequest.SourceQualificationDigest,
			cleanupRoute:              value.QualifyRequest.CleanupRoute,
			accountBindingID:          value.QualifyRequest.AccountBindingID,
			optionsDigest:             value.QualifyRequest.OptionsDigest,
		},
		{
			buildSetDigest:            value.ExecuteRequest.BuildSetDigest,
			sourceQualificationDigest: value.ExecuteRequest.SourceQualificationDigest,
			cleanupRoute:              value.ExecuteRequest.CleanupRoute,
			accountBindingID:          value.ExecuteRequest.AccountBindingID,
			optionsDigest:             value.ExecuteRequest.OptionsDigest,
		},
		{
			buildSetDigest:            value.Qualification.BuildSetDigest,
			sourceQualificationDigest: value.Qualification.SourceQualificationDigest,
			cleanupRoute:              value.Qualification.CleanupRoute,
			accountBindingID:          value.Qualification.AccountBindingID,
			optionsDigest:             value.Qualification.OptionsDigest,
		},
		{
			buildSetDigest:            value.CleanupReceipt.BuildSetDigest,
			sourceQualificationDigest: value.CleanupReceipt.SourceQualificationDigest,
			cleanupRoute:              value.CleanupReceipt.CleanupRoute,
			accountBindingID:          value.CleanupReceipt.AccountBindingID,
			optionsDigest:             value.CleanupReceipt.OptionsDigest,
		},
	}
	for _, actual := range bindings {
		if actual != expected {
			return errors.New("shadow vectors mix frozen attempt bindings")
		}
	}
	cleanupReceipt, cleanupErr := json.Marshal(value.CleanupReceipt)
	readyReceipt, readyErr := json.Marshal(value.ReadyResult.Receipt)
	if cleanupErr != nil || readyErr != nil || !bytes.Equal(cleanupReceipt, readyReceipt) {
		return errors.New("shadow ready result does not carry the exact cleanup vector")
	}
	return nil
}

func DecodeStrict(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("shadow JSON contains trailing data")
	}
	return nil
}

func CanonicalJSON(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func Digest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func validID(value string) bool {
	return len(value) == 32 && lowerHex(value)
}

func validDigest(value string) bool {
	return len(value) == 64 && lowerHex(value)
}

func validAccountID(value string) bool {
	return (len(value) == 16 || len(value) == 32) && lowerHex(value)
}

func lowerHex(value string) bool {
	if value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validRelativeLeaf(value string) bool {
	// Contract leaves are wire-format paths, not host filesystem paths. Keep
	// their slash semantics stable on every OS and reject Windows drive/ADS
	// syntax explicitly instead of letting filepath.Clean reinterpret it.
	if value == "" || len(value) > 240 || path.IsAbs(value) ||
		strings.ContainsAny(value, "\\:") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func ValidateErrorCode(value string) error {
	if !validErrors[value] {
		return fmt.Errorf("unknown shadow error code %q", value)
	}
	return nil
}
