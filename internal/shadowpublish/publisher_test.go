package shadowpublish

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
)

const (
	testAccount = "aabbccddeeff0011"
	testBuild   = "1111111111111111111111111111111111111111111111111111111111111111"
	testAttempt = "22222222222222222222222222222222"
	testOld     = "33333333333333333333333333333333"
	testNew     = "44444444444444444444444444444444"
	testSecret  = "synthetic-publication-secret"
)

type fakeClock struct{ now uint64 }

func (value *fakeClock) NowNS() (uint64, error) { return value.now, nil }

type memoryLocker struct {
	held        bool
	failRelease bool
}

func (value *memoryLocker) Acquire(_ context.Context, _ string) (func() error, error) {
	if value.held {
		return nil, errors.New("locked")
	}
	value.held = true
	return func() error {
		value.held = false
		if value.failRelease {
			return errors.New("injected release failure")
		}
		return nil
	}, nil
}

type memoryState struct {
	ready         *GenerationState
	pending       *GenerationState
	log           *[]string
	failSaveReady int
	serialized    [][]byte
}

func (value *memoryState) LoadReady(context.Context, string) (GenerationState, bool, error) {
	if value.ready == nil {
		return GenerationState{}, false, nil
	}
	return *value.ready, true, nil
}
func (value *memoryState) LoadPending(context.Context, string) (GenerationState, bool, error) {
	if value.pending == nil {
		return GenerationState{}, false, nil
	}
	return *value.pending, true, nil
}
func (value *memoryState) SaveReady(_ context.Context, state GenerationState) error {
	*value.log = append(*value.log, "state:save_ready:"+state.GenerationID+":"+state.ObsoleteGenerationID)
	payload, _ := json.Marshal(state)
	value.serialized = append(value.serialized, payload)
	if value.failSaveReady > 0 {
		value.failSaveReady--
		return errors.New("injected ready failure")
	}
	copy := state
	value.ready = &copy
	return nil
}
func (value *memoryState) SavePending(_ context.Context, state GenerationState) error {
	*value.log = append(*value.log, "state:save_pending:"+state.GenerationID)
	payload, _ := json.Marshal(state)
	value.serialized = append(value.serialized, payload)
	copy := state
	value.pending = &copy
	return nil
}
func (value *memoryState) RemovePending(context.Context, string) error {
	*value.log = append(*value.log, "state:remove_pending")
	value.pending = nil
	return nil
}

type memoryKeychain struct {
	values         map[string][]byte
	ghost          map[string][]byte
	log            *[]string
	uncertainPut   bool
	failDeleteOnce map[string]bool
	clock          *fakeClock
	advancePutNS   uint64
}

func key(account, generation string) string { return account + ":" + generation }

func (value *memoryKeychain) Put(_ context.Context, account, generation string, secret []byte) error {
	*value.log = append(*value.log, "keychain:put:"+generation)
	value.values[key(account, generation)] = append([]byte(nil), secret...)
	value.clock.now += value.advancePutNS
	if value.uncertainPut {
		value.uncertainPut = false
		return errors.New("uncertain put")
	}
	return nil
}
func (value *memoryKeychain) Get(_ context.Context, account, generation string) ([]byte, bool, error) {
	*value.log = append(*value.log, "keychain:get:"+generation)
	secret, found := value.values[key(account, generation)]
	if !found {
		if payload, exists := value.ghost[key(account, generation)]; exists {
			return append([]byte(nil), payload...), false, nil
		}
	}
	return append([]byte(nil), secret...), found, nil
}
func (value *memoryKeychain) Delete(_ context.Context, account, generation string) error {
	*value.log = append(*value.log, "keychain:delete:"+generation)
	if value.failDeleteOnce[generation] {
		delete(value.failDeleteOnce, generation)
		return errors.New("uncertain delete")
	}
	delete(value.values, key(account, generation))
	return nil
}

func oldReady() GenerationState {
	return GenerationState{
		Version: StateVersion, Status: "ready", AccountBindingID: testAccount,
		GenerationID: testOld, BuildSetDigest: testBuild, AttemptID: "55555555555555555555555555555555",
	}
}

func harness(t *testing.T) (*Publisher, *fakeClock, *memoryState, *memoryKeychain, *[]string) {
	t.Helper()
	clock := &fakeClock{now: 1_000_000_000}
	log := &[]string{}
	ready := oldReady()
	state := &memoryState{ready: &ready, log: log}
	keychain := &memoryKeychain{
		values: map[string][]byte{key(testAccount, testOld): []byte("old")},
		ghost:  map[string][]byte{}, log: log, failDeleteOnce: map[string]bool{}, clock: clock,
	}
	publisher := &Publisher{
		Clock: clock, State: state, Keychain: keychain, Locker: &memoryLocker{},
		NewID: func() (string, error) { return testNew, nil },
	}
	return publisher, clock, state, keychain, log
}

func request(clock *fakeClock) Request {
	return Request{
		AccountBindingID: testAccount, BuildSetDigest: testBuild, AttemptID: testAttempt,
		Deadline: contract.NewDeadline(clock.now),
	}
}

func TestGenerationTransactionOrdersPendingKeychainReadyAndObsoleteCleanup(t *testing.T) {
	publisher, clock, state, keychain, log := harness(t)
	ready, err := publisher.Publish(context.Background(), request(clock), []byte(testSecret))
	if err != nil || ready.GenerationID != testNew || ready.ObsoleteGenerationID != "" {
		t.Fatalf("ready=%+v err=%v", ready, err)
	}
	want := []string{
		"keychain:get:" + testOld,
		"keychain:get:" + testNew,
		"state:save_pending:" + testNew,
		"keychain:put:" + testNew,
		"keychain:get:" + testNew,
		"state:save_ready:" + testNew + ":" + testOld,
		"state:remove_pending",
		"keychain:delete:" + testOld,
		"keychain:get:" + testOld,
		"state:save_ready:" + testNew + ":",
	}
	if strings.Join(*log, "\n") != strings.Join(want, "\n") {
		t.Fatalf("transaction order:\n got %v\nwant %v", *log, want)
	}
	if state.pending != nil || state.ready == nil || state.ready.GenerationID != testNew {
		t.Fatalf("state not committed: %+v pending=%+v", state.ready, state.pending)
	}
	if secret := keychain.values[key(testAccount, testNew)]; string(secret) != testSecret || len(keychain.values) != 1 {
		t.Fatalf("Keychain did not retain exactly the new minimal credential: items=%d", len(keychain.values))
	}
	for _, payload := range state.serialized {
		if strings.Contains(string(payload), testSecret) {
			t.Fatal("secret entered durable generation state")
		}
	}
}

func TestGenerationIdentityCollisionNeverOverwritesUnownedKeychainItem(t *testing.T) {
	publisher, clock, state, keychain, log := harness(t)
	keychain.values[key(testAccount, testNew)] = []byte("unowned")
	if _, err := publisher.Publish(context.Background(), request(clock), []byte(testSecret)); !errors.Is(err, ErrGenerationCollision) {
		t.Fatalf("collision error=%v", err)
	}
	if state.pending != nil || state.ready == nil || state.ready.GenerationID != testOld ||
		string(keychain.values[key(testAccount, testNew)]) != "unowned" {
		t.Fatal("generation collision mutated durable state or the unowned Keychain item")
	}
	if strings.Contains(strings.Join(*log, "\n"), "keychain:put:"+testNew) {
		t.Fatal("generation collision reached Keychain Put")
	}
}

func TestGenerationIdentityRejectsPayloadWithFalsePresenceFlag(t *testing.T) {
	publisher, clock, state, keychain, log := harness(t)
	keychain.ghost[key(testAccount, testNew)] = []byte("inconsistent")
	if _, err := publisher.Publish(context.Background(), request(clock), []byte(testSecret)); !errors.Is(err, ErrGenerationCollision) {
		t.Fatalf("inconsistent collision error=%v", err)
	}
	if state.pending != nil || strings.Contains(strings.Join(*log, "\n"), "keychain:put:"+testNew) {
		t.Fatal("inconsistent Keychain presence reached mutation")
	}
}

func TestDefaultGenerationIDDoesNotMutatePublisherConfiguration(t *testing.T) {
	publisher, _, _, _, _ := harness(t)
	publisher.NewID = nil
	first, err := publisher.newID()
	if err != nil || !lowerHex(first, 16) {
		t.Fatalf("default generation ID is invalid: %q err=%v", first, err)
	}
	if publisher.NewID != nil {
		t.Fatal("default generation ID initialization mutated shared publisher configuration")
	}
}

func TestReconcileRequiresExactReadyPendingCommitBinding(t *testing.T) {
	publisher, clock, state, keychain, log := harness(t)
	ready := GenerationState{
		Version: StateVersion, Status: "ready", AccountBindingID: testAccount,
		GenerationID: testNew, BuildSetDigest: testBuild, AttemptID: testAttempt,
		ObsoleteGenerationID: testOld,
	}
	pending := GenerationState{
		Version: StateVersion, Status: "pending", AccountBindingID: testAccount,
		GenerationID: testNew, BuildSetDigest: testBuild,
		AttemptID: "66666666666666666666666666666666", PreviousGenerationID: testOld,
	}
	state.ready = &ready
	state.pending = &pending
	keychain.values[key(testAccount, testNew)] = []byte(testSecret)
	if _, _, err := publisher.Reconcile(context.Background(), testAccount, request(clock).Deadline.MutationStopNS); err == nil {
		t.Fatal("mismatched ready/pending commit binding was reconciled")
	}
	if state.pending == nil || state.ready.ObsoleteGenerationID != testOld || len(*log) != 0 || len(keychain.values) != 2 {
		t.Fatal("invalid cross-state binding was mutated before rejection")
	}
}

func TestReconcileCompletesExactReadyPendingCommit(t *testing.T) {
	publisher, clock, state, keychain, _ := harness(t)
	ready := GenerationState{
		Version: StateVersion, Status: "ready", AccountBindingID: testAccount,
		GenerationID: testNew, BuildSetDigest: testBuild, AttemptID: testAttempt,
		ObsoleteGenerationID: testOld,
	}
	pending := GenerationState{
		Version: StateVersion, Status: "pending", AccountBindingID: testAccount,
		GenerationID: testNew, BuildSetDigest: testBuild, AttemptID: testAttempt,
		PreviousGenerationID: testOld,
	}
	state.ready = &ready
	state.pending = &pending
	keychain.values[key(testAccount, testNew)] = []byte(testSecret)
	reconciled, found, err := publisher.Reconcile(context.Background(), testAccount, request(clock).Deadline.MutationStopNS)
	if err != nil || !found || reconciled.GenerationID != testNew || reconciled.ObsoleteGenerationID != "" {
		t.Fatalf("reconciled=%+v found=%v err=%v", reconciled, found, err)
	}
	if state.pending != nil || len(keychain.values) != 1 || string(keychain.values[key(testAccount, testNew)]) != testSecret {
		t.Fatal("exact committed generation was not reconciled without damaging the current credential")
	}
}

func TestCommittedPublicationReportsLockReleaseFailure(t *testing.T) {
	publisher, clock, state, _, _ := harness(t)
	locker := publisher.Locker.(*memoryLocker)
	locker.failRelease = true
	committed, err := publisher.Publish(context.Background(), request(clock), []byte(testSecret))
	if !Committed(err) || committed.GenerationID != testNew || state.ready == nil || state.ready.GenerationID != testNew || locker.held {
		t.Fatalf("committed=%+v ready=%+v held=%v err=%v", committed, state.ready, locker.held, err)
	}
}

func TestPendingGenerationCanOnlyRollBackAndNeverPromotes(t *testing.T) {
	publisher, clock, state, keychain, _ := harness(t)
	state.failSaveReady = 1
	if _, err := publisher.Publish(context.Background(), request(clock), []byte(testSecret)); !errors.Is(err, ErrReconciliationPending) {
		t.Fatalf("ready commit failure=%v", err)
	}
	if state.pending == nil || string(keychain.values[key(testAccount, testNew)]) != testSecret {
		t.Fatal("uncertain generation was not exactly recoverable")
	}
	ready, found, err := publisher.Reconcile(context.Background(), testAccount, request(clock).Deadline.MutationStopNS)
	if err != nil || !found || ready.GenerationID != testOld {
		t.Fatalf("rollback reconciliation ready=%+v found=%v err=%v", ready, found, err)
	}
	if state.pending != nil {
		t.Fatal("pending generation was promoted or retained after exact rollback")
	}
	if _, found := keychain.values[key(testAccount, testNew)]; found {
		t.Fatal("rolled-back generation remains in Keychain")
	}
}

func TestUncertainKeychainPutRemainsNonReadyUntilExactReconciliation(t *testing.T) {
	publisher, clock, state, keychain, _ := harness(t)
	keychain.uncertainPut = true
	if _, err := publisher.Publish(context.Background(), request(clock), []byte(testSecret)); !errors.Is(err, ErrReconciliationPending) {
		t.Fatalf("uncertain put error=%v", err)
	}
	if state.pending == nil || state.ready.GenerationID != testOld {
		t.Fatal("uncertain put changed ready state or lost pending identity")
	}
	if _, _, err := publisher.Reconcile(context.Background(), testAccount, request(clock).Deadline.MutationStopNS); err != nil {
		t.Fatal(err)
	}
	if state.pending != nil || keychain.values[key(testAccount, testNew)] != nil {
		t.Fatal("uncertain generation was not exactly rolled back")
	}
}

func TestCommittedGenerationRetainsObsoleteIDUntilDeletionCanBeProved(t *testing.T) {
	publisher, clock, state, keychain, _ := harness(t)
	keychain.failDeleteOnce[testOld] = true
	committed, err := publisher.Publish(context.Background(), request(clock), []byte(testSecret))
	if !Committed(err) || committed.GenerationID != testNew || state.ready.ObsoleteGenerationID != testOld {
		t.Fatalf("committed=%+v state=%+v err=%v", committed, state.ready, err)
	}
	ready, found, err := publisher.Reconcile(context.Background(), testAccount, request(clock).Deadline.MutationStopNS)
	if err != nil || !found || ready.GenerationID != testNew || ready.ObsoleteGenerationID != "" {
		t.Fatalf("reconciled=%+v found=%v err=%v", ready, found, err)
	}
	if len(keychain.values) != 1 || string(keychain.values[key(testAccount, testNew)]) != testSecret {
		t.Fatal("obsolete generation cleanup damaged the committed generation")
	}
}

func TestPublicationCannotStartAfterT108OrMutateAfterT115(t *testing.T) {
	t.Run("T108", func(t *testing.T) {
		publisher, clock, state, _, log := harness(t)
		req := request(clock)
		clock.now = req.Deadline.CLIVerifyNS
		if _, err := publisher.Publish(context.Background(), req, []byte(testSecret)); !errors.Is(err, ErrDeadline) {
			t.Fatalf("deadline error=%v", err)
		}
		if state.pending != nil || len(*log) != 0 {
			t.Fatal("publication mutated state at or after T+108")
		}
	})
	t.Run("T115", func(t *testing.T) {
		publisher, clock, state, keychain, _ := harness(t)
		req := request(clock)
		keychain.advancePutNS = contract.MutationStopWindowNS
		if _, err := publisher.Publish(context.Background(), req, []byte(testSecret)); !errors.Is(err, ErrReconciliationPending) {
			t.Fatalf("mutation-stop error=%v", err)
		}
		if state.pending == nil || state.ready.GenerationID != testOld {
			t.Fatal("T+115 crossing did not remain fail-closed and recoverable")
		}
	})
}
