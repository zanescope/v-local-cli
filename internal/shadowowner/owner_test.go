package shadowowner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	approvalmodel "github.com/zanescope/v-local-cli/internal/shadowapproval"
	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
	publishmodel "github.com/zanescope/v-local-cli/internal/shadowpublish"
	verifymodel "github.com/zanescope/v-local-cli/internal/shadowverify"
)

const testCredential = "synthetic-owner-credential"

type fakeClock struct{ now uint64 }

func (value *fakeClock) NowNS() (uint64, error) { return value.now, nil }

type wallClock struct{ now int64 }

func (value *wallClock) NowUnix() int64 { return value.now }

type approvalStore struct{ challenge *contract.Challenge }

func (value *approvalStore) Load(context.Context) (contract.Challenge, bool, error) {
	if value.challenge == nil {
		return contract.Challenge{}, false, nil
	}
	return *value.challenge, true, nil
}
func (value *approvalStore) Save(_ context.Context, challenge contract.Challenge) error {
	copy := challenge
	value.challenge = &copy
	return nil
}
func (value *approvalStore) Remove(_ context.Context, id string) error {
	if value.challenge == nil || value.challenge.ChallengeID != id {
		return errors.New("challenge mismatch")
	}
	value.challenge = nil
	return nil
}

type providerFixture struct {
	qualification contract.Qualification
	ready         contract.Result
	credential    []byte
	log           *[]string
	execution     contract.Request
	clock         *fakeClock
	advanceNS     uint64
}

func (value *providerFixture) Qualify(_ context.Context, request contract.Request) (contract.Result, error) {
	*value.log = append(*value.log, "provider:qualify")
	result := contract.Result{
		Version: contract.Version, RequestID: request.RequestID, Status: "qualified",
		ErrorCode: contract.ErrorNone, Qualification: &value.qualification,
	}
	return result, nil
}
func (value *providerFixture) Execute(_ context.Context, request contract.Request) (contract.Result, Credential, error) {
	*value.log = append(*value.log, "provider:cleanup_complete")
	value.execution = request
	value.clock.now += value.advanceNS
	result := value.ready
	result.RequestID = request.RequestID
	return result, Credential{
		Candidate: append([]byte(nil), value.credential...),
	}, nil
}

type probeFixture struct {
	buildSet  string
	residue   string
	log       *[]string
	resources int
}

func (value *probeFixture) BuildSetDigest(context.Context) (string, error) {
	return value.buildSet, nil
}
func (value *probeFixture) ResourceAbsent(_ context.Context, _ string, resource contract.ResourceBinding) (bool, error) {
	if value.resources == 0 {
		*value.log = append(*value.log, "cli:independent_verify")
	}
	value.resources++
	return resource.Kind != value.residue, nil
}
func (*probeFixture) ProcessAbsent(context.Context, contract.ProcessBinding) (bool, error) {
	return true, nil
}
func (*probeFixture) LaunchRegistrationAbsent(context.Context, string, contract.ResourceBinding) (bool, error) {
	return true, nil
}
func (*probeFixture) SourceUnchanged(context.Context, string) (bool, error) { return true, nil }
func (*probeFixture) SecurityPostureExpected(context.Context) (bool, error) { return true, nil }

type validatorFixture struct {
	log       *[]string
	fail      bool
	clock     *fakeClock
	advanceNS uint64
}

func (value validatorFixture) ValidateAndDerive(_ context.Context, request contract.Request, result contract.Result, credential []byte) ([]byte, error) {
	*value.log = append(*value.log, "cli:candidate_validate")
	if value.clock != nil {
		value.clock.now += value.advanceNS
	}
	if value.fail || string(credential) != testCredential || request.RequestID != result.RequestID || result.Status != "ready" {
		return nil, errors.New("invalid candidate")
	}
	return append([]byte(nil), credential...), nil
}

type stateFixture struct {
	ready   *publishmodel.GenerationState
	pending *publishmodel.GenerationState
	log     *[]string
}

func (value *stateFixture) LoadReady(context.Context, string) (publishmodel.GenerationState, bool, error) {
	if value.ready == nil {
		return publishmodel.GenerationState{}, false, nil
	}
	return *value.ready, true, nil
}
func (value *stateFixture) LoadPending(context.Context, string) (publishmodel.GenerationState, bool, error) {
	if value.pending == nil {
		return publishmodel.GenerationState{}, false, nil
	}
	return *value.pending, true, nil
}
func (value *stateFixture) SaveReady(_ context.Context, state publishmodel.GenerationState) error {
	*value.log = append(*value.log, "state:ready")
	copy := state
	value.ready = &copy
	return nil
}
func (value *stateFixture) SavePending(_ context.Context, state publishmodel.GenerationState) error {
	*value.log = append(*value.log, "state:pending")
	copy := state
	value.pending = &copy
	return nil
}
func (value *stateFixture) RemovePending(context.Context, string) error {
	*value.log = append(*value.log, "state:pending_removed")
	value.pending = nil
	return nil
}

type keychainFixture struct {
	values map[string][]byte
	log    *[]string
}

func (value *keychainFixture) Put(_ context.Context, account, generation string, secret []byte) error {
	*value.log = append(*value.log, "keychain:put")
	value.values[account+generation] = append([]byte(nil), secret...)
	return nil
}
func (value *keychainFixture) Get(_ context.Context, account, generation string) ([]byte, bool, error) {
	secret, found := value.values[account+generation]
	return append([]byte(nil), secret...), found, nil
}
func (value *keychainFixture) Delete(_ context.Context, account, generation string) error {
	delete(value.values, account+generation)
	return nil
}

type lockFixture struct{ held bool }

func (value *lockFixture) Acquire(context.Context, string) (func() error, error) {
	if value.held {
		return nil, errors.New("busy")
	}
	value.held = true
	return func() error { value.held = false; return nil }, nil
}

func golden(t *testing.T) contract.GoldenVectors {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "testdata", "shadow-contract-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors contract.GoldenVectors
	if err := contract.DecodeStrict(payload, &vectors); err != nil || vectors.Validate() != nil {
		t.Fatalf("golden vectors invalid: %v", err)
	}
	return vectors
}

type ownerHarness struct {
	owner    *Owner
	clock    *fakeClock
	provider *providerFixture
	probe    *probeFixture
	state    *stateFixture
	keychain *keychainFixture
	binding  Binding
	log      *[]string
}

func harness(t *testing.T) ownerHarness {
	t.Helper()
	vectors := golden(t)
	clock := &fakeClock{now: vectors.ExecuteRequest.Deadline.T0NS}
	log := &[]string{}
	provider := &providerFixture{
		qualification: vectors.Qualification, ready: vectors.ReadyResult,
		credential: []byte(testCredential), log: log, clock: clock,
	}
	probe := &probeFixture{buildSet: vectors.ExecuteRequest.BuildSetDigest, log: log}
	state := &stateFixture{log: log}
	keychain := &keychainFixture{values: map[string][]byte{}, log: log}
	publisher := &publishmodel.Publisher{
		Clock: clock, State: state, Keychain: keychain, Locker: &lockFixture{},
		NewID: func() (string, error) { return "77777777777777777777777777777777", nil },
	}
	approval := &approvalmodel.Manager{
		Store: &approvalStore{}, Wall: &wallClock{now: 1_800_000_000}, Raw: clock,
		NewID: func() (string, error) { return vectors.Challenge.ChallengeID, nil },
	}
	owner := &Owner{
		Operation: "synthetic_execute", Clock: clock, Provider: provider, Approval: approval,
		Verifier: verifymodel.Verifier{Clock: clock, Probe: probe}, Validator: validatorFixture{log: log, clock: clock}, Publisher: publisher,
	}
	return ownerHarness{
		owner: owner, clock: clock, provider: provider, probe: probe, state: state, keychain: keychain, log: log,
		binding: Binding{
			BuildSetDigest:            vectors.Qualification.BuildSetDigest,
			SourceQualificationDigest: vectors.Qualification.SourceQualificationDigest,
			CleanupRoute:              vectors.Qualification.CleanupRoute, AccountBindingID: vectors.Qualification.AccountBindingID,
			OptionsDigest: vectors.Qualification.OptionsDigest,
		},
	}
}

func TestSyntheticOwnerVerticalSliceReturnsReadyAfterCleanupVerifyAndPublication(t *testing.T) {
	h := harness(t)
	plan, err := h.owner.Plan(context.Background(), "88888888888888888888888888888888", h.binding)
	if err != nil || plan.Result.Status != "action_required" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	result, err := h.owner.Execute(context.Background(), plan.Challenge.ChallengeID,
		"99999999999999999999999999999999", true, h.binding)
	if err != nil || result.Status != "ready" || result.GenerationID == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	order := strings.Join(*h.log, "\n")
	for _, expected := range []string{
		"provider:cleanup_complete\ncli:independent_verify",
		"cli:independent_verify",
		"cli:candidate_validate\nstate:pending\nkeychain:put\nstate:ready",
	} {
		if !strings.Contains(order, expected) {
			t.Fatalf("vertical order lacks %q:\n%s", expected, order)
		}
	}
	if h.state.pending != nil || h.state.ready == nil || len(h.keychain.values) != 1 {
		t.Fatalf("publication is inconsistent: ready=%+v pending=%+v items=%d", h.state.ready, h.state.pending, len(h.keychain.values))
	}
	if h.provider.execution.Deadline == nil || h.provider.execution.Deadline.T0NS != h.clock.now {
		t.Fatal("CLI and Provider did not observe the same immutable T0")
	}
	if h.clock.now-h.provider.execution.Deadline.T0NS >= 10_000_000_000 {
		t.Fatal("synthetic vertical slice missed the under-10-second target")
	}
	payload, _ := json.Marshal(h.state.ready)
	if strings.Contains(string(payload), testCredential) {
		t.Fatal("credential entered non-secret ready state")
	}
}

func TestOwnerNeverPublishesBeforeIndependentVerificationAndCandidateValidation(t *testing.T) {
	t.Run("residue", func(t *testing.T) {
		h := harness(t)
		h.probe.residue = "socket"
		plan, _ := h.owner.Plan(context.Background(), "88888888888888888888888888888888", h.binding)
		result, err := h.owner.Execute(context.Background(), plan.Challenge.ChallengeID,
			"99999999999999999999999999999999", true, h.binding)
		if err != nil || result.ErrorCode != contract.ErrorCleanupVerification || len(h.keychain.values) != 0 || h.state.pending != nil {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
	t.Run("candidate", func(t *testing.T) {
		h := harness(t)
		h.owner.Validator = validatorFixture{log: h.log, fail: true, clock: h.clock}
		plan, _ := h.owner.Plan(context.Background(), "88888888888888888888888888888888", h.binding)
		result, err := h.owner.Execute(context.Background(), plan.Challenge.ChallengeID,
			"99999999999999999999999999999999", true, h.binding)
		if err != nil || result.ErrorCode != contract.ErrorCredentialInvalid || len(h.keychain.values) != 0 || h.state.pending != nil {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
}

type delayedPublisher struct {
	inner    *publishmodel.Publisher
	clock    *fakeClock
	deadline uint64
}

func (value delayedPublisher) Publish(ctx context.Context, request publishmodel.Request, credential []byte) (publishmodel.GenerationState, error) {
	state, err := value.inner.Publish(ctx, request, credential)
	if err == nil {
		value.clock.now = value.deadline
	}
	return state, err
}

func TestOwnerEnforcesProviderT100CLIT108AndReturnT120Boundaries(t *testing.T) {
	t.Run("provider T100", func(t *testing.T) {
		h := harness(t)
		h.provider.advanceNS = contract.ProviderCleanupWindowNS
		plan, _ := h.owner.Plan(context.Background(), "88888888888888888888888888888888", h.binding)
		result, err := h.owner.Execute(context.Background(), plan.Challenge.ChallengeID,
			"99999999999999999999999999999999", true, h.binding)
		if err != nil || result.ErrorCode != contract.ErrorDeadlineProviderCleanup || len(h.keychain.values) != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
	t.Run("CLI T108", func(t *testing.T) {
		h := harness(t)
		h.owner.Validator = validatorFixture{log: h.log, clock: h.clock, advanceNS: contract.CLIVerifyWindowNS}
		plan, _ := h.owner.Plan(context.Background(), "88888888888888888888888888888888", h.binding)
		result, err := h.owner.Execute(context.Background(), plan.Challenge.ChallengeID,
			"99999999999999999999999999999999", true, h.binding)
		if err != nil || result.ErrorCode != contract.ErrorDeadlineCLIVerify || len(h.keychain.values) != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
	t.Run("return T120", func(t *testing.T) {
		h := harness(t)
		actual := h.owner.Publisher.(*publishmodel.Publisher)
		plan, _ := h.owner.Plan(context.Background(), "88888888888888888888888888888888", h.binding)
		h.owner.Publisher = delayedPublisher{inner: actual, clock: h.clock, deadline: contract.NewDeadline(h.clock.now).ReturnNS}
		result, err := h.owner.Execute(context.Background(), plan.Challenge.ChallengeID,
			"99999999999999999999999999999999", true, h.binding)
		if err != nil || result.ErrorCode != contract.ErrorDeadlinePublication || result.GenerationID == "" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
}
