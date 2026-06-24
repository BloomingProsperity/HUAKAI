package dispatcher

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

func TestDefaultSelectorSkipsThrottledAccountSnapshot(t *testing.T) {
	// Regression killed: health_state filtering must happen before ranking.
	// Mutation self-check: deleting the health gate makes account 101 win on
	// priority and this test turns red.
	now := time.Date(2026, 5, 25, 9, 0, 0, 0, time.UTC)
	src := &healthStateAccountSource{accounts: []*AccountSnapshot{
		{ID: 101, TenantID: 7, Priority: 1, MaxConcurrency: 4, HealthState: "throttled", HealthStateUntil: now.Add(3 * time.Minute)},
		{ID: 202, TenantID: 7, Priority: 9, MaxConcurrency: 4, HealthState: "healthy"},
	}}
	sel := router.NewDefaultSelector(src,
		router.WithNow(func() time.Time { return now }),
		router.WithSlotManager(healthStateSlotManager{}),
	)

	res, err := sel.Select(context.Background(), SelectionRequest{TenantID: 7, PoolGroupID: 3})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID != 202 {
		t.Fatalf("selected account=%d, want healthy fallback 202", res.AccountID)
	}
}

func TestDefaultSelectorReactivatesExpiredHealthStateSnapshot(t *testing.T) {
	// Regression killed: a revoked account with an expired deadline must become
	// eligible again. Mutation self-check: treating revoked as permanent makes
	// account 202 win and this test turns red.
	now := time.Date(2026, 5, 25, 9, 0, 0, 0, time.UTC)
	src := &healthStateAccountSource{accounts: []*AccountSnapshot{
		{ID: 101, TenantID: 7, Priority: 1, MaxConcurrency: 4, HealthState: "revoked", HealthStateUntil: now.Add(-time.Minute)},
		{ID: 202, TenantID: 7, Priority: 9, MaxConcurrency: 4, HealthState: "healthy"},
	}}
	sel := router.NewDefaultSelector(src,
		router.WithNow(func() time.Time { return now }),
		router.WithSlotManager(healthStateSlotManager{}),
	)

	res, err := sel.Select(context.Background(), SelectionRequest{TenantID: 7, PoolGroupID: 3})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID != 101 {
		t.Fatalf("selected account=%d, want expired revoked account 101 to recover", res.AccountID)
	}
}

func TestDefaultSelectorSkipsActiveModelRateLimitSnapshot(t *testing.T) {
	// Regression killed: model_rate_limits must be an account+model gate before
	// ranking. Mutation self-check: replacing the model gate with AllowAllGate
	// makes account 101 win on priority and this test turns red.
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	src := &healthStateAccountSource{accounts: []*AccountSnapshot{
		{
			ID:             101,
			TenantID:       7,
			Priority:       1,
			MaxConcurrency: 4,
			HealthState:    "healthy",
			ModelRateLimits: map[string]ModelRateLimit{
				"upstream-gpt-4o": {
					RateLimitResetAt: now.Add(5 * time.Minute),
					Reason:           "model_limit_exceeded",
				},
			},
		},
		{ID: 202, TenantID: 7, Priority: 9, MaxConcurrency: 4, HealthState: "healthy"},
	}}
	sel := router.NewDefaultSelector(src,
		router.WithNow(func() time.Time { return now }),
		router.WithSlotManager(healthStateSlotManager{}),
	)

	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID:         7,
		PoolGroupID:      3,
		RequestedModel:   "public-gpt-4o",
		ModelCooldownKey: "upstream-gpt-4o",
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID != 202 {
		t.Fatalf("selected account=%d, want healthy non-cooled fallback 202", res.AccountID)
	}
}

type healthStateAccountSource struct {
	accounts []*AccountSnapshot
}

func (s *healthStateAccountSource) ListAccounts(context.Context, SelectionRequest) ([]*AccountSnapshot, error) {
	out := make([]*AccountSnapshot, 0, len(s.accounts))
	for _, account := range s.accounts {
		cp := *account
		out = append(out, &cp)
	}
	return out, nil
}

type healthStateSlotManager struct{}

func (healthStateSlotManager) Acquire(context.Context, *AccountSnapshot, SelectionRequest) (*AcquireResult, error) {
	return &AcquireResult{AcquisitionToken: uuid.New()}, nil
}
