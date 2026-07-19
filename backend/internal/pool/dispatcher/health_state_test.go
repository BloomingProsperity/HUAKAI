package dispatcher

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

func TestDefaultSelectorSkipsThrottledAccountSnapshot(t *testing.T) {
	// 锁定的回归：health_state 过滤必须发生在排名之前。
	// 变异自检：删除 health gate 会让账号 101 凭优先级胜出，此测试随之变红。
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
	// 锁定的回归：一个已 revoked 但截止时间已过期的账号必须重新变得合格。
	// 变异自检：把 revoked 当作永久会让账号 202 胜出，此测试随之变红。
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
	// 锁定的回归：model_rate_limits 必须是排名之前的 account+model gate。
	// 变异自检：把 model gate 换成 AllowAllGate 会让账号 101 凭优先级胜出，
	// 此测试随之变红。
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

func TestDBAccountSourceSkipsThrottledAccount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openDispatcherHealthPool(t, ctx)
	seed := seedDispatcherHealthGraph(t, ctx, pool, "throttled")
	now := time.Now().UTC()
	throttledID := insertDispatcherHealthAccount(t, ctx, pool, seed, "throttled", "throttled", now.Add(3*time.Minute), 1)
	healthyID := insertDispatcherHealthAccount(t, ctx, pool, seed, "healthy", "healthy", time.Time{}, 9)

	src := NewDBAccountSource(dbbilling.New(pool))
	sel := router.NewDefaultSelector(src, router.WithSlotManager(healthStateSlotManager{}))
	res, err := sel.Select(ctx, SelectionRequest{TenantID: seed.tenantID, PoolGroupID: seed.poolGroupID})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID == throttledID {
		t.Fatalf("selected throttled account %d; want it skipped", throttledID)
	}
	if res.AccountID != healthyID {
		t.Fatalf("selected account=%d, want healthy fallback %d", res.AccountID, healthyID)
	}
}

func TestDBAccountSourceReactivatesExpiredHealthState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openDispatcherHealthPool(t, ctx)
	seed := seedDispatcherHealthGraph(t, ctx, pool, "expired")
	now := time.Now().UTC()
	expiredID := insertDispatcherHealthAccount(t, ctx, pool, seed, "expired-revoked", "revoked", now.Add(-time.Minute), 1)
	healthyID := insertDispatcherHealthAccount(t, ctx, pool, seed, "healthy", "healthy", time.Time{}, 9)

	src := NewDBAccountSource(dbbilling.New(pool))
	sel := router.NewDefaultSelector(src, router.WithSlotManager(healthStateSlotManager{}))
	res, err := sel.Select(ctx, SelectionRequest{TenantID: seed.tenantID, PoolGroupID: seed.poolGroupID})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID != expiredID {
		t.Fatalf("selected account=%d, want expired revoked account %d to recover ahead of healthy fallback %d", res.AccountID, expiredID, healthyID)
	}

	var state string
	var until *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT health_state, health_state_until FROM provider_accounts WHERE id = $1`,
		expiredID,
	).Scan(&state, &until); err != nil {
		t.Fatalf("read recovered health state: %v", err)
	}
	if state != "healthy" || until != nil {
		t.Fatalf("expired account health=(%q,%v), want healthy with NULL until", state, until)
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

type dispatcherHealthSeed struct {
	tenantID    int64
	providerID  int64
	poolGroupID int64
	channelID   int64
}

func openDispatcherHealthPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping dispatcher PG health_state test")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func seedDispatcherHealthGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) dispatcherHealthSeed {
	t.Helper()
	unique := label + "-" + uuid.NewString()
	seed := dispatcherHealthSeed{}
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"disp-health-tenant-"+unique,
	).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM pool_slot_acquisitions WHERE tenant_id = $1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM account_credentials WHERE tenant_id = $1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id = $1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM channels WHERE tenant_id = $1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id = $1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM providers WHERE tenant_id = $1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, seed.tenantID)
	})
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		seed.tenantID, "disp-health-provider-"+unique, "Dispatcher Health "+unique,
	).Scan(&seed.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "disp-health-pg-"+unique,
	).Scan(&seed.poolGroupID); err != nil {
		t.Fatalf("seed pool_group: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		seed.tenantID, seed.poolGroupID, "disp-health-ch-"+unique,
	).Scan(&seed.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return seed
}

func insertDispatcherHealthAccount(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	seed dispatcherHealthSeed,
	label string,
	state string,
	until time.Time,
	priority int,
) int64 {
	t.Helper()
	var untilArg any
	if !until.IsZero() {
		untilArg = until
	}
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type,
			health_state, health_state_until, cap_concurrency, priority
		) VALUES ($1, $2, $3, $4, 'api_key', $5, $6, 4, $7) RETURNING id`,
		seed.tenantID, seed.providerID, seed.channelID, "disp-health-"+label+"-"+uuid.NewString(), state, untilArg, priority,
	).Scan(&id); err != nil {
		t.Fatalf("seed provider_account state=%s: %v", state, err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO account_credentials (
    tenant_id, provider_account_id, vendor, auth_mode, state,
    credential_version, encrypted_payload, key_id, nonce, aad_hash
) VALUES ($1, $2, 'openai', 'api_key', 'active', 1, $3, $4, $5, $6)`,
		seed.tenantID,
		id,
		[]byte("ciphertext"),
		"dispatcher-health-test-key",
		[]byte("nonce-12345678"),
		"dispatcher-health-aad-"+uuid.NewString(),
	); err != nil {
		t.Fatalf("seed provider_account credential state=%s: %v", state, err)
	}
	return id
}
