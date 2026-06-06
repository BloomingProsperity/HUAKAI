//go:build integration_pg

package audit

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// Mutation: drop tenant_id from ListDisputesForAdmin WHERE.
// Tenant B's dispute would leak into tenant A's admin list.
func TestAdminListDisputes_TenantScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openDisputeAdminListPool(t, ctx)
	f := newDisputeAdminListFixture(t, ctx, pool)
	store := newDisputeAdminListStore(t, pool)
	base := time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)

	f.seedDispute(t, "tenant-a-user-a", f.tenantA, f.userA1, DisputeStatusOpen, base)
	f.seedDispute(t, "tenant-a-user-b", f.tenantA, f.userA2, DisputeStatusReviewing, base.Add(time.Minute))
	f.seedDispute(t, "tenant-b-user", f.tenantB, f.userB1, DisputeStatusOpen, base.Add(2*time.Minute))

	rows, err := store.ListForAdmin(ctx, f.tenantA, "", 20, 0)
	if err != nil {
		t.Fatalf("ListForAdmin tenant scoped: %v", err)
	}
	got := disputeRequestIDs(rows)
	want := map[string]bool{"tenant-a-user-a": true, "tenant-a-user-b": true}
	if len(got) != len(want) {
		t.Fatalf("request_ids=%v want exactly tenant A rows %v", got, want)
	}
	for _, row := range rows {
		if row.TenantID != f.tenantA {
			t.Fatalf("tenant leak row=%+v want tenant_id=%d", row, f.tenantA)
		}
		if !want[row.RequestID] {
			t.Fatalf("unexpected request_id=%q rows=%+v", row.RequestID, rows)
		}
	}
}

// Mutation: ignore status_filter in ListDisputesForAdmin.
// Open/rejected disputes would appear in a resolved-only page.
func TestAdminListDisputes_StatusFilter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openDisputeAdminListPool(t, ctx)
	f := newDisputeAdminListFixture(t, ctx, pool)
	store := newDisputeAdminListStore(t, pool)
	base := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)

	f.seedDispute(t, "tenant-a-open", f.tenantA, f.userA1, DisputeStatusOpen, base)
	f.seedDispute(t, "tenant-a-resolved", f.tenantA, f.userA2, DisputeStatusResolved, base.Add(time.Minute))
	f.seedDispute(t, "tenant-a-rejected", f.tenantA, f.userA2, DisputeStatusRejected, base.Add(2*time.Minute))
	f.seedDispute(t, "tenant-b-resolved", f.tenantB, f.userB1, DisputeStatusResolved, base.Add(3*time.Minute))

	rows, err := store.ListForAdmin(ctx, f.tenantA, DisputeStatusResolved, 20, 0)
	if err != nil {
		t.Fatalf("ListForAdmin status filter: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestID != "tenant-a-resolved" || rows[0].Status != DisputeStatusResolved {
		t.Fatalf("rows=%+v want only tenant-a-resolved", rows)
	}
}

// Mutation: ignore the store limit cap or offset_rows.
// A limit above the cap would return all rows, or offset=1 would still return the newest row.
func TestAdminListDisputes_Pagination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := openDisputeAdminListPool(t, ctx)
	f := newDisputeAdminListFixture(t, ctx, pool)
	store := newDisputeAdminListStore(t, pool)
	base := time.Date(2026, 6, 6, 11, 0, 0, 0, time.UTC)

	for i := 0; i < 503; i++ {
		f.seedDispute(t, fmt.Sprintf("page-%03d", i), f.tenantA, f.userA1, DisputeStatusOpen, base.Add(time.Duration(i)*time.Second))
	}
	capped, err := store.ListForAdmin(ctx, f.tenantA, "", 999, 0)
	if err != nil {
		t.Fatalf("ListForAdmin capped page: %v", err)
	}
	if len(capped) != int(maxDisputeListLimit) {
		t.Fatalf("capped len=%d want %d", len(capped), maxDisputeListLimit)
	}

	page, err := store.ListForAdmin(ctx, f.tenantA, "", 2, 1)
	if err != nil {
		t.Fatalf("ListForAdmin offset page: %v", err)
	}
	if len(page) != 2 || page[0].RequestID != "page-501" || page[1].RequestID != "page-500" {
		t.Fatalf("page request_ids=%v want [page-501 page-500]", disputeRequestIDs(page))
	}
}

// Mutation: implement admin list by calling ListUserCostDisputes with one user's id.
// The tenant admin would see only that user's dispute instead of all tenant users.
func TestAdminListDisputes_AdminListMustNotBeUserScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openDisputeAdminListPool(t, ctx)
	f := newDisputeAdminListFixture(t, ctx, pool)
	store := newDisputeAdminListStore(t, pool)
	base := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)

	f.seedDispute(t, "multi-user-a", f.tenantA, f.userA1, DisputeStatusOpen, base)
	f.seedDispute(t, "multi-user-b", f.tenantA, f.userA2, DisputeStatusOpen, base.Add(time.Minute))

	rows, err := store.ListForAdmin(ctx, f.tenantA, "", 20, 0)
	if err != nil {
		t.Fatalf("ListForAdmin multi-user: %v", err)
	}
	seenUsers := map[int64]bool{}
	for _, row := range rows {
		seenUsers[row.UserID] = true
	}
	if len(rows) != 2 || !seenUsers[f.userA1] || !seenUsers[f.userA2] {
		t.Fatalf("rows=%+v want disputes from users %d and %d", rows, f.userA1, f.userA2)
	}
}

type disputeAdminListFixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	tenantA int64
	tenantB int64
	userA1  int64
	userA2  int64
	userB1  int64
	suffix  string
}

func openDisputeAdminListPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newDisputeAdminListFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *disputeAdminListFixture {
	t.Helper()
	suffix := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	f := &disputeAdminListFixture{ctx: ctx, pool: pool, suffix: suffix}
	f.tenantA = f.seedTenant(t, "dispute-admin-a-"+suffix)
	f.tenantB = f.seedTenant(t, "dispute-admin-b-"+suffix)
	f.userA1 = f.seedUser(t, f.tenantA, "user-a1-"+suffix)
	f.userA2 = f.seedUser(t, f.tenantA, "user-a2-"+suffix)
	f.userB1 = f.seedUser(t, f.tenantB, "user-b1-"+suffix)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM cost_disputes WHERE tenant_id IN ($1, $2)`, f.tenantA, f.tenantB)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id IN ($1, $2)`, f.tenantA, f.tenantB)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id IN ($1, $2)`, f.tenantA, f.tenantB)
	})
	return f
}

func newDisputeAdminListStore(t *testing.T, pool *pgxpool.Pool) *CostDisputeStore {
	t.Helper()
	store, err := NewPGXDisputeStore(pool)
	if err != nil {
		t.Fatalf("NewPGXDisputeStore: %v", err)
	}
	return store
}

func (f *disputeAdminListFixture) seedTenant(t *testing.T, name string) int64 {
	t.Helper()
	var id int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("seed tenant %q: %v", name, err)
	}
	return id
}

func (f *disputeAdminListFixture) seedUser(t *testing.T, tenantID int64, name string) int64 {
	t.Helper()
	var id int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, name,
	).Scan(&id); err != nil {
		t.Fatalf("seed user %q: %v", name, err)
	}
	return id
}

func (f *disputeAdminListFixture) seedDispute(t *testing.T, requestID string, tenantID, userID int64, status string, createdAt time.Time) int64 {
	t.Helper()
	var id int64
	resolvedAt := any(nil)
	if status == DisputeStatusResolved || status == DisputeStatusRejected {
		resolvedAt = createdAt.Add(time.Minute)
	}
	if err := f.pool.QueryRow(f.ctx, `
INSERT INTO cost_disputes (
    dispute_id, tenant_id, user_id, request_id, reason, status, created_at, resolved_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
) RETURNING id`,
		"disp-"+f.suffix+"-"+requestID,
		tenantID,
		userID,
		requestID,
		"cost does not match receipt",
		status,
		createdAt,
		resolvedAt,
	).Scan(&id); err != nil {
		t.Fatalf("seed dispute %q: %v", requestID, err)
	}
	return id
}

func disputeRequestIDs(rows []CostDispute) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.RequestID)
	}
	return out
}
