// Package shadowowner composes the CLI-owned Shadow lifecycle without taking
// over Provider resource ownership. It establishes approval/T0, calls the
// Provider, independently verifies cleanup, validates the candidate, and only
// then starts the Keychain generation transaction.
package shadowowner

import (
	"context"
	"errors"

	approvalmodel "github.com/zanescope/v-local-cli/internal/shadowapproval"
	clockmodel "github.com/zanescope/v-local-cli/internal/shadowclock"
	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
	publishmodel "github.com/zanescope/v-local-cli/internal/shadowpublish"
)

type Provider interface {
	Qualify(context.Context, contract.Request) (contract.Result, error)
	Execute(context.Context, contract.Request) (contract.Result, Credential, error)
}

type Credential struct {
	// Candidate is transient validation evidence. The CLI validator derives the
	// only payload that may be handed to the Keychain publication transaction.
	Candidate []byte
}

type Verifier interface {
	Verify(context.Context, contract.Request, contract.Result) error
}

type CandidateValidator interface {
	ValidateAndDerive(context.Context, contract.Request, contract.Result, []byte) ([]byte, error)
}

type Publisher interface {
	Publish(context.Context, publishmodel.Request, []byte) (publishmodel.GenerationState, error)
}

type Approval interface {
	Issue(context.Context, contract.Qualification, string) (contract.Challenge, error)
	Consume(context.Context, string, string, bool, approvalmodel.Binding, string) (contract.Request, error)
}

type Owner struct {
	Operation string
	Clock     clockmodel.Clock
	Provider  Provider
	Approval  Approval
	Verifier  Verifier
	Validator CandidateValidator
	Publisher Publisher
}

type Binding struct {
	BuildSetDigest            string
	SourceQualificationDigest string
	CleanupRoute              string
	AccountBindingID          string
	OptionsDigest             string
}

func (value Binding) request(requestID, operation string) contract.Request {
	return contract.Request{
		Version: contract.Version, Operation: operation, RequestID: requestID,
		BuildSetDigest: value.BuildSetDigest, SourceQualificationDigest: value.SourceQualificationDigest,
		CleanupRoute: value.CleanupRoute, AccountBindingID: value.AccountBindingID, OptionsDigest: value.OptionsDigest,
	}
}

func (value Binding) approval() approvalmodel.Binding {
	return approvalmodel.Binding{
		BuildSetDigest: value.BuildSetDigest, SourceQualificationDigest: value.SourceQualificationDigest,
		CleanupRoute: value.CleanupRoute, AccountBindingID: value.AccountBindingID, OptionsDigest: value.OptionsDigest,
	}
}

type PlanResult struct {
	Result    contract.Result
	Challenge contract.Challenge
}

type Result struct {
	Status       string
	ErrorCode    string
	Shadow       contract.Result
	GenerationID string
}

func (value *Owner) validate() error {
	if value == nil || value.Clock == nil || value.Provider == nil || value.Approval == nil ||
		value.Verifier == nil || value.Validator == nil || value.Publisher == nil {
		return errors.New("Shadow AttemptOwner dependencies are incomplete")
	}
	if value.Operation != "execute" && value.Operation != "synthetic_execute" {
		return errors.New("Shadow AttemptOwner operation is invalid")
	}
	return nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func before(clock clockmodel.Clock, deadline uint64) bool {
	result, err := clockmodel.Before(clock, deadline)
	return err == nil && result
}

func failed(shadow contract.Result, code string) Result {
	return Result{Status: "failed", ErrorCode: code, Shadow: shadow}
}

func (value *Owner) Plan(ctx context.Context, requestID string, binding Binding) (PlanResult, error) {
	if err := value.validate(); err != nil || ctx == nil {
		if err == nil {
			err = errors.New("Shadow AttemptOwner context is missing")
		}
		return PlanResult{}, err
	}
	request := binding.request(requestID, "qualify")
	if err := request.Validate(); err != nil {
		return PlanResult{}, err
	}
	qualified, err := value.Provider.Qualify(ctx, request)
	if err != nil {
		return PlanResult{}, err
	}
	if qualified.Validate() != nil || qualified.Status != "qualified" || qualified.Qualification == nil ||
		qualified.RequestID != requestID {
		return PlanResult{}, errors.New("Provider Shadow qualification result is invalid")
	}
	qualification := *qualified.Qualification
	if qualification.BuildSetDigest != binding.BuildSetDigest ||
		qualification.SourceQualificationDigest != binding.SourceQualificationDigest ||
		qualification.CleanupRoute != binding.CleanupRoute || qualification.AccountBindingID != binding.AccountBindingID ||
		qualification.OptionsDigest != binding.OptionsDigest {
		return PlanResult{}, errors.New("Provider Shadow qualification drifted from the CLI binding")
	}
	if value.Operation == "execute" && !qualification.ProductionRouteEnabled {
		return PlanResult{Result: contract.Result{
			Version: contract.Version, RequestID: requestID, Status: "failed",
			ErrorCode: contract.ErrorProductionRouteDisabled,
		}}, nil
	}
	challenge, err := value.Approval.Issue(ctx, qualification, value.Operation)
	if err != nil {
		return PlanResult{}, err
	}
	result := contract.Result{
		Version: contract.Version, RequestID: requestID, Status: "action_required",
		Action: "approve_shadow_mode", ErrorCode: contract.ErrorApprovalRequired,
		Qualification: &qualification,
	}
	if err := result.Validate(); err != nil {
		return PlanResult{}, err
	}
	return PlanResult{Result: result, Challenge: challenge}, nil
}

func (value *Owner) Execute(ctx context.Context, challengeID, requestID string, confirmed bool, binding Binding) (Result, error) {
	if err := value.validate(); err != nil || ctx == nil {
		if err == nil {
			err = errors.New("Shadow AttemptOwner context is missing")
		}
		return Result{}, err
	}
	request, err := value.Approval.Consume(ctx, challengeID, requestID, confirmed, binding.approval(), value.Operation)
	if err != nil {
		return Result{}, err
	}
	shadowResult, credential, err := value.Provider.Execute(ctx, request)
	defer clearBytes(credential.Candidate)
	if err != nil {
		return failed(shadowResult, contract.ErrorInternal), nil
	}
	if shadowResult.Validate() != nil || shadowResult.RequestID != request.RequestID {
		return failed(shadowResult, contract.ErrorInternal), nil
	}
	if shadowResult.Status != "ready" {
		return Result{Status: shadowResult.Status, ErrorCode: shadowResult.ErrorCode, Shadow: shadowResult}, nil
	}
	if !before(value.Clock, request.Deadline.ProviderCleanupNS) {
		return failed(shadowResult, contract.ErrorDeadlineProviderCleanup), nil
	}
	if len(credential.Candidate) == 0 {
		return failed(shadowResult, contract.ErrorCredentialInvalid), nil
	}
	if err := value.Verifier.Verify(ctx, request, shadowResult); err != nil {
		return failed(shadowResult, contract.ErrorCleanupVerification), nil
	}
	if !before(value.Clock, request.Deadline.CLIVerifyNS) {
		return failed(shadowResult, contract.ErrorDeadlineCLIVerify), nil
	}
	minimal, err := value.Validator.ValidateAndDerive(ctx, request, shadowResult, credential.Candidate)
	defer clearBytes(minimal)
	if err != nil || len(minimal) == 0 {
		return failed(shadowResult, contract.ErrorCredentialInvalid), nil
	}
	if !before(value.Clock, request.Deadline.CLIVerifyNS) {
		return failed(shadowResult, contract.ErrorDeadlineCLIVerify), nil
	}
	generation, err := value.Publisher.Publish(ctx, publishmodel.Request{
		AccountBindingID: request.AccountBindingID, BuildSetDigest: request.BuildSetDigest,
		AttemptID: shadowResult.Receipt.AttemptID, Deadline: *request.Deadline,
	}, minimal)
	if err != nil {
		if publishmodel.Committed(err) {
			return Result{Status: "cleanup_pending", ErrorCode: contract.ErrorKeychain, Shadow: shadowResult, GenerationID: generation.GenerationID}, nil
		}
		return failed(shadowResult, contract.ErrorKeychain), nil
	}
	if !before(value.Clock, request.Deadline.ReturnNS) {
		return Result{Status: "failed", ErrorCode: contract.ErrorDeadlinePublication, Shadow: shadowResult, GenerationID: generation.GenerationID}, nil
	}
	return Result{Status: "ready", ErrorCode: contract.ErrorNone, Shadow: shadowResult, GenerationID: generation.GenerationID}, nil
}
