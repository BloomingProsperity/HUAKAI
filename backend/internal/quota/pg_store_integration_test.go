//go:build integration_pg

// 配额 store 真 PG 集成测试。每个测试都用判别 fixture 守一个具体缺陷:
//   T1 BUG-QUOTA-001 并发槽超发竞态
//   T2 scope kind/id 元组隔离, 防两数组笛卡尔积误匹配
//   T3 过期 lease 必须拒绝且不占槽
//   T4 同 reservation 重入只占一个槽并刷新 lease
//   T5 tenant 谓词缺失不得泄漏策略或 reservation
//   T6 running reconciliation job stale 后可重新领取
//   T7 InsertReservation 返回 DB 规范化后的 scope/money 值

package quota

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openQuotaIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 32})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type quotaFixture struct {
	t              *testing.T
	ctx            context.Context
	pool           *pgxpool.Pool
	suffix         string
	tenantID       int64
	otherTenantID  int64
	userID         int64
	otherUserID    int64
	apiKeyID       int64
	otherAPIKeyID  int64
	claimedIDs     []int64
	reservationIDs []int64
}

func newQuotaFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *quotaFixture {
	t.Helper()
	f := &quotaFixture{
		t:      t,
		ctx:    ctx,
		pool:   pool,
		suffix: uuid.NewString(),
	}
	f.tenantID, f.userID, f.apiKeyID = f.seedTenantUserKey("quota-a")
	f.otherTenantID, f.otherUserID, f.otherAPIKeyID = f.seedTenantUserKey("quota-b")
	t.Cleanup(f.cleanup)
	return f
}

func (f *quotaFixture) seedTenantUserKey(label string) (int64, int64, int64) {
	f.t.Helper()
	unique := label + "-" + f.suffix
	var tenantID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		unique,
	).Scan(&tenantID); err != nil {
		f.t.Fatalf("seed tenant %s: %v", label, err)
	}
	var userID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "user-"+unique,
	).Scan(&userID); err != nil {
		f.t.Fatalf("seed user %s: %v", label, err)
	}
	var apiKeyID int64
	prefix := "hk_test_" + unique[:min(20, len(unique))]
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		tenantID, userID, "key-"+unique,
		"$2a$10$placeholder-not-resolved-by-quota-tests",
		prefix,
	).Scan(&apiKeyID); err != nil {
		f.t.Fatalf("seed api_key %s: %v", label, err)
	}
	return tenantID, userID, apiKeyID
}

func (f *quotaFixture) cleanup() {
	ctx := context.Background()
	tenantIDs := []int64{f.tenantID, f.otherTenantID}
	for _, tenantID := range tenantIDs {
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_reconciliation_jobs WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_concurrency_slots WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_concurrency_scope_locks WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_reservations WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_windows WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_policies WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM billing_events WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM usage_records WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM billing_ledger_archive WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM api_keys WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}
}

func (f *quotaFixture) seedClaim(label string) int64 {
	f.t.Helper()
	return f.seedClaimForTenant(f.tenantID, f.userID, f.apiKeyID, label)
}

func (f *quotaFixture) seedClaimForTenant(tenantID, userID, apiKeyID int64, label string) int64 {
	f.t.Helper()
	unique := label + "-" + uuid.NewString()
	var claimID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint,
			api_key_id, user_id, logical_request_id, endpoint_family,
			requested_model, billing_policy_version, request_class,
			predicted_cost, currency_code, lease_expires_at
		) VALUES (
			$1, $2, $3,
			$4, $5, $6, 'chat',
			'gpt-4.1-mini', 'quota-test', 'standard',
			0.01, 'USD', $7
		) RETURNING id`,
		tenantID, "idem-"+unique, "fp-"+unique,
		apiKeyID, userID, "logical-"+unique, time.Now().UTC().Add(10*time.Minute),
	).Scan(&claimID); err != nil {
		f.t.Fatalf("seed claim %s: %v", label, err)
	}
	f.claimedIDs = append(f.claimedIDs, claimID)
	return claimID
}

func (f *quotaFixture) seedReservation(claimID int64, label string) int64 {
	f.t.Helper()
	return f.seedReservationForTenant(f.tenantID, claimID, label)
}

func (f *quotaFixture) seedReservationForTenant(tenantID, claimID int64, label string) int64 {
	f.t.Helper()
	var reservationID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO quota_reservations (
			tenant_id, claim_id, request_fingerprint, scope_snapshot,
			policy_snapshot, predicted_cost, reserved_units, lease_expires_at
		) VALUES (
			$1, $2, $3, '[{"kind":"user","id":"u1"}]'::jsonb,
			'[]'::jsonb, 0.01, 1, $4
		) RETURNING id`,
		tenantID, claimID, "quota-fp-"+label, time.Now().UTC().Add(10*time.Minute),
	).Scan(&reservationID); err != nil {
		f.t.Fatalf("seed reservation %s: %v", label, err)
	}
	f.reservationIDs = append(f.reservationIDs, reservationID)
	return reservationID
}

func (f *quotaFixture) seedPolicy(tenantID int64, kind ScopeKind, id string, metric Metric, limit string) int64 {
	f.t.Helper()
	var policyID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO quota_policies (
			tenant_id, scope_kind, scope_id, metric, window_kind,
			window_seconds, limit_value, burst_value, mode, priority,
			enabled, valid_from
		) VALUES (
			$1, $2, $3, $4, 'none',
			0, $5::numeric(20,8), 0, 'enforce', 10,
			true, NOW() - interval '1 minute'
		) RETURNING id`,
		tenantID, string(kind), id, string(metric), limit,
	).Scan(&policyID); err != nil {
		f.t.Fatalf("seed policy %s/%s: %v", kind, id, err)
	}
	return policyID
}

// T7: InsertReservation 必须返回 DB 持久化后的规范值, 不能回显入参。
//
// Mutation 自检: 若 store 继续返回 input.Scopes/input.PredictedCost,
// global 空 scope 会返回 "" 而不是 "*", 9 位小数也不会等于 DB numeric(20,8)。
func TestPostgresStore_InsertReservation_ReturnsDBCanonicalValues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)

	claimID := f.seedClaim("insert-canonical")
	predictedCost, err := decimal.NewFromString("0.123456789")
	if err != nil {
		t.Fatalf("parse predicted cost: %v", err)
	}
	reservedUnits, err := decimal.NewFromString("1.000000019")
	if err != nil {
		t.Fatalf("parse reserved units: %v", err)
	}
	reservation, err := store.InsertReservation(ctx, ReservationInsert{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "quota-canonical-" + uuid.NewString(),
		Scopes:             []Scope{{TenantID: f.tenantID, Kind: ScopeGlobal, ID: ""}},
		PredictedCost:      predictedCost,
		ReservedUnits:      reservedUnits,
		LeaseExpiresAt:     time.Now().UTC().Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("InsertReservation: %v", err)
	}
	if len(reservation.Scopes) != 1 || reservation.Scopes[0].Kind != ScopeGlobal || reservation.Scopes[0].ID != "*" {
		t.Fatalf("InsertReservation returned non-canonical scopes: %+v; want global/*", reservation.Scopes)
	}

	var scopeSnapshot string
	var dbPredictedCost string
	var dbReservedUnits string
	if err := pool.QueryRow(ctx,
		`SELECT scope_snapshot::text, predicted_cost::text, reserved_units::text
		 FROM quota_reservations
		 WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, reservation.ID,
	).Scan(&scopeSnapshot, &dbPredictedCost, &dbReservedUnits); err != nil {
		t.Fatalf("read inserted reservation: %v", err)
	}
	dbScopes, err := parseScopes(f.tenantID, []byte(scopeSnapshot))
	if err != nil {
		t.Fatalf("parse DB scope snapshot: %v", err)
	}
	if len(dbScopes) != 1 || dbScopes[0].Kind != ScopeGlobal || dbScopes[0].ID != "*" {
		t.Fatalf("DB scopes = %+v; want global/*", dbScopes)
	}
	wantPredictedCost, err := decimal.NewFromString(dbPredictedCost)
	if err != nil {
		t.Fatalf("parse DB predicted cost %q: %v", dbPredictedCost, err)
	}
	wantReservedUnits, err := decimal.NewFromString(dbReservedUnits)
	if err != nil {
		t.Fatalf("parse DB reserved units %q: %v", dbReservedUnits, err)
	}
	if !reservation.PredictedCost.Equal(wantPredictedCost) {
		t.Fatalf("PredictedCost=%s; want DB canonical %s", reservation.PredictedCost, wantPredictedCost)
	}
	if !reservation.ReservedUnits.Equal(wantReservedUnits) {
		t.Fatalf("ReservedUnits=%s; want DB canonical %s", reservation.ReservedUnits, wantReservedUnits)
	}
}

// T1 BUG-QUOTA-001: 同一 tenant/scope 下 M>N 个不同 reservation 并发抢槽,
// 成功数必须精确等于 N。
//
// Mutation 自检: 去掉 quota_acquire_concurrency_slot 里的 scope 锁 FOR UPDATE
// 或把 store 改成非锁路径, 多个事务会同时看见 active_count<N 并插入, 成功数会 >N。
func TestPostgresStore_AcquireConcurrencySlot_ExactLimitUnderConcurrentRace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)

	const limit = int64(3)
	const workers = 24
	now := time.Now().UTC()
	reservations := make([]struct {
		claimID       int64
		reservationID int64
	}, workers)
	for i := 0; i < workers; i++ {
		claimID := f.seedClaim(fmt.Sprintf("race-%02d", i))
		reservationID := f.seedReservation(claimID, fmt.Sprintf("race-%02d", i))
		reservations[i] = struct {
			claimID       int64
			reservationID int64
		}{claimID: claimID, reservationID: reservationID}
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	errs := make([]error, 0)
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			slot, err := store.AcquireConcurrencySlot(ctx, ConcurrencyAcquire{
				TenantID:       f.tenantID,
				ReservationID:  reservations[i].reservationID,
				ClaimID:        reservations[i].claimID,
				Scope:          Scope{TenantID: f.tenantID, Kind: ScopeUser, ID: "u1"},
				SlotLimit:      limit,
				At:             now,
				LeaseExpiresAt: now.Add(2 * time.Minute),
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			if slot.ID != 0 {
				successes++
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("AcquireConcurrencySlot returned unexpected errors: %v", errs)
	}
	if successes != int(limit) {
		t.Fatalf("concurrent acquire successes=%d; want exactly slot_limit=%d", successes, limit)
	}
	if got := f.activeSlotCount(ScopeUser, "u1"); got != limit {
		t.Fatalf("active DB slot count=%d; want %d", got, limit)
	}
}

// T2: scope 必须按 (kind,id) 元组匹配, 不能把 kind 数组和 id 数组做笛卡尔积。
//
// Mutation 自检: 把 ListActivePolicies 查询退回 scope_kind IN kinds AND
// scope_id IN ids 后, decoy (user,k1)/(api_key,u1) 会被误返回, 本测试 red。
func TestPostgresStore_ListActivePolicies_MatchesScopeTuplesOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)

	wantID := f.seedPolicy(f.tenantID, ScopeUser, "u1", MetricConcurrency, "1")
	f.seedPolicy(f.tenantID, ScopeUser, "k1", MetricConcurrency, "99")
	f.seedPolicy(f.tenantID, ScopeAPIKey, "u1", MetricConcurrency, "99")

	policies, err := store.ListActivePolicies(ctx, PolicyFilter{
		TenantID: f.tenantID,
		Scopes: []Scope{
			{TenantID: f.tenantID, Kind: ScopeUser, ID: "u1"},
			{TenantID: f.tenantID, Kind: ScopeAPIKey, ID: "k1"},
		},
		Metrics: []Metric{MetricConcurrency},
		At:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ListActivePolicies: %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("policies len=%d; want exactly 1 tuple match; got %+v", len(policies), policies)
	}
	if policies[0].ID != wantID || policies[0].Scope.Kind != ScopeUser || policies[0].Scope.ID != "u1" {
		t.Fatalf("tuple match drift: got %+v want policy id=%d user/u1", policies[0], wantID)
	}
}

// T3: lease_expires_at <= at_time 必须拒绝, 且不写入 active slot。
//
// Mutation 自检: 去掉 DB 函数里的 lease guard 后, 过期 lease 会返回槽并占用计数。
func TestPostgresStore_AcquireConcurrencySlot_RejectsExpiredLeaseWithoutCounting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)

	claimID := f.seedClaim("expired-lease")
	reservationID := f.seedReservation(claimID, "expired-lease")
	now := time.Now().UTC()
	slot, err := store.AcquireConcurrencySlot(ctx, ConcurrencyAcquire{
		TenantID:       f.tenantID,
		ReservationID:  reservationID,
		ClaimID:        claimID,
		Scope:          Scope{TenantID: f.tenantID, Kind: ScopeUser, ID: "u1"},
		SlotLimit:      1,
		At:             now,
		LeaseExpiresAt: now,
	})
	if err != nil {
		t.Fatalf("AcquireConcurrencySlot expired lease: %v", err)
	}
	if slot.ID != 0 {
		t.Fatalf("expired lease acquired slot %+v; want empty slot", slot)
	}
	if got := f.activeSlotCount(ScopeUser, "u1"); got != 0 {
		t.Fatalf("expired lease active slot count=%d; want 0", got)
	}
}

// T4: 同一 reservation/scope 重入必须只占 1 个槽, 并刷新 lease。
//
// Mutation 自检: 去掉重入 UPDATE 分支或改成每次都插新槽, active count 会变 2
// 或 lease 不刷新, 本测试 red。
func TestPostgresStore_AcquireConcurrencySlot_IdempotentReentryRefreshesLease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)

	claimID := f.seedClaim("reentry")
	reservationID := f.seedReservation(claimID, "reentry")
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstLease := now.Add(1 * time.Minute)
	secondLease := now.Add(5 * time.Minute)
	input := ConcurrencyAcquire{
		TenantID:       f.tenantID,
		ReservationID:  reservationID,
		ClaimID:        claimID,
		Scope:          Scope{TenantID: f.tenantID, Kind: ScopeUser, ID: "u1"},
		SlotLimit:      1,
		At:             now,
		LeaseExpiresAt: firstLease,
	}
	first, err := store.AcquireConcurrencySlot(ctx, input)
	if err != nil {
		t.Fatalf("first AcquireConcurrencySlot: %v", err)
	}
	if first.ID == 0 {
		t.Fatalf("first acquire returned empty slot")
	}
	input.LeaseExpiresAt = secondLease
	second, err := store.AcquireConcurrencySlot(ctx, input)
	if err != nil {
		t.Fatalf("second AcquireConcurrencySlot: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("reentry returned different slot id: first=%d second=%d", first.ID, second.ID)
	}
	if got := f.activeSlotCount(ScopeUser, "u1"); got != 1 {
		t.Fatalf("reentry active slot count=%d; want 1", got)
	}
	var dbLease time.Time
	if err := pool.QueryRow(ctx,
		`SELECT lease_expires_at FROM quota_concurrency_slots
		 WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, first.ID,
	).Scan(&dbLease); err != nil {
		t.Fatalf("read slot lease: %v", err)
	}
	if dbLease.Before(secondLease.Add(-time.Second)) {
		t.Fatalf("lease was not refreshed: got %s want around %s", dbLease, secondLease)
	}
}

// T5: store 方法必须保留 tenant 谓词; tenant B 不能看见 tenant A 的策略或 reservation。
//
// Mutation 自检: 任一查询漏 tenant_id WHERE, tenant B 会读到 A 的 policy/reservation。
func TestPostgresStore_TenantIsolation_HidesForeignRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)

	f.seedPolicy(f.tenantID, ScopeUser, "u1", MetricConcurrency, "1")
	claimID := f.seedClaim("tenant-isolation")
	f.seedReservation(claimID, "tenant-isolation")

	policies, err := store.ListActivePolicies(ctx, PolicyFilter{
		TenantID: f.otherTenantID,
		Scopes:   []Scope{{TenantID: f.otherTenantID, Kind: ScopeUser, ID: "u1"}},
		Metrics:  []Metric{MetricConcurrency},
		At:       time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ListActivePolicies tenant B: %v", err)
	}
	if len(policies) != 0 {
		t.Fatalf("tenant B saw tenant A policies: %+v", policies)
	}

	reservation, err := store.GetReservationByClaimForUpdate(ctx, f.otherTenantID, claimID)
	if err == nil && reservation.ID != 0 {
		t.Fatalf("tenant B saw tenant A reservation: %+v", reservation)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("tenant B GetReservation unexpected error: %v", err)
	}
}

// T6: running job 超过 15 分钟 stale 后, GetDue + MarkRunning 必须能重新领取。
//
// Mutation 自检: 去掉 ListDue 或 MarkRunning 的 stale-running 分支后, job 领不到
// 或 MarkRunning rows=0, 本测试 red。
func TestPostgresStore_Reconciliation_ReclaimsStaleRunningJob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)

	claimID := f.seedClaim("reconcile-stale")
	reservationID := f.seedReservation(claimID, "reconcile-stale")
	now := time.Now().UTC()
	job, err := store.EnqueueReconciliationJob(ctx, ReconciliationEnqueue{
		TenantID:      f.tenantID,
		ClaimID:       claimID,
		ReservationID: &reservationID,
		Kind:          "settle_after_billing_success",
		NextRunAt:     now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("EnqueueReconciliationJob: %v", err)
	}
	if err := store.MarkReconciliationJobRunning(ctx, f.tenantID, job.ID); err != nil {
		t.Fatalf("first MarkReconciliationJobRunning: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE quota_reconciliation_jobs
		 SET locked_at=$1, updated_at=NOW()
		 WHERE tenant_id=$2 AND id=$3 AND status='running'`,
		now.Add(-16*time.Minute), f.tenantID, job.ID,
	); err != nil {
		t.Fatalf("simulate stale running job: %v", err)
	}

	due, err := store.ListDueReconciliationJobs(ctx, f.tenantID, now, 5)
	if err != nil {
		t.Fatalf("ListDueReconciliationJobs: %v", err)
	}
	if len(due) != 1 || due[0].ID != job.ID {
		t.Fatalf("stale running job not returned; got %+v want job id %d", due, job.ID)
	}
	if err := store.MarkReconciliationJobRunning(ctx, f.tenantID, job.ID); err != nil {
		t.Fatalf("second MarkReconciliationJobRunning stale reclaim: %v", err)
	}
	var status string
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT status, attempt_count FROM quota_reconciliation_jobs
		 WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, job.ID,
	).Scan(&status, &attempts); err != nil {
		t.Fatalf("read reclaimed job: %v", err)
	}
	if status != "running" || attempts != 2 {
		t.Fatalf("reclaimed job status=%s attempts=%d; want running/2", status, attempts)
	}
}

func (f *quotaFixture) activeSlotCount(kind ScopeKind, id string) int64 {
	f.t.Helper()
	var count int64
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*)
		 FROM quota_concurrency_slots
		 WHERE tenant_id=$1 AND scope_kind=$2 AND scope_id=$3 AND status='acquired'`,
		f.tenantID, string(kind), id,
	).Scan(&count); err != nil {
		f.t.Fatalf("count active slots: %v", err)
	}
	return count
}
