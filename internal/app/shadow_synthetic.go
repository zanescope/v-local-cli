package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"sync"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
	"github.com/zanescope/v-local-cli/internal/provider"
	approvalmodel "github.com/zanescope/v-local-cli/internal/shadowapproval"
	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
	ownermodel "github.com/zanescope/v-local-cli/internal/shadowowner"
	publishmodel "github.com/zanescope/v-local-cli/internal/shadowpublish"
	verifymodel "github.com/zanescope/v-local-cli/internal/shadowverify"
	"github.com/zanescope/v-local-cli/internal/state"
)

const (
	shadowSyntheticVersion     = "v-local-shadow-synthetic-owner/v1"
	shadowSyntheticAccountID   = "aabbccddeeff0011"
	shadowSyntheticBuild       = "1111111111111111111111111111111111111111111111111111111111111111"
	shadowSyntheticSource      = "2222222222222222222222222222222222222222222222222222222222222222"
	shadowSyntheticOptions     = "3333333333333333333333333333333333333333333333333333333333333333"
	shadowSyntheticChallengeID = "00112233445566778899aabbccddeeff"
	shadowSyntheticPlanID      = "88888888888888888888888888888888"
	shadowSyntheticRequestID   = "99999999999999999999999999999999"
	shadowSyntheticAttemptID   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shadowSyntheticPendingID   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shadowSyntheticReadyID     = "cccccccccccccccccccccccccccccccc"
)

type shadowSyntheticClock struct{ now uint64 }

func (value *shadowSyntheticClock) NowNS() (uint64, error) { return value.now, nil }

type shadowSyntheticWall struct{ now int64 }

func (value shadowSyntheticWall) NowUnix() int64 { return value.now }

type shadowSyntheticLog struct {
	mu     sync.Mutex
	events []string
}

func (value *shadowSyntheticLog) add(event string) {
	value.mu.Lock()
	defer value.mu.Unlock()
	value.events = append(value.events, event)
}

func (value *shadowSyntheticLog) snapshot() []string {
	value.mu.Lock()
	defer value.mu.Unlock()
	return append([]string(nil), value.events...)
}

type shadowSyntheticApprovalStore struct {
	mu        sync.Mutex
	challenge *contract.Challenge
	log       *shadowSyntheticLog
}

func (value *shadowSyntheticApprovalStore) Load(context.Context) (contract.Challenge, bool, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.challenge == nil {
		return contract.Challenge{}, false, nil
	}
	return *value.challenge, true, nil
}

func (value *shadowSyntheticApprovalStore) Save(_ context.Context, challenge contract.Challenge) error {
	value.mu.Lock()
	copy := challenge
	value.challenge = &copy
	value.mu.Unlock()
	value.log.add("approval:issued")
	return nil
}

func (value *shadowSyntheticApprovalStore) Remove(_ context.Context, challengeID string) error {
	value.mu.Lock()
	if value.challenge == nil || value.challenge.ChallengeID != challengeID {
		value.mu.Unlock()
		return errors.New("synthetic approval identity mismatch")
	}
	value.challenge = nil
	value.mu.Unlock()
	value.log.add("approval:consumed")
	return nil
}

type shadowSyntheticProvider struct {
	qualification contract.Qualification
	log           *shadowSyntheticLog
}

func (value *shadowSyntheticProvider) Qualify(_ context.Context, request contract.Request) (contract.Result, error) {
	if request.Validate() != nil || request.Operation != "qualify" {
		return contract.Result{}, errors.New("synthetic Provider qualification request is invalid")
	}
	value.log.add("provider:qualify")
	qualification := value.qualification
	return contract.Result{
		Version: contract.Version, RequestID: request.RequestID, Status: "qualified",
		ErrorCode: contract.ErrorNone, Qualification: &qualification,
	}, nil
}

func (value *shadowSyntheticProvider) Execute(_ context.Context, request contract.Request) (contract.Result, ownermodel.Credential, error) {
	if request.Validate() != nil || request.Operation != "synthetic_execute" {
		return contract.Result{}, ownermodel.Credential{}, errors.New("synthetic Provider execution request is invalid")
	}
	value.log.add("provider:execute_cleanup_complete")
	result := shadowSyntheticReadyResult(request)
	bundle := provider.CandidateBundle{
		Protocol: provider.Protocol, RequestID: request.RequestID,
		ImageKeys:   &provider.ImageKeys{AES: "1234567890abcdef", XOR: 7},
		Diagnostics: map[string]any{"requested_scopes": []string{"media"}}, ShadowAttempt: &result,
	}
	payload, err := json.Marshal(bundle)
	if err != nil {
		return contract.Result{}, ownermodel.Credential{}, err
	}
	return result, ownermodel.Credential{Candidate: payload}, nil
}

func shadowSyntheticReadyResult(request contract.Request) contract.Result {
	root := "attempt-" + shadowSyntheticAttemptID
	bundleID := "com.zanescope.vlocal.shadow." + shadowSyntheticAttemptID
	resources := []contract.ResourceBinding{
		{Kind: "workspace", Leaf: root, Device: 1, Inode: 1, UID: 1, Mode: 448, LinkCount: 1},
		{Kind: "clone_app", Leaf: root + "/WeChat.app", Device: 1, Inode: 2, UID: 1, Mode: 448, LinkCount: 1, DigestSHA256: shadowSyntheticBuild},
		{Kind: "container", Leaf: bundleID, Device: 1, Inode: 3, UID: 1, Mode: 448, LinkCount: 1},
		{Kind: "hook", Leaf: root + "/capture.hook", Device: 1, Inode: 4, UID: 1, Mode: 384, LinkCount: 1},
		{Kind: "socket", Leaf: root + "/capture.sock", Device: 1, Inode: 5, UID: 1, Mode: 384, LinkCount: 1},
		{Kind: "recovery_record", Leaf: "recovery.json", Device: 1, Inode: 6, UID: 1, Mode: 384, LinkCount: 1},
		{Kind: "supervisor", Leaf: "v-local-shadow-supervisor", Device: 1, Inode: 7, UID: 1, Mode: 384, LinkCount: 1, DigestSHA256: shadowSyntheticBuild},
	}
	receipt := contract.CleanupReceipt{
		Version: contract.Version, Operation: request.Operation, AttemptID: shadowSyntheticAttemptID,
		ChallengeID: request.ChallengeID, BuildSetDigest: request.BuildSetDigest,
		SourceQualificationDigest: request.SourceQualificationDigest, CleanupRoute: request.CleanupRoute,
		AccountBindingID: request.AccountBindingID, OptionsDigest: request.OptionsDigest,
		RootLeaf: root, BundleID: bundleID, Resources: resources,
		Process: &contract.ProcessBinding{
			PID: 101, StartMonotonicNS: 1, SupervisorPID: 100, SupervisorStartMonotonicNS: 1,
			ExecutableLeaf: root + "/WeChat.app/Contents/MacOS/WeChat", ExecutableDigest: shadowSyntheticBuild,
			CloneRootLeaf: root, SupervisorDigest: shadowSyntheticBuild,
		},
		Cleanup: contract.CleanupFacts{
			ProcessAbsent: true, SupervisorAbsent: true, LaunchRegistrationAbsent: true,
			ContainerAbsent: true, HookAbsent: true, SocketAbsent: true, CloneAbsent: true,
			WorkspaceAbsent: true, RecoveryRecordAbsent: true, SourceUnchanged: true,
			SecurityPostureExpected: true,
		},
	}
	return contract.Result{
		Version: contract.Version, RequestID: request.RequestID, Status: "ready",
		ErrorCode: contract.ErrorNone, CredentialReleased: true, Receipt: &receipt,
	}
}

type shadowSyntheticProbe struct{ log *shadowSyntheticLog }

func (value shadowSyntheticProbe) BuildSetDigest(context.Context) (string, error) {
	value.log.add("cli:independent_cleanup_verify")
	return shadowSyntheticBuild, nil
}
func (shadowSyntheticProbe) ResourceAbsent(context.Context, string, contract.ResourceBinding) (bool, error) {
	return true, nil
}
func (shadowSyntheticProbe) ProcessAbsent(context.Context, contract.ProcessBinding) (bool, error) {
	return true, nil
}
func (shadowSyntheticProbe) LaunchRegistrationAbsent(context.Context, string, contract.ResourceBinding) (bool, error) {
	return true, nil
}
func (shadowSyntheticProbe) SourceUnchanged(context.Context, string) (bool, error) { return true, nil }
func (shadowSyntheticProbe) SecurityPostureExpected(context.Context) (bool, error) { return true, nil }

type shadowSyntheticState struct {
	mu      sync.Mutex
	ready   *publishmodel.GenerationState
	pending *publishmodel.GenerationState
	log     *shadowSyntheticLog
}

func (value *shadowSyntheticState) LoadReady(context.Context, string) (publishmodel.GenerationState, bool, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.ready == nil {
		return publishmodel.GenerationState{}, false, nil
	}
	return *value.ready, true, nil
}
func (value *shadowSyntheticState) LoadPending(context.Context, string) (publishmodel.GenerationState, bool, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.pending == nil {
		return publishmodel.GenerationState{}, false, nil
	}
	return *value.pending, true, nil
}
func (value *shadowSyntheticState) SaveReady(_ context.Context, state publishmodel.GenerationState) error {
	value.mu.Lock()
	copy := state
	value.ready = &copy
	value.mu.Unlock()
	value.log.add("publisher:ready_saved")
	return nil
}
func (value *shadowSyntheticState) SavePending(_ context.Context, state publishmodel.GenerationState) error {
	value.mu.Lock()
	copy := state
	value.pending = &copy
	value.mu.Unlock()
	value.log.add("publisher:pending_saved")
	return nil
}
func (value *shadowSyntheticState) RemovePending(context.Context, string) error {
	value.mu.Lock()
	value.pending = nil
	value.mu.Unlock()
	value.log.add("state:pending_removed")
	return nil
}
func (value *shadowSyntheticState) hasPending() bool {
	value.mu.Lock()
	defer value.mu.Unlock()
	return value.pending != nil
}
func (value *shadowSyntheticState) readyID() string {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.ready == nil {
		return ""
	}
	return value.ready.GenerationID
}

type shadowSyntheticKeychain struct {
	mu     sync.Mutex
	values map[string][]byte
	log    *shadowSyntheticLog
}

func shadowSyntheticKey(accountID, generationID string) string {
	return accountID + "\x00" + generationID
}

func (value *shadowSyntheticKeychain) Put(_ context.Context, accountID, generationID string, secret []byte) error {
	value.mu.Lock()
	value.values[shadowSyntheticKey(accountID, generationID)] = append([]byte(nil), secret...)
	value.mu.Unlock()
	value.log.add("publisher:keychain_put")
	return nil
}
func (value *shadowSyntheticKeychain) Get(_ context.Context, accountID, generationID string) ([]byte, bool, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	secret, found := value.values[shadowSyntheticKey(accountID, generationID)]
	return append([]byte(nil), secret...), found, nil
}
func (value *shadowSyntheticKeychain) Delete(_ context.Context, accountID, generationID string) error {
	value.mu.Lock()
	delete(value.values, shadowSyntheticKey(accountID, generationID))
	value.mu.Unlock()
	value.log.add("startup:exact_pending_delete")
	return nil
}
func (value *shadowSyntheticKeychain) count() int {
	value.mu.Lock()
	defer value.mu.Unlock()
	return len(value.values)
}

type shadowSyntheticLocker struct{ locks sync.Map }

func (value *shadowSyntheticLocker) Acquire(ctx context.Context, accountID string) (func() error, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, errors.New("synthetic lock context is unavailable")
	}
	mutexValue, _ := value.locks.LoadOrStore(accountID, &sync.Mutex{})
	mutex := mutexValue.(*sync.Mutex)
	mutex.Lock()
	return func() error { mutex.Unlock(); return nil }, nil
}

type shadowSyntheticSummary struct {
	Version                string   `json:"version"`
	Status                 string   `json:"status"`
	Operation              string   `json:"operation"`
	ProductionRouteEnabled bool     `json:"production_route_enabled"`
	StartupReconciled      bool     `json:"startup_reconciled"`
	ApprovalConsumed       bool     `json:"approval_consumed"`
	GenerationID           string   `json:"generation_id,omitempty"`
	PendingGeneration      bool     `json:"pending_generation"`
	KeychainItemCount      int      `json:"keychain_item_count"`
	Events                 []string `json:"events"`
}

func writeShadowSyntheticFailure(writer io.Writer, stage string) int {
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"version": shadowSyntheticVersion, "status": "failed", "stage": stage,
		"production_route_enabled": false,
	})
	return 5
}

func runShadowSyntheticOwnerCommand(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("__shadow-synthetic-owner", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	confirmed := set.Bool("confirm", false, "consume the in-memory synthetic approval")
	if err := set.Parse(args); err != nil || len(set.Args()) != 0 {
		_, _ = fmt.Fprintln(stderr, "v-local-cli: invalid synthetic Shadow Owner arguments")
		return 2
	}

	ctx := context.Background()
	clock := &shadowSyntheticClock{now: 1_000_000_000}
	log := &shadowSyntheticLog{}
	pending := publishmodel.GenerationState{
		Version: publishmodel.StateVersion, Status: "pending", AccountBindingID: shadowSyntheticAccountID,
		GenerationID: shadowSyntheticPendingID, BuildSetDigest: shadowSyntheticBuild,
		AttemptID: "dddddddddddddddddddddddddddddddd",
	}
	stateStore := &shadowSyntheticState{pending: &pending, log: log}
	keychain := &shadowSyntheticKeychain{values: map[string][]byte{
		shadowSyntheticKey(shadowSyntheticAccountID, shadowSyntheticPendingID): []byte("discarded-synthetic-pending-secret"),
	}, log: log}
	locker := &shadowSyntheticLocker{}
	publisher := &publishmodel.Publisher{
		Clock: clock, State: stateStore, Keychain: keychain, Locker: locker,
		NewID: func() (string, error) { return shadowSyntheticReadyID, nil },
	}
	if err := reconcileStartupShadowGenerations(ctx, clock, []state.AccountState{{AccountID: shadowSyntheticAccountID}},
		func(accountID string) (startupGenerationReconciler, error) {
			if accountID != shadowSyntheticAccountID {
				return nil, errors.New("synthetic startup account drift")
			}
			return publisher, nil
		}); err != nil || stateStore.hasPending() || keychain.count() != 0 {
		return writeShadowSyntheticFailure(stderr, "startup_reconciliation")
	}
	log.add("startup:reconciled")

	qualification := contract.Qualification{
		Version: contract.Version, BuildSetDigest: shadowSyntheticBuild,
		SourceQualificationDigest: shadowSyntheticSource, CleanupRoute: contract.CleanupRouteDirect,
		AccountBindingID: shadowSyntheticAccountID, OptionsDigest: shadowSyntheticOptions,
		SourceVersion: "synthetic-only", SourceBuild: "windows-native-contract",
		ProductionRouteEnabled: false,
	}
	approvalStore := &shadowSyntheticApprovalStore{log: log}
	validator := provider.ShadowCandidateValidator{
		Account: localplatform.Account{Name: "synthetic-only"}, Scopes: []string{"media"},
		ValidateSelected: func(_ context.Context, candidate *provider.CandidateBundle) error {
			log.add("cli:candidate_validated")
			if candidate.ImageKeys == nil || candidate.ImageKeys.AES != "1234567890abcdef" || candidate.ImageKeys.XOR != 7 {
				return errors.New("synthetic candidate changed")
			}
			return nil
		},
	}
	owner := &ownermodel.Owner{
		Operation: "synthetic_execute", Clock: clock,
		Provider: &shadowSyntheticProvider{qualification: qualification, log: log},
		Approval: &approvalmodel.Manager{
			Store: approvalStore, Wall: shadowSyntheticWall{now: 1_800_000_000}, Raw: clock,
			NewID: func() (string, error) { return shadowSyntheticChallengeID, nil },
		},
		Verifier:  verifymodel.Verifier{Clock: clock, Probe: shadowSyntheticProbe{log: log}},
		Validator: validator, Publisher: publisher,
	}
	binding := ownermodel.Binding{
		BuildSetDigest: shadowSyntheticBuild, SourceQualificationDigest: shadowSyntheticSource,
		CleanupRoute: contract.CleanupRouteDirect, AccountBindingID: shadowSyntheticAccountID,
		OptionsDigest: shadowSyntheticOptions,
	}
	plan, err := owner.Plan(ctx, shadowSyntheticPlanID, binding)
	if err != nil || plan.Result.Validate() != nil || plan.Result.Status != "action_required" ||
		plan.Result.Qualification == nil || plan.Result.Qualification.ProductionRouteEnabled {
		return writeShadowSyntheticFailure(stderr, "approval_plan")
	}
	if !*confirmed {
		summary := shadowSyntheticSummary{
			Version: shadowSyntheticVersion, Status: plan.Result.Status, Operation: "synthetic_execute",
			ProductionRouteEnabled: false, StartupReconciled: true, PendingGeneration: stateStore.hasPending(),
			KeychainItemCount: keychain.count(), Events: log.snapshot(),
		}
		if err := json.NewEncoder(stdout).Encode(summary); err != nil {
			return writeShadowSyntheticFailure(stderr, "output")
		}
		return 3
	}
	result, err := owner.Execute(ctx, plan.Challenge.ChallengeID, shadowSyntheticRequestID, true, binding)
	if err != nil || result.Status != "ready" || result.ErrorCode != contract.ErrorNone ||
		result.GenerationID != shadowSyntheticReadyID || stateStore.readyID() != shadowSyntheticReadyID ||
		stateStore.hasPending() || keychain.count() != 1 {
		return writeShadowSyntheticFailure(stderr, "owner_execute")
	}
	_, approvalFound, approvalErr := approvalStore.Load(ctx)
	if approvalErr != nil || approvalFound {
		return writeShadowSyntheticFailure(stderr, "approval_consumption")
	}
	events := log.snapshot()
	if strings.Contains(strings.Join(events, "\n"), "discarded-synthetic-pending-secret") {
		return writeShadowSyntheticFailure(stderr, "secret_output")
	}
	summary := shadowSyntheticSummary{
		Version: shadowSyntheticVersion, Status: result.Status, Operation: "synthetic_execute",
		ProductionRouteEnabled: false, StartupReconciled: true, ApprovalConsumed: true,
		GenerationID: result.GenerationID, PendingGeneration: false,
		KeychainItemCount: keychain.count(), Events: events,
	}
	if err := json.NewEncoder(stdout).Encode(summary); err != nil {
		return writeShadowSyntheticFailure(stderr, "output")
	}
	return 0
}
