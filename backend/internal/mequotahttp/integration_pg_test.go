//go:build integration_pg

package mequotahttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

func openMeQuotaPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 4})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type meQuotaFixture struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	suffix  string
	tenants []int64
	tenantA int64
	userA   int64
	userB   int64
}

func newMeQuotaFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *meQuotaFixture {
	t.Helper()
	f := &meQuotaFixture{t: t, ctx: ctx, pool: pool, suffix: uuid.NewString()}
	f.tenantA = f.seedTenant("me-quota-a")
	f.userA = f.seedUser(f.tenantA, "a")
	f.userB = f.seedUser(f.tenantA, "b")
	t.Cleanup(f.cleanup)
	return f
}

func (f *meQuotaFixture) seedTenant(label string) int64 {
	f.t.Helper()
	var tenantID int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, label+"-"+f.suffix).Scan(&tenantID); err != nil {
		f.t.Fatalf("seed tenant: %v", err)
	}
	f.tenants = append(f.tenants, tenantID)
	return tenantID
}

func (f *meQuotaFixture) seedUser(tenantID int64, label string) int64 {
	f.t.Helper()
	var userID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "user-"+label+"-"+f.suffix,
	).Scan(&userID); err != nil {
		f.t.Fatalf("seed user: %v", err)
	}
	return userID
}

func (f *meQuotaFixture) seedQuotaWindow(tenantID, userID int64, at time.Time, limit, reserved, settled string) {
	f.t.Helper()
	var policyID int64
	scopeID := strconv.FormatInt(userID, 10)
	if err := f.pool.QueryRow(f.ctx, `
INSERT INTO quota_policies (
	tenant_id, scope_kind, scope_id, metric, window_kind, window_seconds,
	limit_value, burst_value, mode, priority, enabled, valid_from, valid_until
) VALUES (
	$1, 'user', $2, 'cost_usd', 'calendar_day', 0,
	$3::numeric(20,8), 0, 'enforce', 10, true, $4, $5
) RETURNING id`, tenantID, scopeID, limit, at.Add(-time.Hour), at.Add(24*time.Hour)).Scan(&policyID); err != nil {
		f.t.Fatalf("seed quota policy: %v", err)
	}
	start, end, ok := quota.ComputeWindow(quota.WindowCalendarDay, 0, at)
	if !ok {
		f.t.Fatal("calendar day window did not compute")
	}
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO quota_windows (
	tenant_id, policy_id, window_start, window_end, reserved_value,
	settled_value, overage_value, request_count
) VALUES (
	$1, $2, $3, $4, $5::numeric(20,8),
	$6::numeric(20,8), 0, 9
)`, tenantID, policyID, start, end, reserved, settled); err != nil {
		f.t.Fatalf("seed quota window: %v", err)
	}
}

func (f *meQuotaFixture) cleanup() {
	ctx := context.Background()
	for _, tenantID := range f.tenants {
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_windows WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_policies WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}
}

func TestMeQuotaSelfScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openMeQuotaPool(t, ctx)
	f := newMeQuotaFixture(t, ctx, pool)
	at := time.Now().UTC()
	f.seedQuotaWindow(f.tenantA, f.userA, at, "10", "3", "2")
	f.seedQuotaWindow(f.tenantA, f.userB, at, "99", "0", "0")
	h := NewHandler(Deps{
		Auth:  authStub{identity: auth.Identity{TenantID: f.tenantA, UserID: f.userA}},
		Store: quota.NewPostgresStore(pool),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/quota?user_id="+strconv.FormatInt(f.userB, 10)+"&scope_id="+strconv.FormatInt(f.userB, 10), nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	// MUTATION: handler hard-codes userB scopeID, passes "", or accepts query scope;
	// user A sees userB/all windows and this assertion goes RED.
	if len(body.Items) != 1 {
		t.Fatalf("items len=%d want only user A quota window; body=%s", len(body.Items), rec.Body.String())
	}
	item := body.Items[0]
	if item["cap"] != "10" || item["remaining"] != "5" {
		t.Fatalf("cap/remaining=%v/%v want 10/5 body=%s", item["cap"], item["remaining"], rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"99"`) {
		t.Fatalf("response leaked user B quota window: %s", rec.Body.String())
	}
}
