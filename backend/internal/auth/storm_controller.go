package auth

import (
	"context"
	"sync"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// StormController enforces refresh storm budgets. This slice implements
// account scope only; provider-endpoint and global scopes remain deferred.
type StormController struct {
	queries *db.Queries
}

func NewStormController(queries *db.Queries) *StormController {
	return &StormController{queries: queries}
}

// Acquire reserves one account-scope refresh slot. The returned release
// function is idempotent and intentionally best-effort.
func (c *StormController) Acquire(ctx context.Context, tenantID, accountID int64) (func(), Outcome, error) {
	if c == nil || c.queries == nil {
		panic("TODO: wire sqlc queries before using account storm controller")
	}

	budget, err := c.queries.GetOrCreateAccountStormBudget(ctx, db.GetOrCreateAccountStormBudgetParams{
		TenantID:          tenantID,
		ProviderAccountID: &accountID,
	})
	if err != nil {
		return nil, "", err
	}

	currentInFlight, err := c.queries.TryAcquireAccountStormSlot(ctx, budget.ID)
	if err != nil {
		return nil, "", err
	}
	if currentInFlight <= 0 {
		return nil, OutcomeStormBudgetExhausted, nil
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			_ = c.queries.ReleaseAccountStormSlot(context.Background(), budget.ID)
		})
	}

	return release, "", nil
}

func (c *StormController) AcquireProviderEndpoint(ctx context.Context, tenantID int64, providerCode, endpointFingerprint string) (func(), Outcome, error) {
	panic("TODO: provider-endpoint storm scope deferred")
}

func (c *StormController) AcquireGlobal(ctx context.Context, tenantID int64) (func(), Outcome, error) {
	panic("TODO: global storm scope deferred")
}

