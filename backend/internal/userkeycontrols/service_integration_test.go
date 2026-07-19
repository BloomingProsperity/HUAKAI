//go:build integration_pg

package userkeycontrols

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

func openUserKeyControlsPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
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

type userKeyControlsFixture struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	suffix   string
	tenantID int64
	userID   int64
	apiKeyID int64
}

func newUserKeyControlsFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *userKeyControlsFixture {
	t.Helper()
	f := &userKeyControlsFixture{t: t, ctx: ctx, pool: pool, suffix: uuid.NewString()}
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"userkeycontrols-"+f.suffix,
	).Scan(&f.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		f.tenantID, "user-"+f.suffix,
	).Scan(&f.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		f.tenantID, f.userID, "key-"+f.suffix,
		"$2a$10$placeholder-not-resolved-by-userkeycontrols-tests",
		"hk_test_"+f.suffix[:8],
	).Scan(&f.apiKeyID); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}
	t.Cleanup(f.cleanup)
	return f
}

func (f *userKeyControlsFixture) cleanup() {
	ctx := context.Background()
	_, _ = f.pool.Exec(ctx, `DELETE FROM api_keys WHERE tenant_id=$1`, f.tenantID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM quota_policies WHERE tenant_id=$1`, f.tenantID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, f.tenantID)
	_, _ = f.pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, f.tenantID)
}

func TestSetGetModelAllowlist(t *testing.T) {
	// 变异:若忽略 setter 输入或读错列,精确的存储 CSV 断言会变红。
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openUserKeyControlsPool(t, ctx)
	f := newUserKeyControlsFixture(t, ctx, pool)
	svc := NewKeyControlService(pool, nil)

	if _, err := svc.SetKeyModelAllowlist(ctx, SetKeyModelAllowlistRequest{
		TenantID:      f.tenantID,
		UserID:        f.userID,
		APIKeyID:      f.apiKeyID,
		AllowedModels: []string{" GPT-4O ", "claude-3"},
	}); err != nil {
		t.Fatalf("SetKeyModelAllowlist: %v", err)
	}
	view, err := svc.GetKeyModelAllowlist(ctx, f.tenantID, f.userID, f.apiKeyID)
	if err != nil {
		t.Fatalf("GetKeyModelAllowlist: %v", err)
	}
	if got, want := fmt.Sprint(view.AllowedModels), "[gpt-4o claude-3]"; got != want {
		t.Fatalf("AllowedModels=%s want %s", got, want)
	}
}

func TestPerKeyRequestMetric(t *testing.T) {
	// 变异:在 SetKeyQuota 里硬编码 cost_usd,ResolvePolicies 就不会
	// 返回这条按 key 的 MetricRequests 策略。
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openUserKeyControlsPool(t, ctx)
	f := newUserKeyControlsFixture(t, ctx, pool)
	svc := NewKeyControlService(pool, nil)

	if _, err := svc.SetKeyQuota(ctx, SetKeyQuotaRequest{
		TenantID:      f.tenantID,
		UserID:        f.userID,
		APIKeyID:      f.apiKeyID,
		LimitUSD:      decimal.RequireFromString("10.00000000"),
		WindowKind:    quota.WindowFixed,
		WindowSeconds: 60,
		Mode:          quota.ModeEnforce,
	}); err != nil {
		t.Fatalf("SetKeyQuota cost default: %v", err)
	}
	if _, err := svc.SetKeyQuota(ctx, SetKeyQuotaRequest{
		TenantID:      f.tenantID,
		UserID:        f.userID,
		APIKeyID:      f.apiKeyID,
		LimitUSD:      decimal.RequireFromString("2.00000000"),
		Metric:        quota.MetricRequests,
		WindowKind:    quota.WindowFixed,
		WindowSeconds: 60,
		Mode:          quota.ModeEnforce,
	}); err != nil {
		t.Fatalf("SetKeyQuota request-count: %v", err)
	}

	var wildcardPolicies int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM quota_policies
		WHERE tenant_id = $1
		  AND scope_kind = 'api_key'
		  AND scope_id = $2
		  AND model_selector = '*'
		  AND enabled = true
		  AND valid_until IS NULL
	`, f.tenantID, fmt.Sprint(f.apiKeyID)).Scan(&wildcardPolicies); err != nil {
		t.Fatalf("查询全模型配额策略: %v", err)
	}
	if wildcardPolicies != 2 {
		t.Fatalf("全模型配额策略数=%d want 2", wildcardPolicies)
	}

	resolved, err := quota.ResolvePolicies(
		ctx,
		quota.NewPostgresStore(pool),
		f.tenantID,
		[]quota.Scope{{TenantID: f.tenantID, Kind: quota.ScopeAPIKey, ID: fmt.Sprint(f.apiKeyID)}},
		"",
		[]quota.Metric{quota.MetricCostUSD, quota.MetricRequests},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("ResolvePolicies: %v", err)
	}
	var sawCost, sawRequests bool
	for _, policy := range resolved.Ordered {
		if policy.Scope.Kind != quota.ScopeAPIKey || policy.Scope.ID != fmt.Sprint(f.apiKeyID) {
			continue
		}
		switch policy.Metric {
		case quota.MetricCostUSD:
			sawCost = true
		case quota.MetricRequests:
			sawRequests = true
			if !policy.LimitValue.Equal(decimal.RequireFromString("2.00000000")) {
				t.Fatalf("requests limit=%s want 2.00000000", policy.LimitValue)
			}
			if policy.Window.Kind != quota.WindowFixed || policy.Window.Seconds != 60 || policy.Mode != quota.ModeEnforce {
				t.Fatalf("requests policy window/mode=%+v/%s want fixed 60 enforce", policy.Window, policy.Mode)
			}
		}
	}
	if !sawRequests {
		t.Fatalf("resolved policies=%+v missing api_key MetricRequests", resolved.Ordered)
	}
	if !sawCost {
		t.Fatalf("resolved policies=%+v missing existing api_key MetricCostUSD", resolved.Ordered)
	}
}
