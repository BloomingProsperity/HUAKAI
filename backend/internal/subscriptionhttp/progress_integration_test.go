// HUAKAI · iKun
//go:build integration_pg

package subscriptionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

func openSubProgressPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 8})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type subProgressFixture struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	suffix   string
	tenants  []int64
	tenantID int64
	userA    int64
	userB    int64
	tenantB  int64
	userC    int64
}

func newSubProgressFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *subProgressFixture {
	t.Helper()
	f := &subProgressFixture{t: t, ctx: ctx, pool: pool, suffix: uuid.NewString()}
	f.tenantID = f.seedTenant("sub-progress-a")
	f.userA = f.seedUser(f.tenantID, "a")
	f.userB = f.seedUser(f.tenantID, "b")
	f.tenantB = f.seedTenant("sub-progress-b")
	f.userC = f.seedUser(f.tenantB, "c")
	t.Cleanup(f.cleanup)
	return f
}

func (f *subProgressFixture) seedTenant(label string) int64 {
	f.t.Helper()
	var tenantID int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, label+"-"+f.suffix).Scan(&tenantID); err != nil {
		f.t.Fatalf("seed tenant: %v", err)
	}
	f.tenants = append(f.tenants, tenantID)
	return tenantID
}

func (f *subProgressFixture) seedUser(tenantID int64, label string) int64 {
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

func (f *subProgressFixture) seedActiveSubscription(tenantID, userID int64, at time.Time, cap string) int64 {
	f.t.Helper()
	var planID int64
	if err := f.pool.QueryRow(f.ctx, `
INSERT INTO subscription_plans (
	tenant_id, name, description, validity_days, granted_group, daily_cap_usd,
	for_sale, enabled
) VALUES ($1, $2, '', 30, 'premium', $3::numeric(20,8), true, true)
RETURNING id`, tenantID, "plan-"+uuid.NewString(), cap).Scan(&planID); err != nil {
		f.t.Fatalf("seed subscription plan: %v", err)
	}
	var subID int64
	if err := f.pool.QueryRow(f.ctx, `
INSERT INTO user_subscriptions (
	tenant_id, user_id, plan_id, granted_group, daily_cap_usd, status, source,
	starts_at, expires_at, prev_user_group
) VALUES ($1, $2, $3, 'premium', $4::numeric(20,8), 'active', 'admin',
	$5, $6, 'default')
RETURNING id`, tenantID, userID, planID, cap, at.Add(-time.Hour), at.Add(30*24*time.Hour)).Scan(&subID); err != nil {
		f.t.Fatalf("seed active subscription: %v", err)
	}
	return subID
}

func (f *subProgressFixture) seedQuotaWindow(tenantID, userID int64, at time.Time, limit, settled, reserved, overage string, requests int64) int64 {
	f.t.Helper()
	scopeID := strconv.FormatInt(userID, 10)
	var policyID int64
	if err := f.pool.QueryRow(f.ctx, `
INSERT INTO quota_policies (
	tenant_id, scope_kind, scope_id, metric, window_kind, window_seconds,
	limit_value, burst_value, mode, priority, enabled, valid_from, valid_until
) VALUES (
	$1, 'user', $2, 'cost_usd', 'calendar_day', 0,
	$3::numeric(20,8), 0, 'enforce', 10, true, $4, $5
) RETURNING id`, tenantID, scopeID, limit, at.Add(-time.Hour), at.Add(30*24*time.Hour)).Scan(&policyID); err != nil {
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
	$6::numeric(20,8), $7::numeric(20,8), $8
)`, tenantID, policyID, start, end, reserved, settled, overage, requests); err != nil {
		f.t.Fatalf("seed quota window: %v", err)
	}
	return policyID
}

func (f *subProgressFixture) cleanup() {
	ctx := context.Background()
	for _, tenantID := range f.tenants {
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_windows WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_policies WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM subscription_policy_links WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM user_subscriptions WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM subscription_plans WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}
}

func newSubProgressRouter(d UserDeps, ident *sessionauth.SessionIdentity) http.Handler {
	r := chi.NewRouter()
	r.Route("/subs", func(r chi.Router) {
		if ident != nil {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					next.ServeHTTP(w, req.WithContext(sessionauth.ContextWithSession(req.Context(), *ident)))
				})
			})
		}
		MountSubscriptionUserRoutes(r, d)
	})
	return r
}

type progressResponse struct {
	Subscription *subscriptionView `json:"subscription"`
	Progress     []struct {
		WindowKind   string    `json:"window_kind"`
		Cap          string    `json:"cap"`
		Consumed     string    `json:"consumed"`
		Remaining    string    `json:"remaining"`
		Overage      string    `json:"overage"`
		RequestCount int64     `json:"request_count"`
		WindowStart  time.Time `json:"window_start"`
		WindowEnd    time.Time `json:"window_end"`
	} `json:"progress"`
}

func decodeProgressResponse(t *testing.T, rec *httptest.ResponseRecorder) progressResponse {
	t.Helper()
	var out progressResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode progress response: %v body=%s", err, rec.Body.String())
	}
	return out
}

func TestSubProgress_ConsumedVsCap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openSubProgressPool(t, ctx)
	f := newSubProgressFixture(t, ctx, pool)
	at := time.Now().UTC()
	f.seedActiveSubscription(f.tenantID, f.userA, at, "10")
	f.seedQuotaWindow(f.tenantID, f.userA, at, "10", "3", "1", "0.25", 12)
	router := newSubProgressRouter(UserDeps{
		Service: subscription.NewService(subscription.NewPostgresStore(pool)),
		Quota:   quota.NewPostgresStore(pool),
	}, &sessionauth.SessionIdentity{TenantID: f.tenantID, UserID: f.userA})

	req := httptest.NewRequest(http.MethodGet, "/subs/me/progress", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	out := decodeProgressResponse(t, rec)
	if out.Subscription == nil {
		t.Fatalf("subscription = nil, want current active subscription; body=%s", rec.Body.String())
	}
	if len(out.Progress) != 1 {
		t.Fatalf("progress len = %d, want 1; body=%s", len(out.Progress), rec.Body.String())
	}
	got := out.Progress[0]
	// MUTATION: compute remaining as cap (ignore consumed) or consumed=settled only (drop reserved) -> this assertion goes RED.
	if got.Cap != "10" || got.Consumed != "4" || got.Remaining != "6" {
		t.Fatalf("cap/consumed/remaining = %s/%s/%s, want 10/4/6", got.Cap, got.Consumed, got.Remaining)
	}
	if got.Overage != "0.25" || got.RequestCount != 12 {
		t.Fatalf("overage/request_count = %s/%d, want 0.25/12", got.Overage, got.RequestCount)
	}
}

func TestSubProgress_SelfScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openSubProgressPool(t, ctx)
	f := newSubProgressFixture(t, ctx, pool)
	at := time.Now().UTC()
	f.seedActiveSubscription(f.tenantID, f.userA, at, "10")
	f.seedActiveSubscription(f.tenantID, f.userB, at, "50")
	f.seedActiveSubscription(f.tenantB, f.userC, at, "90")
	f.seedQuotaWindow(f.tenantID, f.userA, at, "10", "3", "1", "0", 4)
	f.seedQuotaWindow(f.tenantID, f.userB, at, "50", "40", "5", "0", 45)
	f.seedQuotaWindow(f.tenantB, f.userC, at, "90", "80", "1", "0", 81)
	router := newSubProgressRouter(UserDeps{
		Service: subscription.NewService(subscription.NewPostgresStore(pool)),
		Quota:   quota.NewPostgresStore(pool),
	}, &sessionauth.SessionIdentity{TenantID: f.tenantID, UserID: f.userA})

	req := httptest.NewRequest(http.MethodGet, "/subs/me/progress", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	out := decodeProgressResponse(t, rec)
	// MUTATION: drop tenant_id or user scope predicates from ListCurrentWindowsForScope -> user B/tenant B windows leak and this goes RED.
	if len(out.Progress) != 1 {
		t.Fatalf("progress len = %d, want only caller's one window; body=%s", len(out.Progress), rec.Body.String())
	}
	if out.Progress[0].Cap != "10" || out.Progress[0].Consumed != "4" {
		t.Fatalf("caller progress = cap %s consumed %s, want only user A cap 10 consumed 4; body=%s",
			out.Progress[0].Cap, out.Progress[0].Consumed, rec.Body.String())
	}
}

func TestSubProgress_NoActiveSub(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openSubProgressPool(t, ctx)
	f := newSubProgressFixture(t, ctx, pool)
	at := time.Now().UTC()
	f.seedQuotaWindow(f.tenantID, f.userA, at, "10", "3", "1", "0", 4)
	router := newSubProgressRouter(UserDeps{
		Service: subscription.NewService(subscription.NewPostgresStore(pool)),
		Quota:   quota.NewPostgresStore(pool),
	}, &sessionauth.SessionIdentity{TenantID: f.tenantID, UserID: f.userA})

	req := httptest.NewRequest(http.MethodGet, "/subs/me/progress", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	out := decodeProgressResponse(t, rec)
	// MUTATION: read quota progress before confirming an active subscription -> seeded quota appears and this goes RED.
	if out.Subscription != nil || len(out.Progress) != 0 {
		t.Fatalf("subscription/progress = %+v/%d, want nil/empty for no active subscription; body=%s",
			out.Subscription, len(out.Progress), rec.Body.String())
	}
}

func TestSubProgress_AuthRequired(t *testing.T) {
	router := newSubProgressRouter(UserDeps{Service: &fakeSubscriptionService{}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/subs/me/progress", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// MUTATION: mount progress outside session resolution or bypass resolveSession -> status becomes 200 and this goes RED.
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for missing session; body=%s", rec.Code, rec.Body.String())
	}
}
