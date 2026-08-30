//go:build darwin

package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
	approvalmodel "github.com/zanescope/v-local-cli/internal/shadowapproval"
	clockmodel "github.com/zanescope/v-local-cli/internal/shadowclock"
	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
	ownermodel "github.com/zanescope/v-local-cli/internal/shadowowner"
	publishmodel "github.com/zanescope/v-local-cli/internal/shadowpublish"
	verifymodel "github.com/zanescope/v-local-cli/internal/shadowverify"
	"golang.org/x/sys/unix"
)

const (
	executableSyntheticBuild  = "6170b8b73eb596f4a22aed6f4c15a9fb92900f3924152b0fc174b11122930bf9"
	executableSyntheticSource = "6d221ae3f44995df0b2c09a124859ad11908bcd0ee0630fc9efe15ce1c12ee1e"
	executableSyntheticText   = "v-local synthetic source v1"
)

type executableWallClock struct{}

func (executableWallClock) NowUnix() int64 { return time.Now().Unix() }

type executableProbe struct {
	root       string
	sourcePath string
	buildSet   string
}

func (value executableProbe) BuildSetDigest(context.Context) (string, error) {
	return value.buildSet, nil
}

func (value executableProbe) ResourceAbsent(_ context.Context, _ string, resource contract.ResourceBinding) (bool, error) {
	path := filepath.Clean(filepath.Join(value.root, filepath.FromSlash(resource.Leaf)))
	if path != value.root && !strings.HasPrefix(path, value.root+string(filepath.Separator)) {
		return false, errors.New("synthetic receipt resource escaped its observation root")
	}
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	return false, err
}

func (executableProbe) ProcessAbsent(_ context.Context, process contract.ProcessBinding) (bool, error) {
	err := unix.Kill(process.PID, 0)
	if errors.Is(err, unix.ESRCH) {
		return true, nil
	}
	if err == nil || errors.Is(err, unix.EPERM) {
		return false, nil
	}
	return false, err
}

func (value executableProbe) LaunchRegistrationAbsent(_ context.Context, bundleID string, _ contract.ResourceBinding) (bool, error) {
	_, err := os.Lstat(filepath.Join(value.root, "launch."+bundleID))
	if os.IsNotExist(err) {
		return true, nil
	}
	return false, err
}

func (value executableProbe) SourceUnchanged(_ context.Context, expected string) (bool, error) {
	payload, err := os.ReadFile(value.sourcePath)
	if err != nil {
		return false, err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]) == expected, nil
}

func (executableProbe) SecurityPostureExpected(context.Context) (bool, error) {
	return true, nil
}

type executableState struct {
	ready   *publishmodel.GenerationState
	pending *publishmodel.GenerationState
}

func (value *executableState) LoadReady(context.Context, string) (publishmodel.GenerationState, bool, error) {
	if value.ready == nil {
		return publishmodel.GenerationState{}, false, nil
	}
	return *value.ready, true, nil
}

func (value *executableState) LoadPending(context.Context, string) (publishmodel.GenerationState, bool, error) {
	if value.pending == nil {
		return publishmodel.GenerationState{}, false, nil
	}
	return *value.pending, true, nil
}

func (value *executableState) SaveReady(_ context.Context, state publishmodel.GenerationState) error {
	copy := state
	value.ready = &copy
	return nil
}

func (value *executableState) SavePending(_ context.Context, state publishmodel.GenerationState) error {
	copy := state
	value.pending = &copy
	return nil
}

func (value *executableState) RemovePending(context.Context, string) error {
	value.pending = nil
	return nil
}

type executableKeychain struct{ values map[string][]byte }

func (value *executableKeychain) Put(_ context.Context, account, generation string, secret []byte) error {
	value.values[account+generation] = append([]byte(nil), secret...)
	return nil
}

func (value *executableKeychain) Get(_ context.Context, account, generation string) ([]byte, bool, error) {
	secret, found := value.values[account+generation]
	return append([]byte(nil), secret...), found, nil
}

func (value *executableKeychain) Delete(_ context.Context, account, generation string) error {
	delete(value.values, account+generation)
	return nil
}

type executableLock struct{ held bool }

func (value *executableLock) Acquire(context.Context, string) (func() error, error) {
	if value.held {
		return nil, errors.New("synthetic publication lock is held")
	}
	value.held = true
	return func() error { value.held = false; return nil }, nil
}

func canonicalExecutableRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestExecutableSyntheticVerticalSliceUsesRealProviderDaemon(t *testing.T) {
	providerPath := strings.TrimSpace(os.Getenv("V_LOCAL_SHADOW_PROVIDER_TEST_BINARY"))
	if providerPath == "" {
		t.Skip("set V_LOCAL_SHADOW_PROVIDER_TEST_BINARY to run the cross-repository executable gate")
	}
	root := canonicalExecutableRoot(t)
	accountPath := filepath.Join(root, "account")
	databasePath := filepath.Join(accountPath, "db")
	approvalPath := filepath.Join(root, "approval")
	observationPath := filepath.Join(root, "observation")
	for _, path := range []string{databasePath, approvalPath, observationPath} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sourcePath := filepath.Join(observationPath, "synthetic-source.txt")
	if err := os.WriteFile(sourcePath, []byte(executableSyntheticText), 0o600); err != nil {
		t.Fatal(err)
	}
	raw := clockmodel.System{}
	client, err := NewShadowClient(providerPath, localplatform.Account{
		Name: "synthetic", Path: accountPath, DBDir: databasePath,
	}, []string{"media"}, "", raw)
	if err != nil {
		t.Fatal(err)
	}
	approvalStore, err := approvalmodel.NewFileStore(approvalPath)
	if err != nil {
		t.Fatal(err)
	}
	state := &executableState{}
	keychain := &executableKeychain{values: map[string][]byte{}}
	publisher := &publishmodel.Publisher{
		Clock: raw, State: state, Keychain: keychain, Locker: &executableLock{},
		NewID: func() (string, error) { return "77777777777777777777777777777777", nil },
	}
	validator := ShadowCandidateValidator{
		Account:    localplatform.Account{Name: "synthetic", Path: accountPath, DBDir: databasePath},
		CatalogKey: strings.Repeat("c", 64), Scopes: []string{"media"},
		ValidateSelected: func(_ context.Context, candidate *CandidateBundle) error {
			if candidate.ImageKeys == nil || candidate.ImageKeys.AES != "1234567890abcdef" || candidate.ImageKeys.XOR != 7 {
				return errors.New("synthetic selected input did not validate")
			}
			return nil
		},
	}
	owner := &ownermodel.Owner{
		Operation: "synthetic_execute", Clock: raw, Provider: client,
		Approval: &approvalmodel.Manager{
			Store: approvalStore, Wall: executableWallClock{}, Raw: raw,
			NewID: func() (string, error) { return "00112233445566778899aabbccddeeff", nil },
		},
		Verifier: verifymodel.Verifier{Clock: raw, Probe: executableProbe{
			root: observationPath, sourcePath: sourcePath, buildSet: executableSyntheticBuild,
		}},
		Validator: validator, Publisher: publisher,
	}
	binding := ownermodel.Binding{
		BuildSetDigest: executableSyntheticBuild, SourceQualificationDigest: executableSyntheticSource,
		CleanupRoute: contract.CleanupRouteDirect, AccountBindingID: "aabbccddeeff0011",
		OptionsDigest: "3333333333333333333333333333333333333333333333333333333333333333",
	}
	start := time.Now()
	plan, err := owner.Plan(context.Background(), "88888888888888888888888888888888", binding)
	if err != nil || plan.Result.Status != "action_required" {
		t.Fatalf("executable plan=%+v err=%v", plan, err)
	}
	result, err := owner.Execute(context.Background(), plan.Challenge.ChallengeID,
		"99999999999999999999999999999999", true, binding)
	elapsed := time.Since(start)
	if err != nil || result.Status != "ready" || result.GenerationID != "77777777777777777777777777777777" {
		t.Fatalf("executable result=%+v err=%v elapsed=%s", result, err, elapsed)
	}
	if elapsed >= 10*time.Second {
		t.Fatalf("eager executable synthetic vertical slice took %s", elapsed)
	}
	if state.pending != nil || state.ready == nil || len(keychain.values) != 1 {
		t.Fatalf("executable publication inconsistent: pending=%+v ready=%+v items=%d", state.pending, state.ready, len(keychain.values))
	}
	for _, payload := range keychain.values {
		if text := string(payload); strings.Contains(text, "shadow_attempt") || strings.Contains(text, "diagnostics") {
			t.Fatal("Keychain received transient Shadow evidence instead of the minimal credential")
		}
	}
	if _, found, err := approvalStore.Load(context.Background()); err != nil || found {
		t.Fatalf("consumed executable approval remains: found=%v err=%v", found, err)
	}
}
