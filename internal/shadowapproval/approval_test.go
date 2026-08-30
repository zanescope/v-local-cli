package shadowapproval

import (
	"context"
	"errors"
	"testing"

	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
)

const (
	testBuild   = "1111111111111111111111111111111111111111111111111111111111111111"
	testSource  = "2222222222222222222222222222222222222222222222222222222222222222"
	testOptions = "3333333333333333333333333333333333333333333333333333333333333333"
	testID      = "00112233445566778899aabbccddeeff"
)

type memoryStore struct {
	challenge  *contract.Challenge
	failRemove bool
	onLoad     func()
}

func (value *memoryStore) Load(context.Context) (contract.Challenge, bool, error) {
	if value.onLoad != nil {
		value.onLoad()
	}
	if value.challenge == nil {
		return contract.Challenge{}, false, nil
	}
	return *value.challenge, true, nil
}
func (value *memoryStore) Save(_ context.Context, challenge contract.Challenge) error {
	copy := challenge
	value.challenge = &copy
	return nil
}
func (value *memoryStore) Remove(_ context.Context, challengeID string) error {
	if value.failRemove {
		return errors.New("injected remove failure")
	}
	if value.challenge == nil || value.challenge.ChallengeID != challengeID {
		return errors.New("challenge identity mismatch")
	}
	value.challenge = nil
	return nil
}

type fakeWall struct{ now int64 }

func (value *fakeWall) NowUnix() int64 { return value.now }

type fakeRaw struct{ now uint64 }

func (value *fakeRaw) NowNS() (uint64, error) { return value.now, nil }

func qualification() contract.Qualification {
	return contract.Qualification{
		Version: contract.Version, BuildSetDigest: testBuild, SourceQualificationDigest: testSource,
		CleanupRoute: contract.CleanupRouteDirect, AccountBindingID: "aabbccddeeff0011", OptionsDigest: testOptions,
		SourceVersion: "4.1.11", SourceBuild: "26000",
	}
}

func manager() (*Manager, *memoryStore, *fakeWall, *fakeRaw) {
	store := &memoryStore{}
	wall := &fakeWall{now: 1_800_000_000}
	raw := &fakeRaw{now: 1_000_000_000}
	return &Manager{Store: store, Wall: wall, Raw: raw, NewID: func() (string, error) { return testID, nil }}, store, wall, raw
}

func TestChallengeIsBoundSingleUseAndEstablishesOneT0(t *testing.T) {
	manager, store, _, raw := manager()
	challenge, err := manager.Issue(context.Background(), qualification(), "synthetic_execute")
	if err != nil {
		t.Fatal(err)
	}
	request, err := manager.Consume(context.Background(), challenge.ChallengeID,
		"9999aaaabbbbccccddddeeeeffff0000", true, BindingFromQualification(qualification()), "synthetic_execute")
	if err != nil {
		t.Fatal(err)
	}
	if store.challenge != nil || request.Deadline.T0NS != raw.now || request.Deadline != nil && request.Deadline.ReturnNS != raw.now+contract.ReturnWindowNS {
		t.Fatalf("challenge was not consumed into the fixed deadline: request=%+v stored=%+v", request, store.challenge)
	}
	if _, err := manager.Consume(context.Background(), challenge.ChallengeID,
		"8888aaaabbbbccccddddeeeeffff0000", true, BindingFromQualification(qualification()), "synthetic_execute"); err == nil {
		t.Fatal("single-use Shadow challenge was replayed")
	}
}

func TestConfirmationT0PrecedesChallengeStorageLatency(t *testing.T) {
	manager, store, _, raw := manager()
	challenge, err := manager.Issue(context.Background(), qualification(), "synthetic_execute")
	if err != nil {
		t.Fatal(err)
	}
	confirmedAt := raw.now
	store.onLoad = func() { raw.now += 5_000_000_000 }
	request, err := manager.Consume(context.Background(), challenge.ChallengeID,
		"9999aaaabbbbccccddddeeeeffff0000", true, BindingFromQualification(qualification()), "synthetic_execute")
	if err != nil {
		t.Fatal(err)
	}
	if request.Deadline == nil || request.Deadline.T0NS != confirmedAt || raw.now == confirmedAt {
		t.Fatalf("confirmation deadline was reset after storage work: deadline=%+v clock=%d", request.Deadline, raw.now)
	}
}

func TestChallengeCannotBypassConfirmationDriftExpiryOrRemovalFailure(t *testing.T) {
	t.Run("operation", func(t *testing.T) {
		manager, store, _, _ := manager()
		challenge, _ := manager.Issue(context.Background(), qualification(), "synthetic_execute")
		if _, err := manager.Consume(context.Background(), challenge.ChallengeID,
			"9999aaaabbbbccccddddeeeeffff0000", true, BindingFromQualification(qualification()), "execute"); err == nil {
			t.Fatal("synthetic approval challenge authorized production execution")
		}
		if store.challenge == nil {
			t.Fatal("operation mismatch consumed the challenge")
		}
	})
	t.Run("confirmation", func(t *testing.T) {
		manager, _, _, _ := manager()
		challenge, _ := manager.Issue(context.Background(), qualification(), "synthetic_execute")
		if _, err := manager.Consume(context.Background(), challenge.ChallengeID,
			"9999aaaabbbbccccddddeeeeffff0000", false, BindingFromQualification(qualification()), "synthetic_execute"); err == nil {
			t.Fatal("unconfirmed Shadow challenge was consumed")
		}
	})
	t.Run("drift", func(t *testing.T) {
		manager, _, _, _ := manager()
		challenge, _ := manager.Issue(context.Background(), qualification(), "synthetic_execute")
		binding := BindingFromQualification(qualification())
		binding.BuildSetDigest = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		if _, err := manager.Consume(context.Background(), challenge.ChallengeID,
			"9999aaaabbbbccccddddeeeeffff0000", true, binding, "synthetic_execute"); err == nil {
			t.Fatal("drifted Shadow approval binding was consumed")
		}
	})
	t.Run("expiry", func(t *testing.T) {
		manager, store, wall, _ := manager()
		challenge, _ := manager.Issue(context.Background(), qualification(), "synthetic_execute")
		wall.now = challenge.ExpiresAtUnix
		if _, err := manager.Consume(context.Background(), challenge.ChallengeID,
			"9999aaaabbbbccccddddeeeeffff0000", true, BindingFromQualification(qualification()), "synthetic_execute"); err == nil {
			t.Fatal("expired Shadow challenge was consumed")
		}
		if store.challenge != nil {
			t.Fatal("expired Shadow challenge was retained")
		}
	})
	t.Run("remove failure", func(t *testing.T) {
		manager, store, _, _ := manager()
		challenge, _ := manager.Issue(context.Background(), qualification(), "synthetic_execute")
		store.failRemove = true
		if _, err := manager.Consume(context.Background(), challenge.ChallengeID,
			"9999aaaabbbbccccddddeeeeffff0000", true, BindingFromQualification(qualification()), "synthetic_execute"); err == nil {
			t.Fatal("challenge removal failure granted mutation authority")
		}
	})
}

func TestIssueReplacesFutureDatedChallengeAfterWallClockRollback(t *testing.T) {
	manager, store, wall, _ := manager()
	existing, err := manager.Issue(context.Background(), qualification(), "synthetic_execute")
	if err != nil {
		t.Fatal(err)
	}
	wall.now = existing.IssuedAtUnix - 1
	reissued, err := manager.Issue(context.Background(), qualification(), "synthetic_execute")
	if err != nil {
		t.Fatal(err)
	}
	if reissued.IssuedAtUnix != wall.now || store.challenge == nil || store.challenge.IssuedAtUnix != wall.now {
		t.Fatalf("future-dated challenge was reused: existing=%+v reissued=%+v", existing, reissued)
	}
}

func TestManagerRejectsNilContext(t *testing.T) {
	manager, _, _, _ := manager()
	if _, err := manager.Issue(nil, qualification(), "synthetic_execute"); err == nil {
		t.Fatal("nil issue context was accepted")
	}
	if _, err := manager.Consume(nil, testID, "9999aaaabbbbccccddddeeeeffff0000", true,
		BindingFromQualification(qualification()), "synthetic_execute"); err == nil {
		t.Fatal("nil consume context was accepted")
	}
}
