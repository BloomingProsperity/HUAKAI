package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

var (
	ErrStormControllerUnavailable = errors.New("auth: storm controller unavailable")
)

// StormController enforces refresh storm budgets across three scopes:
//
//   - account:           DB-durable concurrency budget (Postgres), survives
//     restart and coordinates across replicas. Always active.
//   - provider-endpoint: in-memory per-(provider, endpoint) token bucket.
//   - global:            in-memory process-wide token bucket.
//
// The endpoint/global scopes are an opt-in additive throttle (configured via
// StormScopeConfig); when unconfigured they admit, leaving the always-on account
// budget as the sole guard — behaviorally identical to the prior account-only
// slice. They are process-local; cross-replica endpoint/global budgets are future
// work.
type StormController struct {
	queries *dbauth.Queries
	scope   *stormScopeLimiter // nil = endpoint/global scopes disabled (admit-all)
	now     func() time.Time
}

// NewStormController builds an account-scope-only controller. Endpoint and global
// scopes admit everything (the additive throttle is off). Use
// NewStormControllerWithScopeBudget to enable them.
func NewStormController(queries *dbauth.Queries) *StormController {
	return NewStormControllerWithScopeBudget(queries, StormScopeConfig{})
}

// NewStormControllerWithScopeBudget builds a controller whose endpoint/global
// scopes enforce the supplied token budgets. A zero/partial config leaves those
// scopes off (admit-all) — the account scope is always enforced.
func NewStormControllerWithScopeBudget(queries *dbauth.Queries, cfg StormScopeConfig) *StormController {
	return &StormController{
		queries: queries,
		scope:   newStormScopeLimiter(cfg),
		now:     time.Now,
	}
}

func (c *StormController) clock() time.Time {
	if c == nil || c.now == nil {
		return time.Now()
	}
	return c.now()
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

// AcquireProviderEndpoint consumes one token from the per-(provider, endpoint)
// bucket. When the scope is unconfigured it admits. On admission it returns a
// refund function that the caller invokes ONLY if a later scope in the same
// acquire cascade denies — so a cascade rejection does not waste this scope's
// budget. The refund is a no-op when the scope is disabled. A denial returns
// OutcomeStormBudgetExhausted with a nil refund and no error.
func (c *StormController) AcquireProviderEndpoint(_ context.Context, _ int64, providerCode, endpointFingerprint string) (func(), Outcome, error) {
	if c == nil || c.scope == nil || !c.scope.cfg.endpointEnabled() {
		return func() {}, "", nil
	}
	bucket := c.scope.endpointBucket(providerCode + "|" + endpointFingerprint)
	if !bucket.tryAcquire(c.clock()) {
		return nil, OutcomeStormBudgetExhausted, nil
	}
	return func() { bucket.refund(c.clock()) }, "", nil
}

// AcquireGlobal consumes one token from the process-wide bucket. When the scope
// is unconfigured it admits. The returned function is a no-op (global is the last
// scope in the cascade, so it never needs a cascade refund). A denial returns
// OutcomeStormBudgetExhausted with a nil function and no error.
func (c *StormController) AcquireGlobal(_ context.Context, _ int64) (func(), Outcome, error) {
	if c == nil || c.scope == nil || c.scope.globalBucket == nil {
		return func() {}, "", nil
	}
	if !c.scope.globalBucket.tryAcquire(c.clock()) {
		return nil, OutcomeStormBudgetExhausted, nil
	}
	return func() {}, "", nil
}
