// Package shadowverify independently verifies Provider cleanup evidence before
// the CLI begins any credential publication transaction.
package shadowverify

import (
	"context"
	"errors"
	"fmt"

	clockmodel "github.com/zanescope/v-local-cli/internal/shadowclock"
	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
)

// Probe is intentionally independent from the Provider. Implementations must
// query the current machine state for each exact receipt binding; they cannot
// translate the Provider's CleanupFacts booleans into answers.
type Probe interface {
	BuildSetDigest(context.Context) (string, error)
	ResourceAbsent(context.Context, string, contract.ResourceBinding) (bool, error)
	ProcessAbsent(context.Context, contract.ProcessBinding) (bool, error)
	LaunchRegistrationAbsent(context.Context, string, contract.ResourceBinding) (bool, error)
	SourceUnchanged(context.Context, string) (bool, error)
	SecurityPostureExpected(context.Context) (bool, error)
}

type Verifier struct {
	Clock clockmodel.Clock
	Probe Probe
}

type Failure struct {
	Fact string
}

func (value *Failure) Error() string {
	return "independent Shadow verification failed at " + value.Fact
}

func fail(fact string) error { return &Failure{Fact: fact} }

func before(clock clockmodel.Clock, deadline uint64) bool {
	result, err := clockmodel.Before(clock, deadline)
	return err == nil && result
}

func boundedContext(parent context.Context, clock clockmodel.Clock, deadline uint64) (context.Context, context.CancelFunc, error) {
	remaining, err := clockmodel.Remaining(clock, deadline)
	if err != nil || remaining <= 0 {
		return nil, nil, errors.New("independent Shadow verification deadline is unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, remaining)
	return ctx, cancel, nil
}

func cloneBinding(receipt *contract.CleanupReceipt) (contract.ResourceBinding, error) {
	for _, resource := range receipt.Resources {
		if resource.Kind == "clone_app" {
			return resource, nil
		}
	}
	return contract.ResourceBinding{}, errors.New("cleanup receipt has no clone binding")
}

func (value Verifier) Verify(ctx context.Context, request contract.Request, result contract.Result) error {
	if value.Clock == nil || value.Probe == nil || ctx == nil {
		return errors.New("independent Shadow verifier dependencies are incomplete")
	}
	if err := request.Validate(); err != nil || request.Deadline == nil ||
		(request.Operation != "execute" && request.Operation != "synthetic_execute") {
		return errors.New("independent Shadow verifier request is invalid")
	}
	if err := result.Validate(); err != nil || result.Status != "ready" || result.Receipt == nil || result.Receipt.Process == nil {
		return errors.New("independent Shadow verifier result is not ready")
	}
	if !before(value.Clock, request.Deadline.CLIVerifyNS) {
		return fail("cli_verify_deadline")
	}
	verifyCtx, cancel, err := boundedContext(ctx, value.Clock, request.Deadline.CLIVerifyNS)
	if err != nil {
		return fail("cli_verify_deadline")
	}
	defer cancel()
	receipt := result.Receipt
	if result.RequestID != request.RequestID || receipt.ChallengeID != request.ChallengeID ||
		receipt.Operation != request.Operation || receipt.AccountBindingID != request.AccountBindingID ||
		receipt.OptionsDigest != request.OptionsDigest ||
		receipt.BuildSetDigest != request.BuildSetDigest ||
		receipt.SourceQualificationDigest != request.SourceQualificationDigest ||
		receipt.CleanupRoute != request.CleanupRoute {
		return fail("cross_process_binding")
	}
	buildSet, err := value.Probe.BuildSetDigest(verifyCtx)
	if err != nil || buildSet != request.BuildSetDigest {
		return fail("build_set")
	}
	if !before(value.Clock, request.Deadline.CLIVerifyNS) || verifyCtx.Err() != nil {
		return fail("cli_verify_deadline")
	}
	for _, resource := range receipt.Resources {
		absent, probeErr := value.Probe.ResourceAbsent(verifyCtx, receipt.RootLeaf, resource)
		if probeErr != nil || !absent {
			return fail("resource_" + resource.Kind)
		}
		if !before(value.Clock, request.Deadline.CLIVerifyNS) || verifyCtx.Err() != nil {
			return fail("cli_verify_deadline")
		}
	}
	processAbsent, err := value.Probe.ProcessAbsent(verifyCtx, *receipt.Process)
	if err != nil || !processAbsent {
		return fail("process")
	}
	if !before(value.Clock, request.Deadline.CLIVerifyNS) || verifyCtx.Err() != nil {
		return fail("cli_verify_deadline")
	}
	clone, err := cloneBinding(receipt)
	if err != nil {
		return err
	}
	launchAbsent, err := value.Probe.LaunchRegistrationAbsent(verifyCtx, receipt.BundleID, clone)
	if err != nil || !launchAbsent {
		return fail("launch_registration")
	}
	if !before(value.Clock, request.Deadline.CLIVerifyNS) || verifyCtx.Err() != nil {
		return fail("cli_verify_deadline")
	}
	sourceUnchanged, err := value.Probe.SourceUnchanged(verifyCtx, receipt.SourceQualificationDigest)
	if err != nil || !sourceUnchanged {
		return fail("source")
	}
	if !before(value.Clock, request.Deadline.CLIVerifyNS) || verifyCtx.Err() != nil {
		return fail("cli_verify_deadline")
	}
	postureExpected, err := value.Probe.SecurityPostureExpected(verifyCtx)
	if err != nil || !postureExpected {
		return fail("security_posture")
	}
	if !before(value.Clock, request.Deadline.CLIVerifyNS) || verifyCtx.Err() != nil {
		return fail("cli_verify_deadline")
	}
	if !receipt.Cleanup.Complete() {
		return fmt.Errorf("Provider receipt is internally incomplete")
	}
	return nil
}
