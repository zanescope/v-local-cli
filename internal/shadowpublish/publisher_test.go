package shadowpublish

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
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
	ready                       *GenerationState
	pending                     *GenerationState
	log                         *[]string
	failSaveReady               int
	commitThenFailSaveReady     int
	commitThenFailSavePending   int
	failRemovePending           int
	commitThenFailRemovePending int
	serialized                  [][]byte
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
	if value.commitThenFailSaveReady > 0 {
		value.commitThenFailSaveReady--
		return errors.New("injected post-commit ready failure")
	}
	return nil
}
func (value *memoryState) SavePending(_ context.Context, state GenerationState) error {
	*value.log = append(*value.log, "state:save_pending:"+state.GenerationID)
	payload, _ := json.Marshal(state)
	value.serialized = append(value.serialized, payload)
	copy := state
	value.pending = &copy
	if value.commitThenFailSavePending > 0 {
		value.commitThenFailSavePending--
		return errors.New("injected post-commit pending failure")
	}
	return nil
}
func (value *memoryState) RemovePending(context.Context, string) error {
	*value.log = append(*value.log, "state:remove_pending")
	if value.failRemovePending > 0 {
		value.failRemovePending--
		return errors.New("injected pending removal failure")
	}
	value.pending = nil
	if value.commitThenFailRemovePending > 0 {
		value.commitThenFailRemovePending--
		return errors.New("injected post-removal failure")
	}
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

func TestCrashBoundariesConvergeWithoutPromotingPending(t *testing.T) {
	tests := []struct {
		name      string
		inject    func(*memoryState)
		committed bool
	}{
		{
			name:   "pending state committed before error",
			inject: func(state *memoryState) { state.commitThenFailSavePending = 1 },
		},
		{
			name:      "ready state committed before error",
			inject:    func(state *memoryState) { state.commitThenFailSaveReady = 1 },
			committed: true,
		},
		{
			name:      "pending removal fails before deletion",
			inject:    func(state *memoryState) { state.failRemovePending = 1 },
			committed: true,
		},
		{
			name:      "pending removal commits before error",
			inject:    func(state *memoryState) { state.commitThenFailRemovePending = 1 },
			committed: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			publisher, clock, state, keychain, _ := harness(t)
			test.inject(state)
			published, publishErr := publisher.Publish(context.Background(), request(clock), []byte(testSecret))
			if test.committed {
				if !Committed(publishErr) || published.GenerationID != testNew {
					t.Fatalf("post-ready crash was not classified committed: state=%+v err=%v", published, publishErr)
				}
			} else if !errors.Is(publishErr, ErrReconciliationPending) || Committed(publishErr) || published.GenerationID != "" {
				t.Fatalf("pre-ready crash crossed the commit boundary: state=%+v err=%v", published, publishErr)
			}

			reconciled, found, reconcileErr := publisher.Reconcile(
				context.Background(), testAccount, request(clock).Deadline.MutationStopNS,
			)
			if reconcileErr != nil || !found {
				t.Fatalf("startup reconciliation failed: ready=%+v found=%v err=%v", reconciled, found, reconcileErr)
			}
			wantGeneration := testOld
			wantSecret := "old"
			if test.committed {
				wantGeneration = testNew
				wantSecret = testSecret
			}
			if reconciled.GenerationID != wantGeneration || reconciled.ObsoleteGenerationID != "" || state.pending != nil ||
				state.ready == nil || state.ready.GenerationID != wantGeneration || len(keychain.values) != 1 ||
				string(keychain.values[key(testAccount, wantGeneration)]) != wantSecret {
				t.Fatalf("crash recovery did not converge: ready=%+v pending=%+v keys=%v", state.ready, state.pending, keychain.values)
			}
			if _, found := keychain.values[key(testAccount, map[bool]string{true: testOld, false: testNew}[test.committed])]; found {
				t.Fatal("reconciliation retained the exact generation that should have been removed")
			}
		})
	}
}

type concurrentState struct {
	mu      sync.Mutex
	ready   *GenerationState
	pending *GenerationState
}

func (value *concurrentState) LoadReady(context.Context, string) (GenerationState, bool, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.ready == nil {
		return GenerationState{}, false, nil
	}
	return *value.ready, true, nil
}
func (value *concurrentState) LoadPending(context.Context, string) (GenerationState, bool, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.pending == nil {
		return GenerationState{}, false, nil
	}
	return *value.pending, true, nil
}
func (value *concurrentState) SaveReady(_ context.Context, state GenerationState) error {
	value.mu.Lock()
	defer value.mu.Unlock()
	copy := state
	value.ready = &copy
	return nil
}
func (value *concurrentState) SavePending(_ context.Context, state GenerationState) error {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.pending != nil {
		return errors.New("pending exists")
	}
	copy := state
	value.pending = &copy
	return nil
}
func (value *concurrentState) RemovePending(context.Context, string) error {
	value.mu.Lock()
	defer value.mu.Unlock()
	value.pending = nil
	return nil
}
func (value *concurrentState) snapshot() (*GenerationState, *GenerationState) {
	value.mu.Lock()
	defer value.mu.Unlock()
	var readyCopy *GenerationState
	var pendingCopy *GenerationState
	if value.ready != nil {
		copy := *value.ready
		readyCopy = &copy
	}
	if value.pending != nil {
		copy := *value.pending
		pendingCopy = &copy
	}
	return readyCopy, pendingCopy
}

type concurrentKeychain struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (value *concurrentKeychain) Put(_ context.Context, accountID, generationID string, secret []byte) error {
	value.mu.Lock()
	defer value.mu.Unlock()
	value.values[key(accountID, generationID)] = append([]byte(nil), secret...)
	return nil
}
func (value *concurrentKeychain) Get(_ context.Context, accountID, generationID string) ([]byte, bool, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	secret, found := value.values[key(accountID, generationID)]
	return append([]byte(nil), secret...), found, nil
}
func (value *concurrentKeychain) Delete(_ context.Context, accountID, generationID string) error {
	value.mu.Lock()
	defer value.mu.Unlock()
	delete(value.values, key(accountID, generationID))
	return nil
}
func (value *concurrentKeychain) snapshot() map[string][]byte {
	value.mu.Lock()
	defer value.mu.Unlock()
	result := make(map[string][]byte, len(value.values))
	for name, secret := range value.values {
		result[name] = append([]byte(nil), secret...)
	}
	return result
}

type concurrentLocker struct{ mu sync.Mutex }

func (value *concurrentLocker) Acquire(context.Context, string) (func() error, error) {
	value.mu.Lock()
	return func() error { value.mu.Unlock(); return nil }, nil
}

func TestConcurrentPublishAndStartupReconcileSerializeToOneReadyGeneration(t *testing.T) {
	clock := &fakeClock{now: 1_000_000_000}
	old := oldReady()
	state := &concurrentState{ready: &old}
	keychain := &concurrentKeychain{values: map[string][]byte{key(testAccount, testOld): []byte("old")}}
	publisher := &Publisher{
		Clock: clock, State: state, Keychain: keychain, Locker: &concurrentLocker{},
		NewID: func() (string, error) { return testNew, nil },
	}
	req := request(clock)
	const goroutines = 32
	start := make(chan struct{})
	errorsChannel := make(chan error, goroutines)
	var wait sync.WaitGroup
	for index := 0; index < goroutines; index++ {
		wait.Add(1)
		go func(publish bool) {
			defer wait.Done()
			<-start
			if publish {
				_, err := publisher.Publish(context.Background(), req, []byte(testSecret))
				if err != nil && !errors.Is(err, ErrGenerationCollision) {
					errorsChannel <- err
				}
				return
			}
			_, _, err := publisher.Reconcile(context.Background(), testAccount, req.Deadline.MutationStopNS)
			if err != nil {
				errorsChannel <- err
			}
		}(index%2 == 0)
	}
	close(start)
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Fatalf("concurrent operation failed: %v", err)
	}
	ready, found, err := publisher.Reconcile(context.Background(), testAccount, req.Deadline.MutationStopNS)
	if err != nil || !found || ready.GenerationID != testNew || ready.ObsoleteGenerationID != "" {
		t.Fatalf("final reconcile ready=%+v found=%v err=%v", ready, found, err)
	}
	storedReady, pending := state.snapshot()
	values := keychain.snapshot()
	if storedReady == nil || storedReady.GenerationID != testNew || pending != nil || len(values) != 1 ||
		string(values[key(testAccount, testNew)]) != testSecret {
		t.Fatalf("concurrent final state is inconsistent: ready=%+v pending=%+v values=%v", storedReady, pending, values)
	}
}
