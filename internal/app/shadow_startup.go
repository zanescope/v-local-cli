package app

import (
	"context"
	"errors"
	"sort"

	clockmodel "github.com/zanescope/v-local-cli/internal/shadowclock"
	publishmodel "github.com/zanescope/v-local-cli/internal/shadowpublish"
	"github.com/zanescope/v-local-cli/internal/state"
)

const startupGenerationReconcileWindowNS uint64 = 30_000_000_000

type startupGenerationReconciler interface {
	Reconcile(context.Context, string, uint64) (publishmodel.GenerationState, bool, error)
}

type startupGenerationFactory func(string) (startupGenerationReconciler, error)

// reconcileStartupShadowGenerations is the account-readiness gate shared by
// production startup and native synthetic tests. All accounts share one fixed
// absolute deadline; per-account work cannot reset the recovery budget.
func reconcileStartupShadowGenerations(
	ctx context.Context,
	clock clockmodel.Clock,
	accounts []state.AccountState,
	factory startupGenerationFactory,
) error {
	if ctx == nil || clock == nil || factory == nil {
		return errors.New("Shadow startup reconciliation dependencies are incomplete")
	}
	now, err := clock.NowNS()
	if err != nil || now == 0 || now > ^uint64(0)-startupGenerationReconcileWindowNS {
		return errors.New("Shadow startup reconciliation deadline is unavailable")
	}
	deadline := now + startupGenerationReconcileWindowNS
	accountIDs := make([]string, 0, len(accounts))
	seen := map[string]bool{}
	for _, account := range accounts {
		if account.AccountID == "" || seen[account.AccountID] {
			return errors.New("Shadow startup account set is invalid")
		}
		seen[account.AccountID] = true
		accountIDs = append(accountIDs, account.AccountID)
	}
	sort.Strings(accountIDs)
	for _, accountID := range accountIDs {
		if ctx.Err() != nil {
			return errors.New("Shadow startup reconciliation was cancelled")
		}
		reconciler, err := factory(accountID)
		if err != nil || reconciler == nil {
			return errors.New("Shadow startup reconciler is unavailable")
		}
		ready, found, err := reconciler.Reconcile(ctx, accountID, deadline)
		if err != nil {
			return errors.New("Shadow startup generation remains unreconciled")
		}
		if found && (ready.Validate() != nil || ready.Status != "ready" || ready.AccountBindingID != accountID) {
			return errors.New("Shadow startup ready generation is invalid")
		}
	}
	return nil
}
