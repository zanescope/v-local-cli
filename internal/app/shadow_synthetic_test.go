package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	publishmodel "github.com/zanescope/v-local-cli/internal/shadowpublish"
	"github.com/zanescope/v-local-cli/internal/state"
)

func TestExecutableSyntheticOwnerContractCoversEveryCLIStage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Main([]string{"__shadow-synthetic-owner", "--confirm"}, &stdout, &stderr); code != 0 {
		t.Fatalf("synthetic Owner command code=%d stderr=%s", code, stderr.String())
	}
	var summary shadowSyntheticSummary
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&summary); err != nil || decoder.Decode(&struct{}{}) == nil {
		t.Fatalf("synthetic Owner output is not one strict object: %v", err)
	}
	wantEvents := []string{
		"startup:exact_pending_delete",
		"state:pending_removed",
		"startup:reconciled",
		"provider:qualify",
		"approval:issued",
		"approval:consumed",
		"provider:execute_cleanup_complete",
		"cli:independent_cleanup_verify",
		"cli:candidate_validated",
		"publisher:pending_saved",
		"publisher:keychain_put",
		"publisher:ready_saved",
		"state:pending_removed",
	}
	if summary.Version != shadowSyntheticVersion || summary.Status != "ready" ||
		summary.Operation != "synthetic_execute" || summary.ProductionRouteEnabled ||
		!summary.StartupReconciled || !summary.ApprovalConsumed ||
		summary.GenerationID != shadowSyntheticReadyID || summary.PendingGeneration ||
		summary.KeychainItemCount != 1 || !reflect.DeepEqual(summary.Events, wantEvents) {
		t.Fatalf("synthetic Owner contract drifted: %+v", summary)
	}
	output := stdout.String() + stderr.String()
	for _, secret := range []string{"1234567890abcdef", "discarded-synthetic-pending-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("synthetic credential leaked to command output: %q", secret)
		}
	}
}

func TestSyntheticOwnerRequiresExplicitConfirmationAndNeverEnablesProduction(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Main([]string{"__shadow-synthetic-owner"}, &stdout, &stderr); code != 3 {
		t.Fatalf("unconfirmed synthetic Owner code=%d stderr=%s", code, stderr.String())
	}
	var summary shadowSyntheticSummary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Status != "action_required" || summary.ApprovalConsumed || summary.ProductionRouteEnabled ||
		summary.GenerationID != "" || summary.KeychainItemCount != 0 {
		t.Fatalf("unconfirmed synthetic Owner crossed its boundary: %+v", summary)
	}
	for _, forbidden := range []string{"approval:consumed", "provider:execute_cleanup_complete", "publisher:keychain_put"} {
		if strings.Contains(strings.Join(summary.Events, "\n"), forbidden) {
			t.Fatalf("unconfirmed synthetic Owner performed %s", forbidden)
		}
	}
}

type startupTestClock struct{ now uint64 }

func (value startupTestClock) NowNS() (uint64, error) { return value.now, nil }

type startupTestReconciler struct {
	accountID string
	calls     *[]string
	deadlines *[]uint64
	ready     publishmodel.GenerationState
	found     bool
	err       error
}

func (value startupTestReconciler) Reconcile(_ context.Context, accountID string, deadline uint64) (publishmodel.GenerationState, bool, error) {
	if accountID != value.accountID {
		return publishmodel.GenerationState{}, false, errors.New("account drift")
	}
	*value.calls = append(*value.calls, accountID)
	*value.deadlines = append(*value.deadlines, deadline)
	return value.ready, value.found, value.err
}

func TestStartupGenerationReconciliationSortsAccountsAndSharesOneDeadline(t *testing.T) {
	const first = "1111111111111111"
	const second = "2222222222222222"
	calls := []string{}
	deadlines := []uint64{}
	factory := func(accountID string) (startupGenerationReconciler, error) {
		return startupTestReconciler{accountID: accountID, calls: &calls, deadlines: &deadlines}, nil
	}
	err := reconcileStartupShadowGenerations(context.Background(), startupTestClock{now: 10}, []state.AccountState{
		{AccountID: second}, {AccountID: first},
	}, factory)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{first, second}) || len(deadlines) != 2 ||
		deadlines[0] != 10+startupGenerationReconcileWindowNS || deadlines[1] != deadlines[0] {
		t.Fatalf("startup reconciliation reset or reordered its budget: calls=%v deadlines=%v", calls, deadlines)
	}
}

func TestStartupGenerationReconciliationFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		accounts []state.AccountState
		factory  startupGenerationFactory
	}{
		{
			name: "duplicate account", accounts: []state.AccountState{{AccountID: "1111111111111111"}, {AccountID: "1111111111111111"}},
			factory: func(string) (startupGenerationReconciler, error) {
				return nil, errors.New("must not be reached")
			},
		},
		{
			name: "reconcile failure", accounts: []state.AccountState{{AccountID: "1111111111111111"}},
			factory: func(accountID string) (startupGenerationReconciler, error) {
				calls := []string{}
				deadlines := []uint64{}
				return startupTestReconciler{accountID: accountID, calls: &calls, deadlines: &deadlines, err: errors.New("injected")}, nil
			},
		},
		{
			name: "invalid ready", accounts: []state.AccountState{{AccountID: "1111111111111111"}},
			factory: func(accountID string) (startupGenerationReconciler, error) {
				calls := []string{}
				deadlines := []uint64{}
				return startupTestReconciler{accountID: accountID, calls: &calls, deadlines: &deadlines, found: true}, nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := reconcileStartupShadowGenerations(context.Background(), startupTestClock{now: 10}, test.accounts, test.factory); err == nil {
				t.Fatal("unsafe startup state was accepted")
			}
		})
	}
}
