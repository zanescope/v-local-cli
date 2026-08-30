// Package shadowpublish implements the CLI-owned, crash-recoverable Shadow
// credential generation transaction. Durable state contains identities only;
// secret bytes are accepted solely by the Keychain backend.
package shadowpublish

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	clockmodel "github.com/zanescope/v-local-cli/internal/shadowclock"
	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
)

const (
	StateVersion                = 1
	maxPublishedCredentialBytes = 128 * 1024
)

var (
	ErrDeadline              = errors.New("Shadow publication deadline exhausted")
	ErrGenerationCollision   = errors.New("Shadow generation identity already exists")
	ErrReconciliationPending = errors.New("Shadow generation reconciliation remains pending")
)

type GenerationState struct {
	Version              int    `json:"version"`
	Status               string `json:"status"`
	AccountBindingID     string `json:"account_binding_id"`
	GenerationID         string `json:"generation_id"`
	BuildSetDigest       string `json:"build_set_digest"`
	AttemptID            string `json:"attempt_id"`
	PreviousGenerationID string `json:"previous_generation_id,omitempty"`
	ObsoleteGenerationID string `json:"obsolete_generation_id,omitempty"`
}

func lowerHex(value string, bytes int) bool {
	if len(value) != bytes*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == bytes
}

func (value GenerationState) Validate() error {
	if value.Version != StateVersion || (!lowerHex(value.AccountBindingID, 8) && !lowerHex(value.AccountBindingID, 16)) ||
		!lowerHex(value.GenerationID, 16) || !lowerHex(value.BuildSetDigest, 32) || !lowerHex(value.AttemptID, 16) {
		return errors.New("Shadow generation state binding is invalid")
	}
	switch value.Status {
	case "pending":
		if value.ObsoleteGenerationID != "" || value.PreviousGenerationID != "" &&
			(!lowerHex(value.PreviousGenerationID, 16) || value.PreviousGenerationID == value.GenerationID) {
			return errors.New("Shadow pending generation state is invalid")
		}
	case "ready":
		if value.PreviousGenerationID != "" || value.ObsoleteGenerationID != "" &&
			(!lowerHex(value.ObsoleteGenerationID, 16) || value.ObsoleteGenerationID == value.GenerationID) {
			return errors.New("Shadow ready generation state is invalid")
		}
	default:
		return errors.New("Shadow generation state status is invalid")
	}
	return nil
}

type StateStore interface {
	LoadReady(context.Context, string) (GenerationState, bool, error)
	LoadPending(context.Context, string) (GenerationState, bool, error)
	SaveReady(context.Context, GenerationState) error
	SavePending(context.Context, GenerationState) error
	RemovePending(context.Context, string) error
}

// Keychain is context-aware. A production implementation that wraps a
// non-cancellable platform API must isolate it in a bounded helper process.
type Keychain interface {
	Put(context.Context, string, string, []byte) error
	Get(context.Context, string, string) ([]byte, bool, error)
	Delete(context.Context, string, string) error
}

type Locker interface {
	Acquire(context.Context, string) (func() error, error)
}

type Publisher struct {
	Clock    clockmodel.Clock
	State    StateStore
	Keychain Keychain
	Locker   Locker
	NewID    func() (string, error)
}

type Request struct {
	AccountBindingID string
	BuildSetDigest   string
	AttemptID        string
	Deadline         contract.Deadline
}

func (value Request) validate() error {
	state := GenerationState{
		Version: StateVersion, Status: "ready", AccountBindingID: value.AccountBindingID,
		GenerationID: "00000000000000000000000000000001", BuildSetDigest: value.BuildSetDigest,
		AttemptID: value.AttemptID,
	}
	if err := state.Validate(); err != nil {
		return err
	}
	return value.Deadline.Validate()
}

func randomID() (string, error) {
	payload := make([]byte, 16)
	if _, err := rand.Read(payload); err != nil {
		return "", err
	}
	return hex.EncodeToString(payload), nil
}

func (value *Publisher) initialize() error {
	if value == nil || value.Clock == nil || value.State == nil || value.Keychain == nil || value.Locker == nil {
		return errors.New("Shadow publisher dependencies are incomplete")
	}
	return nil
}

func (value *Publisher) newID() (string, error) {
	if value.NewID != nil {
		return value.NewID()
	}
	return randomID()
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

func boundedContext(parent context.Context, clock clockmodel.Clock, deadline uint64) (context.Context, context.CancelFunc, error) {
	remaining, err := clockmodel.Remaining(clock, deadline)
	if err != nil {
		return nil, nil, err
	}
	if remaining <= 0 {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}, nil
	}
	ctx, cancel := context.WithTimeout(parent, remaining)
	return ctx, cancel, nil
}

type CommittedError struct{ Cause error }

func (value *CommittedError) Error() string {
	return "new Shadow generation is committed but obsolete-generation cleanup is pending"
}

func (value *CommittedError) Unwrap() error { return value.Cause }

func Committed(err error) bool {
	var committed *CommittedError
	return errors.As(err, &committed)
}

func validateLoaded(state GenerationState, found bool, accountID, status string) error {
	if !found {
		return nil
	}
	if err := state.Validate(); err != nil || state.AccountBindingID != accountID || state.Status != status {
		return errors.New("persisted Shadow generation state is invalid")
	}
	return nil
}

func validateStatePair(ready GenerationState, readyFound bool, pending GenerationState, pendingFound bool) error {
	if !pendingFound {
		return nil
	}
	if readyFound && pending.GenerationID == ready.GenerationID {
		if pending.BuildSetDigest != ready.BuildSetDigest || pending.AttemptID != ready.AttemptID ||
			pending.PreviousGenerationID != ready.ObsoleteGenerationID {
			return errors.New("committed Shadow generation does not match its pending record")
		}
		return nil
	}
	if !readyFound {
		if pending.PreviousGenerationID != "" {
			return errors.New("first Shadow generation has an unexpected predecessor")
		}
		return nil
	}
	if ready.ObsoleteGenerationID != "" || pending.PreviousGenerationID != ready.GenerationID {
		return errors.New("pending Shadow generation does not follow the current ready generation")
	}
	return nil
}

func (value *Publisher) exactDeleteAndVerify(ctx context.Context, accountID, generationID string, mutationStopNS uint64) error {
	if !before(value.Clock, mutationStopNS) {
		return ErrDeadline
	}
	if err := value.Keychain.Delete(ctx, accountID, generationID); err != nil {
		return err
	}
	payload, found, err := value.Keychain.Get(ctx, accountID, generationID)
	clearBytes(payload)
	if err != nil || found || len(payload) != 0 {
		return ErrReconciliationPending
	}
	return nil
}

func (value *Publisher) reconcileLocked(ctx context.Context, accountID string, mutationStopNS uint64) (GenerationState, bool, error) {
	ready, readyFound, err := value.State.LoadReady(ctx, accountID)
	if err != nil || validateLoaded(ready, readyFound, accountID, "ready") != nil {
		return GenerationState{}, false, errors.New("ready Shadow generation state is unreadable")
	}
	pending, pendingFound, err := value.State.LoadPending(ctx, accountID)
	if err != nil || validateLoaded(pending, pendingFound, accountID, "pending") != nil {
		return GenerationState{}, false, errors.New("pending Shadow generation state is unreadable")
	}
	if err := validateStatePair(ready, readyFound, pending, pendingFound); err != nil {
		return GenerationState{}, false, err
	}
	if pendingFound {
		if readyFound && ready.GenerationID == pending.GenerationID {
			if !before(value.Clock, mutationStopNS) {
				return GenerationState{}, false, ErrReconciliationPending
			}
			if err := value.State.RemovePending(ctx, accountID); err != nil {
				return GenerationState{}, false, ErrReconciliationPending
			}
			pendingFound = false
		} else {
			if err := value.exactDeleteAndVerify(ctx, accountID, pending.GenerationID, mutationStopNS); err != nil {
				return GenerationState{}, false, ErrReconciliationPending
			}
			if !before(value.Clock, mutationStopNS) || value.State.RemovePending(ctx, accountID) != nil {
				return GenerationState{}, false, ErrReconciliationPending
			}
			pendingFound = false
		}
	}
	if readyFound && ready.ObsoleteGenerationID != "" {
		if err := value.exactDeleteAndVerify(ctx, accountID, ready.ObsoleteGenerationID, mutationStopNS); err != nil {
			return GenerationState{}, false, &CommittedError{Cause: err}
		}
		ready.ObsoleteGenerationID = ""
		if !before(value.Clock, mutationStopNS) || value.State.SaveReady(ctx, ready) != nil {
			return GenerationState{}, false, &CommittedError{Cause: ErrReconciliationPending}
		}
	}
	if readyFound {
		secret, found, getErr := value.Keychain.Get(ctx, accountID, ready.GenerationID)
		valid := getErr == nil && found && len(secret) != 0 && len(secret) <= maxPublishedCredentialBytes
		clearBytes(secret)
		if !valid || pendingFound {
			return GenerationState{}, false, ErrReconciliationPending
		}
	}
	return ready, readyFound, nil
}

// Reconcile makes no new credential generation. It only resolves exact IDs
// already present in pending/ready state and never promotes pending to ready.
func (value *Publisher) Reconcile(parent context.Context, accountID string, mutationStopNS uint64) (result GenerationState, found bool, resultErr error) {
	if err := value.initialize(); err != nil || parent == nil || (!lowerHex(accountID, 8) && !lowerHex(accountID, 16)) {
		return GenerationState{}, false, errors.New("Shadow reconciliation input is invalid")
	}
	if !before(value.Clock, mutationStopNS) {
		return GenerationState{}, false, ErrDeadline
	}
	ctx, cancel, err := boundedContext(parent, value.Clock, mutationStopNS)
	if err != nil {
		return GenerationState{}, false, err
	}
	defer cancel()
	release, err := value.Locker.Acquire(ctx, accountID)
	if err != nil {
		return GenerationState{}, false, err
	}
	if release == nil {
		return GenerationState{}, false, errors.New("Shadow generation lock release is unavailable")
	}
	defer func() {
		if err := release(); err != nil && resultErr == nil {
			resultErr = errors.New("Shadow generation lock release failed")
		}
	}()
	return value.reconcileLocked(ctx, accountID, mutationStopNS)
}

func (value *Publisher) Publish(parent context.Context, request Request, credential []byte) (result GenerationState, resultErr error) {
	if err := value.initialize(); err != nil || parent == nil || request.validate() != nil || len(credential) == 0 || len(credential) > maxPublishedCredentialBytes {
		return GenerationState{}, errors.New("Shadow publication input is invalid")
	}
	if !before(value.Clock, request.Deadline.CLIVerifyNS) {
		return GenerationState{}, ErrDeadline
	}
	ctx, cancel, err := boundedContext(parent, value.Clock, request.Deadline.MutationStopNS)
	if err != nil {
		return GenerationState{}, err
	}
	defer cancel()
	release, err := value.Locker.Acquire(ctx, request.AccountBindingID)
	if err != nil {
		return GenerationState{}, err
	}
	if release == nil {
		return GenerationState{}, errors.New("Shadow generation lock release is unavailable")
	}
	defer func() {
		if err := release(); err != nil && resultErr == nil {
			if result.Validate() == nil && result.Status == "ready" {
				resultErr = &CommittedError{Cause: errors.New("Shadow generation lock release failed")}
			} else {
				resultErr = errors.New("Shadow generation lock release failed")
			}
		}
	}()
	ready, readyFound, err := value.reconcileLocked(ctx, request.AccountBindingID, request.Deadline.MutationStopNS)
	if err != nil {
		return GenerationState{}, err
	}
	if !before(value.Clock, request.Deadline.CLIVerifyNS) {
		return GenerationState{}, ErrDeadline
	}
	generationID, err := value.newID()
	if err != nil || !lowerHex(generationID, 16) {
		return GenerationState{}, errors.New("could not generate a Shadow generation identity")
	}
	existing, found, err := value.Keychain.Get(ctx, request.AccountBindingID, generationID)
	clearBytes(existing)
	if err != nil {
		return GenerationState{}, ErrReconciliationPending
	}
	if found || len(existing) != 0 {
		return GenerationState{}, ErrGenerationCollision
	}
	pending := GenerationState{
		Version: StateVersion, Status: "pending", AccountBindingID: request.AccountBindingID,
		GenerationID: generationID, BuildSetDigest: request.BuildSetDigest, AttemptID: request.AttemptID,
	}
	if readyFound {
		pending.PreviousGenerationID = ready.GenerationID
	}
	if err := pending.Validate(); err != nil {
		return GenerationState{}, err
	}
	if !before(value.Clock, request.Deadline.MutationStopNS) {
		return GenerationState{}, ErrDeadline
	}
	if value.State.SavePending(ctx, pending) != nil {
		return GenerationState{}, ErrReconciliationPending
	}
	if !before(value.Clock, request.Deadline.MutationStopNS) {
		return GenerationState{}, ErrReconciliationPending
	}
	if err := value.Keychain.Put(ctx, request.AccountBindingID, generationID, credential); err != nil {
		// Put may have committed despite returning an uncertain platform result.
		// Keep pending state so startup reconciliation can query this exact ID.
		return GenerationState{}, ErrReconciliationPending
	}
	stored, found, err := value.Keychain.Get(ctx, request.AccountBindingID, generationID)
	verified := err == nil && found && len(stored) == len(credential) &&
		subtle.ConstantTimeCompare(stored, credential) == 1
	clearBytes(stored)
	if !verified {
		return GenerationState{}, ErrReconciliationPending
	}
	if !before(value.Clock, request.Deadline.MutationStopNS) {
		return GenerationState{}, ErrReconciliationPending
	}
	committed := GenerationState{
		Version: StateVersion, Status: "ready", AccountBindingID: request.AccountBindingID,
		GenerationID: generationID, BuildSetDigest: request.BuildSetDigest, AttemptID: request.AttemptID,
	}
	if readyFound {
		committed.ObsoleteGenerationID = ready.GenerationID
	}
	if err := value.State.SaveReady(ctx, committed); err != nil {
		return GenerationState{}, ErrReconciliationPending
	}
	if !before(value.Clock, request.Deadline.MutationStopNS) || value.State.RemovePending(ctx, request.AccountBindingID) != nil {
		return committed, &CommittedError{Cause: ErrReconciliationPending}
	}
	if committed.ObsoleteGenerationID != "" {
		if err := value.exactDeleteAndVerify(ctx, request.AccountBindingID, committed.ObsoleteGenerationID, request.Deadline.MutationStopNS); err != nil {
			return committed, &CommittedError{Cause: err}
		}
		committed.ObsoleteGenerationID = ""
		if !before(value.Clock, request.Deadline.MutationStopNS) || value.State.SaveReady(ctx, committed) != nil {
			return committed, &CommittedError{Cause: ErrReconciliationPending}
		}
	}
	return committed, nil
}

func (value GenerationState) String() string {
	return fmt.Sprintf("Shadow generation %s status=%s", value.GenerationID, value.Status)
}
