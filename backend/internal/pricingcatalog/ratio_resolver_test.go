package pricingcatalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestRatioResolverReturnsConfiguredRatioAndUsesCache(t *testing.T) {
	store := &ratioResolverStore{
		rows: map[ratioResolverKey]GroupPricingRatio{
			{tenantID: 7, poolGroupID: 42}: {TenantID: 7, PoolGroupID: 42, Ratio: decimal.RequireFromString("0.8"), RatioText: "0.8"},
		},
	}
	resolver := NewRatioResolver(store, time.Hour)

	first := mustResolveCatalogRatio(t, resolver, 7, 42)
	store.err = errors.New("backend temporarily unavailable")
	second := mustResolveCatalogRatio(t, resolver, 7, 42)

	assertCatalogDecimal(t, "first ratio", first, "0.8")
	assertCatalogDecimal(t, "cached ratio", second, "0.8")
	if store.getCalls != 1 {
		t.Fatalf("GetRatio calls=%d want 1 cached read", store.getCalls)
	}
}

func TestRatioResolverDefaultsToOneWhenRatioIsMissing(t *testing.T) {
	store := &ratioResolverStore{err: ErrNotFound}
	resolver := NewRatioResolver(store, time.Hour)

	got := mustResolveCatalogRatio(t, resolver, 7, 42)

	assertCatalogDecimal(t, "ratio", got, "1")
	if store.getCalls != 1 {
		t.Fatalf("GetRatio calls=%d want 1", store.getCalls)
	}
}

func TestRatioResolverBackendErrorWithoutLastKnownGoodServesDefaultWithAlert(t *testing.T) {
	store := &ratioResolverStore{err: ErrBackend}
	resolver := NewRatioResolver(store, time.Hour)
	before := SnapshotRatioResolverSignals()

	got, err := resolver.Resolve(context.Background(), 7, 42)

	// 判别性夹具: 旧 fail-closed 会返回 error/0 并导致上层 503；新行为必须放行但非静默告警。
	if err != nil {
		t.Fatalf("Resolve() error=%v want nil fail-open", err)
	}
	assertCatalogDecimal(t, "fallback ratio", got, "1")
	after := SnapshotRatioResolverSignals()
	if after.BackendErrorWithoutLKGTotal-before.BackendErrorWithoutLKGTotal != 1 {
		t.Fatalf("backend error metric delta=%d want 1", after.BackendErrorWithoutLKGTotal-before.BackendErrorWithoutLKGTotal)
	}
}

func TestRatioResolverBackendErrorUsesLastKnownGoodAndSignalsStale(t *testing.T) {
	store := &ratioResolverStore{
		rows: map[ratioResolverKey]GroupPricingRatio{
			{tenantID: 7, poolGroupID: 42}: {TenantID: 7, PoolGroupID: 42, Ratio: decimal.RequireFromString("0.8"), RatioText: "0.8"},
		},
	}
	resolver := NewRatioResolver(store, time.Nanosecond)
	assertCatalogDecimal(t, "first ratio", mustResolveCatalogRatio(t, resolver, 7, 42), "0.8")
	time.Sleep(time.Millisecond)
	store.err = ErrBackend
	before := SnapshotRatioResolverSignals()

	got := mustResolveCatalogRatio(t, resolver, 7, 42)

	assertCatalogDecimal(t, "stale ratio", got, "0.8")
	after := SnapshotRatioResolverSignals()
	if after.StaleOnBackendErrorTotal-before.StaleOnBackendErrorTotal != 1 {
		t.Fatalf("stale metric delta=%d want 1", after.StaleOnBackendErrorTotal-before.StaleOnBackendErrorTotal)
	}
}

func TestRatioResolverFailsSafeToOneForNilStoreOrInvalidScope(t *testing.T) {
	resolver := NewRatioResolver(nil, time.Hour)

	assertCatalogDecimal(t, "nil store ratio", mustResolveCatalogRatio(t, resolver, 7, 42), "1")

	store := &ratioResolverStore{}
	resolver = NewRatioResolver(store, time.Hour)
	assertCatalogDecimal(t, "invalid scope ratio", mustResolveCatalogRatio(t, resolver, 0, 42), "1")
	if store.getCalls != 0 {
		t.Fatalf("GetRatio calls=%d want 0 for invalid scope", store.getCalls)
	}
}

type ratioResolverKey struct {
	tenantID    int64
	poolGroupID int64
}

type ratioResolverStore struct {
	rows     map[ratioResolverKey]GroupPricingRatio
	err      error
	getCalls int
}

func (s *ratioResolverStore) GetRatio(_ context.Context, tenantID, poolGroupID int64) (GroupPricingRatio, error) {
	s.getCalls++
	if s.err != nil {
		return GroupPricingRatio{}, s.err
	}
	row, ok := s.rows[ratioResolverKey{tenantID: tenantID, poolGroupID: poolGroupID}]
	if !ok {
		return GroupPricingRatio{}, ErrNotFound
	}
	return row, nil
}

func (s *ratioResolverStore) ListRatios(context.Context, int64) ([]GroupPricingRatio, error) {
	return nil, errors.New("unexpected ListRatios")
}

func (s *ratioResolverStore) UpsertRatio(context.Context, UpsertRatioParams) (GroupPricingRatio, error) {
	return GroupPricingRatio{}, errors.New("unexpected UpsertRatio")
}

func (s *ratioResolverStore) DeleteRatio(context.Context, DeleteRatioParams) error {
	return errors.New("unexpected DeleteRatio")
}

func assertCatalogDecimal(t *testing.T, field string, got decimal.Decimal, want string) {
	t.Helper()
	wantDecimal := decimal.RequireFromString(want)
	if !got.Equal(wantDecimal) {
		t.Fatalf("%s=%s want %s", field, got, wantDecimal)
	}
}

func mustResolveCatalogRatio(t *testing.T, resolver *RatioResolver, tenantID, poolGroupID int64) decimal.Decimal {
	t.Helper()
	got, err := resolver.Resolve(context.Background(), tenantID, poolGroupID)
	if err != nil {
		t.Fatalf("Resolve() error=%v", err)
	}
	return got
}
