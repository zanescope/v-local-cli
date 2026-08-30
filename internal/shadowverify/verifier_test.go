package shadowverify

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
)

type fakeClock struct{ now uint64 }

func (value *fakeClock) NowNS() (uint64, error) { return value.now, nil }

type fakeProbe struct {
	clock       *fakeClock
	buildSet    string
	residueKind string
	process     bool
	launch      bool
	source      bool
	posture     bool
	advanceNS   uint64
	resources   []string
	blockBuild  bool
}

func (value *fakeProbe) BuildSetDigest(ctx context.Context) (string, error) {
	if value.blockBuild {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return value.buildSet, nil
}
func (value *fakeProbe) ResourceAbsent(_ context.Context, _ string, resource contract.ResourceBinding) (bool, error) {
	value.resources = append(value.resources, resource.Kind+"\x00"+resource.Leaf)
	value.clock.now += value.advanceNS
	return resource.Kind != value.residueKind, nil
}
func (value *fakeProbe) ProcessAbsent(context.Context, contract.ProcessBinding) (bool, error) {
	return value.process, nil
}
func (value *fakeProbe) LaunchRegistrationAbsent(context.Context, string, contract.ResourceBinding) (bool, error) {
	return value.launch, nil
}
func (value *fakeProbe) SourceUnchanged(context.Context, string) (bool, error) {
	return value.source, nil
}
func (value *fakeProbe) SecurityPostureExpected(context.Context) (bool, error) {
	return value.posture, nil
}

func vectors(t *testing.T) contract.GoldenVectors {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "testdata", "shadow-contract-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result contract.GoldenVectors
	if err := contract.DecodeStrict(payload, &result); err != nil || result.Validate() != nil {
		t.Fatalf("invalid golden vectors: decode=%v validate=%v", err, result.Validate())
	}
	return result
}

func verifierFixture(t *testing.T) (contract.Request, contract.Result, *fakeClock, *fakeProbe) {
	t.Helper()
	golden := vectors(t)
	clock := &fakeClock{now: golden.ExecuteRequest.Deadline.T0NS + 1}
	probe := &fakeProbe{
		clock: clock, buildSet: golden.ExecuteRequest.BuildSetDigest,
		process: true, launch: true, source: true, posture: true,
	}
	return golden.ExecuteRequest, golden.ReadyResult, clock, probe
}

func TestIndependentVerifierQueriesEveryExactReceiptBinding(t *testing.T) {
	request, result, clock, probe := verifierFixture(t)
	if err := (Verifier{Clock: clock, Probe: probe}).Verify(context.Background(), request, result); err != nil {
		t.Fatal(err)
	}
	if len(probe.resources) != len(result.Receipt.Resources) {
		t.Fatalf("queried %d resources, want %d", len(probe.resources), len(result.Receipt.Resources))
	}
}

func TestIndependentVerifierRejectsProviderCleanClaimWhenMachineHasResidue(t *testing.T) {
	request, result, clock, probe := verifierFixture(t)
	probe.residueKind = "socket"
	if err := (Verifier{Clock: clock, Probe: probe}).Verify(context.Background(), request, result); err == nil {
		t.Fatal("Provider cleanup booleans overrode independent socket residue")
	}
}

func TestIndependentVerifierRejectsBuildProcessSourceAndPostureDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fakeProbe)
	}{
		{"build", func(value *fakeProbe) {
			value.buildSet = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		}},
		{"process", func(value *fakeProbe) { value.process = false }},
		{"launch", func(value *fakeProbe) { value.launch = false }},
		{"source", func(value *fakeProbe) { value.source = false }},
		{"posture", func(value *fakeProbe) { value.posture = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, result, clock, probe := verifierFixture(t)
			test.mutate(probe)
			if err := (Verifier{Clock: clock, Probe: probe}).Verify(context.Background(), request, result); err == nil {
				t.Fatal("independent drift was accepted")
			}
		})
	}
}

func TestIndependentVerifierCannotResetT108Deadline(t *testing.T) {
	request, result, clock, probe := verifierFixture(t)
	probe.advanceNS = contract.CLIVerifyWindowNS
	if err := (Verifier{Clock: clock, Probe: probe}).Verify(context.Background(), request, result); err == nil {
		t.Fatal("CLI verification completed after the fixed T+108 boundary")
	}
}

func TestIndependentVerifierBoundsBlockingProbeAndRejectsNilContext(t *testing.T) {
	request, result, clock, probe := verifierFixture(t)
	if err := (Verifier{Clock: clock, Probe: probe}).Verify(nil, request, result); err == nil {
		t.Fatal("nil verifier context was accepted")
	}
	clock.now = request.Deadline.CLIVerifyNS - 1
	probe.blockBuild = true
	start := time.Now()
	if err := (Verifier{Clock: clock, Probe: probe}).Verify(context.Background(), request, result); err == nil {
		t.Fatal("blocking machine probe escaped the CLI verification deadline")
	}
	if time.Since(start) >= time.Second {
		t.Fatal("blocking machine probe did not return on its bounded context")
	}
}
