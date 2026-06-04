package pricingcatalog

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	pricingcatalogdb "github.com/BloomingProsperity/HUAKAI/internal/db/pricingcatalog"
)

func TestPostgresStore_ReturnsMissingRatioErrNotFound(t *testing.T) {
	store := newPostgresStore(&fakeRatioQueries{getErr: pgx.ErrNoRows})

	_, err := store.GetRatio(context.Background(), 10, 20)

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRatio error=%v want ErrNotFound", err)
	}
}

func TestPostgresStore_TenantIsolation(t *testing.T) {
	assertPricingCatalogQueryContains(t, "ListPoolGroupPricingRatios", "WHERE tenant_id = $1")
	store := newPostgresStore(&fakeRatioQueries{
		listRows: []pricingcatalogdb.ListPoolGroupPricingRatiosRow{{
			ID:          1,
			TenantID:    77,
			PoolGroupID: 88,
			Ratio:       "1.25000000",
		}},
	})

	rows, err := store.ListRatios(context.Background(), 77)
	if err != nil {
		t.Fatalf("ListRatios error=%v", err)
	}
	if len(rows) != 1 || rows[0].TenantID != 77 || rows[0].PoolGroupID != 88 {
		t.Fatalf("ListRatios rows=%+v want tenant 77 group 88", rows)
	}
	if got := store.queries.(*fakeRatioQueries).listTenantID; got != 77 {
		t.Fatalf("ListRatios tenantID arg=%d want 77", got)
	}
}

func TestPostgresStore_UpsertQueryIsIdempotent(t *testing.T) {
	assertPricingCatalogQueryContains(t, "UpsertPoolGroupPricingRatio", "ON CONFLICT (tenant_id, pool_group_id) DO UPDATE")
}

func TestPostgresStore_BackendErrorPropagated(t *testing.T) {
	backendErr := errors.New("database unavailable")
	store := newPostgresStore(&fakeRatioQueries{listErr: backendErr})

	_, err := store.ListRatios(context.Background(), 7)

	if !errors.Is(err, ErrBackend) {
		t.Fatalf("ListRatios error=%v want ErrBackend", err)
	}
	if !errors.Is(err, backendErr) {
		t.Fatalf("ListRatios error=%v should wrap backend cause", err)
	}
}

func TestPostgresStore_MoneyPrecisionPreservesExactDecimalString(t *testing.T) {
	const exact = "123456789.12345678"
	store := newPostgresStore(&fakeRatioQueries{
		getRow: pricingcatalogdb.GetPoolGroupPricingRatioRow{
			ID:          1,
			TenantID:    7,
			PoolGroupID: 9,
			Ratio:       exact,
		},
	})

	got, err := store.GetRatio(context.Background(), 7, 9)
	if err != nil {
		t.Fatalf("GetRatio error=%v", err)
	}
	if got.RatioString() != exact {
		t.Fatalf("ratio string=%q want exact %q", got.RatioString(), exact)
	}
}

func assertPricingCatalogQueryContains(t *testing.T, queryName, want string) {
	t.Helper()
	body, err := os.ReadFile("../../sql/queries/pricing_catalog.sql")
	if err != nil {
		t.Fatalf("read pricing catalog queries: %v", err)
	}
	text := string(body)
	start := strings.Index(text, "-- name: "+queryName+" ")
	if start < 0 {
		t.Fatalf("query %s not found", queryName)
	}
	next := strings.Index(text[start+1:], "-- name: ")
	section := text[start:]
	if next >= 0 {
		section = text[start : start+1+next]
	}
	if !strings.Contains(section, want) {
		t.Fatalf("query %s missing %q:\n%s", queryName, want, section)
	}
}

type fakeRatioQueries struct {
	getRow       pricingcatalogdb.GetPoolGroupPricingRatioRow
	getErr       error
	listRows     []pricingcatalogdb.ListPoolGroupPricingRatiosRow
	listErr      error
	listTenantID int64
	upsertRow    pricingcatalogdb.UpsertPoolGroupPricingRatioRow
	upsertErr    error
	upsertCalls  int
	deleteErr    error
}

func (f *fakeRatioQueries) GetPoolGroupPricingRatio(context.Context, pricingcatalogdb.GetPoolGroupPricingRatioParams) (pricingcatalogdb.GetPoolGroupPricingRatioRow, error) {
	return f.getRow, f.getErr
}

func (f *fakeRatioQueries) ListPoolGroupPricingRatios(_ context.Context, tenantID int64) ([]pricingcatalogdb.ListPoolGroupPricingRatiosRow, error) {
	f.listTenantID = tenantID
	return f.listRows, f.listErr
}

func (f *fakeRatioQueries) UpsertPoolGroupPricingRatio(context.Context, pricingcatalogdb.UpsertPoolGroupPricingRatioParams) (pricingcatalogdb.UpsertPoolGroupPricingRatioRow, error) {
	f.upsertCalls++
	return f.upsertRow, f.upsertErr
}

func (f *fakeRatioQueries) DeletePoolGroupPricingRatio(context.Context, pricingcatalogdb.DeletePoolGroupPricingRatioParams) (int64, error) {
	return 1, f.deleteErr
}
