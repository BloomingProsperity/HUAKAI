package auth

import (
	"context"
	"errors"
	"sync"

	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

var (
	ErrStormControllerUnavailable = errors.New("auth: storm controller unavailable")
	ErrStormScopeNotImplemented   = errors.New("auth: storm scope not implemented")
)

// StormController enforces refresh storm budgets. This slice implements
// account scope only; provider-endpoint and global scopes remain deferred.
type StormController struct {
	queries *dbauth.Queries
}

func NewStormController(queries *dbauth.Queries) *StormController {
	return &StormController{queries: queries}
}

// Acquire reserves one account-scope refresh slot. The returned release
// function is idempotent and intentionally best-effort.
func (c *StormController) Acquire(ctx context.Context, tenantID, accountID int64) (func(), Outcome, error) {
	if c == nil || c.queries == nil {
		return nil, OutcomeStormBudgetExhausted, ErrStormControllerUnavailable
	}

	budget, err := c.queries.GetOrCreateAccountStormBudget(ctx, dbauth.GetOrCreateAccountStormBudgetParams{
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
	return nil, OutcomeStormBudgetExhausted, ErrStormScopeNotImplemented
}

func (c *StormController) AcquireGlobal(ctx context.Context, tenantID int64) (func(), Outcome, error) {
	return nil, OutcomeStormBudgetExhausted, ErrStormScopeNotImplemented
}
