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

	first := resolver.Resolve(context.Background(), 7, 42)
	store.err = errors.New("backend temporarily unavailable")
	second := resolver.Resolve(context.Background(), 7, 42)

	assertCatalogDecimal(t, "first ratio", first, "0.8")
	assertCatalogDecimal(t, "cached ratio", second, "0.8")
	if store.getCalls != 1 {
		t.Fatalf("GetRatio calls=%d want 1 cached read", store.getCalls)
	}
}

func TestRatioResolverFailsSafeToOneForMissingAndBackendError(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "missing", err: ErrNotFound},
		{name: "backend error", err: ErrBackend},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &ratioResolverStore{err: tt.err}
			resolver := NewRatioResolver(store, time.Hour)

			got := resolver.Resolve(context.Background(), 7, 42)

			assertCatalogDecimal(t, "ratio", got, "1")
			if store.getCalls != 1 {
				t.Fatalf("GetRatio calls=%d want 1", store.getCalls)
			}
		})
	}
}

func TestRatioResolverFailsSafeToOneForNilStoreOrInvalidScope(t *testing.T) {
	resolver := NewRatioResolver(nil, time.Hour)

	assertCatalogDecimal(t, "nil store ratio", resolver.Resolve(context.Background(), 7, 42), "1")

	store := &ratioResolverStore{}
	resolver = NewRatioResolver(store, time.Hour)
	assertCatalogDecimal(t, "invalid scope ratio", resolver.Resolve(context.Background(), 0, 42), "1")
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
