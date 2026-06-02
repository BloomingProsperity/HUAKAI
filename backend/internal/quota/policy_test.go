package quota

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestResolvePolicies_FiltersGroupsAndOrders(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	tenantID := int64(42)
	userScope := Scope{TenantID: tenantID, Kind: ScopeUser, ID: "u1"}
	keyScope := Scope{TenantID: tenantID, Kind: ScopeAPIKey, ID: "k1"}
	store := policyListStoreFunc(func(_ context.Context, filter PolicyFilter) ([]Policy, error) {
		if filter.TenantID != tenantID || len(filter.Scopes) != 2 || len(filter.Metrics) != 2 || !filter.At.Equal(at) {
			t.Fatalf("unexpected filter: %+v", filter)
		}
		return []Policy{
			{
				TenantID:   tenantID,
				ID:         30,
				Scope:      keyScope,
				Metric:     MetricCostUSD,
				LimitValue: decimal.NewFromInt(100),
				Mode:       ModeEnforce,
				Priority:   10,
			},
			{
				TenantID:   tenantID,
				ID:         20,
				Scope:      userScope,
				Metric:     MetricCostUSD,
				LimitValue: decimal.NewFromInt(10),
				Mode:       ModeObserve,
				Priority:   10,
			},
			{
				TenantID:   tenantID,
				ID:         21,
				Scope:      userScope,
				Metric:     MetricRequests,
				LimitValue: decimal.NewFromInt(5),
				Mode:       ModeDisabled,
				Priority:   10,
			},
			{
				TenantID:   tenantID,
				ID:         22,
				Scope:      Scope{TenantID: tenantID, Kind: ScopeUser, ID: "decoy"},
				Metric:     MetricCostUSD,
				LimitValue: decimal.NewFromInt(1),
				Mode:       ModeEnforce,
				Priority:   1,
			},
		}, nil
	})

	resolved, err := ResolvePolicies(ctx, store, tenantID, []Scope{keyScope, userScope}, []Metric{MetricCostUSD, MetricRequests}, at)
	if err != nil {
		t.Fatalf("ResolvePolicies: %v", err)
	}
	if len(resolved.Ordered) != 2 {
		t.Fatalf("ordered policies len=%d; want 2, got %+v", len(resolved.Ordered), resolved.Ordered)
	}
	if resolved.Ordered[0].Scope.Kind != ScopeUser || resolved.Ordered[1].Scope.Kind != ScopeAPIKey {
		t.Fatalf("lock order drift: got %s then %s", resolved.Ordered[0].Scope.Kind, resolved.Ordered[1].Scope.Kind)
	}
	userCost := resolved.Groups[PolicyGroupKey{ScopeKind: ScopeUser, Metric: MetricCostUSD}]
	if len(userCost.Observe) != 1 || userCost.Observe[0].ID != 20 {
		t.Fatalf("user cost observe group=%+v; want policy 20", userCost)
	}
	if len(userCost.Enforce) != 0 || len(userCost.ManualFirst) != 0 {
		t.Fatalf("user cost wrong mode classification: %+v", userCost)
	}
	keyCost := resolved.Groups[PolicyGroupKey{ScopeKind: ScopeAPIKey, Metric: MetricCostUSD}]
	if len(keyCost.Enforce) != 1 || keyCost.Enforce[0].ID != 30 {
		t.Fatalf("key cost enforce group=%+v; want policy 30", keyCost)
	}
	if _, ok := resolved.Groups[PolicyGroupKey{ScopeKind: ScopeUser, Metric: MetricRequests}]; ok {
		t.Fatalf("disabled requests policy should be skipped")
	}
}

type policyListStoreFunc func(context.Context, PolicyFilter) ([]Policy, error)

func (f policyListStoreFunc) ListActivePolicies(ctx context.Context, filter PolicyFilter) ([]Policy, error) {
	return f(ctx, filter)
}
