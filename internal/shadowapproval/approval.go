// Package shadowapproval owns the CLI-side, short-lived, single-use approval
// challenge. Consuming a challenge establishes the one immutable T0 deadline.
package shadowapproval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	clockmodel "github.com/zanescope/v-local-cli/internal/shadowclock"
	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
)

const ChallengeLifetimeSeconds int64 = 300

type Store interface {
	Load(context.Context) (contract.Challenge, bool, error)
	Save(context.Context, contract.Challenge) error
	Remove(context.Context, string) error
}

type WallClock interface {
	NowUnix() int64
}

type Manager struct {
	Store Store
	Wall  WallClock
	Raw   clockmodel.Clock
	NewID func() (string, error)
}

type Binding struct {
	BuildSetDigest            string
	SourceQualificationDigest string
	CleanupRoute              string
	AccountBindingID          string
	OptionsDigest             string
}

func BindingFromQualification(value contract.Qualification) Binding {
	return Binding{
		BuildSetDigest: value.BuildSetDigest, SourceQualificationDigest: value.SourceQualificationDigest,
		CleanupRoute: value.CleanupRoute, AccountBindingID: value.AccountBindingID, OptionsDigest: value.OptionsDigest,
	}
}

func (value Binding) matches(challenge contract.Challenge) bool {
	return challenge.BuildSetDigest == value.BuildSetDigest &&
		challenge.SourceQualificationDigest == value.SourceQualificationDigest &&
		challenge.CleanupRoute == value.CleanupRoute && challenge.AccountBindingID == value.AccountBindingID &&
		challenge.OptionsDigest == value.OptionsDigest
}

func randomID() (string, error) {
	payload := make([]byte, 16)
	if _, err := rand.Read(payload); err != nil {
		return "", err
	}
	return hex.EncodeToString(payload), nil
}

func (value *Manager) initialize() error {
	if value == nil || value.Store == nil || value.Wall == nil || value.Raw == nil {
		return errors.New("Shadow approval manager dependencies are incomplete")
	}
	return nil
}

func (value *Manager) newID() (string, error) {
	if value.NewID != nil {
		return value.NewID()
	}
	return randomID()
}

func (value *Manager) Issue(ctx context.Context, qualification contract.Qualification, operation string) (contract.Challenge, error) {
	if err := value.initialize(); err != nil || ctx == nil || ctx.Err() != nil || qualification.Validate() != nil ||
		(operation != "execute" && operation != "synthetic_execute") {
		return contract.Challenge{}, errors.New("Shadow qualification cannot issue an approval")
	}
	now := value.Wall.NowUnix()
	if now <= 0 {
		return contract.Challenge{}, errors.New("Shadow approval wall clock is invalid")
	}
	if existing, found, err := value.Store.Load(ctx); err != nil {
		return contract.Challenge{}, err
	} else if found {
		if existing.Validate() == nil && now >= existing.IssuedAtUnix && now < existing.ExpiresAtUnix &&
			existing.Operation == operation && BindingFromQualification(qualification).matches(existing) {
			return existing, nil
		}
		if err := value.Store.Remove(ctx, existing.ChallengeID); err != nil {
			return contract.Challenge{}, errors.New("stale Shadow approval could not be removed")
		}
	}
	id, err := value.newID()
	if err != nil {
		return contract.Challenge{}, errors.New("Shadow approval identity generation failed")
	}
	challenge := contract.Challenge{
		Version: contract.Version, ChallengeID: id, Operation: operation,
		BuildSetDigest: qualification.BuildSetDigest, SourceQualificationDigest: qualification.SourceQualificationDigest,
		CleanupRoute: qualification.CleanupRoute, AccountBindingID: qualification.AccountBindingID,
		OptionsDigest: qualification.OptionsDigest, IssuedAtUnix: now, ExpiresAtUnix: now + ChallengeLifetimeSeconds,
	}
	if err := challenge.Validate(); err != nil {
		return contract.Challenge{}, err
	}
	if err := value.Store.Save(ctx, challenge); err != nil {
		return contract.Challenge{}, err
	}
	return challenge, nil
}

// Consume removes the exact challenge before returning an execution request.
// A failed removal never grants mutation authority. T0 is captured at the
// confirmation boundary and is not reset after storage I/O.
func (value *Manager) Consume(ctx context.Context, challengeID, requestID string, confirmed bool, expected Binding, operation string) (contract.Request, error) {
	if err := value.initialize(); err != nil || ctx == nil || ctx.Err() != nil || !confirmed ||
		(operation != "execute" && operation != "synthetic_execute") {
		return contract.Request{}, errors.New("explicit Shadow approval confirmation is required")
	}
	// The confirmation call itself is T0. Any challenge read, validation, or
	// durable removal latency consumes the one fixed 120-second budget.
	t0, err := value.Raw.NowNS()
	if err != nil || t0 == 0 {
		return contract.Request{}, errors.New("Shadow approval T0 could not be established")
	}
	challenge, found, err := value.Store.Load(ctx)
	if err != nil || !found || challenge.Validate() != nil || challenge.ChallengeID != challengeID || !expected.matches(challenge) {
		return contract.Request{}, errors.New("Shadow approval challenge is invalid or drifted")
	}
	if challenge.Operation != operation {
		return contract.Request{}, errors.New("Shadow approval challenge is bound to another operation")
	}
	now := value.Wall.NowUnix()
	if now < challenge.IssuedAtUnix || now >= challenge.ExpiresAtUnix {
		_ = value.Store.Remove(ctx, challenge.ChallengeID)
		return contract.Request{}, errors.New("Shadow approval challenge expired")
	}
	if err := value.Store.Remove(ctx, challenge.ChallengeID); err != nil {
		return contract.Request{}, errors.New("Shadow approval challenge could not be consumed")
	}
	deadline := contract.NewDeadline(t0)
	request := contract.Request{
		Version: contract.Version, Operation: operation, RequestID: requestID, ChallengeID: challenge.ChallengeID,
		BuildSetDigest: challenge.BuildSetDigest, SourceQualificationDigest: challenge.SourceQualificationDigest,
		CleanupRoute: challenge.CleanupRoute, AccountBindingID: challenge.AccountBindingID,
		OptionsDigest: challenge.OptionsDigest, Deadline: &deadline,
	}
	if err := request.Validate(); err != nil {
		return contract.Request{}, err
	}
	return request, nil
}
