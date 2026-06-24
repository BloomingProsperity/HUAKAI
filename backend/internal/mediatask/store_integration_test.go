//go:build integration_pg

package mediatask

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestMediaTaskSubmitReservesOnce(t *testing.T) {
	// MUTATION: remove billing.Reserve from CreateTask; held remains 0 instead of 1.23.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "submit-reserve")
	svc := newIntegrationService(pool)

	task, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput("req-reserve"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if task.EstimatedCents != 123 || task.HoldRef == "" {
		t.Fatalf("task estimate/hold_ref=%d/%q", task.EstimatedCents, task.HoldRef)
	}
	balance, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !balance.Equal(decimal.RequireFromString("10.00")) || !held.Equal(decimal.RequireFromString("1.23")) {
		t.Fatalf("balance/held=%s/%s want 10.00/1.23", balance, held)
	}
}

func TestMediaTaskIdempotentSubmit(t *testing.T) {
	// MUTATION: skip the (tenant_id, request_id) lookup before reserve; second submit doubles held to 2.46.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "idempotent")
	svc := newIntegrationService(pool)

	first, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput("req-idem"))
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	second, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput("req-idem"))
	if err != nil {
		t.Fatalf("second Submit: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotent IDs %d/%d want same", first.ID, second.ID)
	}
	_, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !held.Equal(decimal.RequireFromString("1.23")) {
		t.Fatalf("held=%s want 1.23 after idempotent replay", held)
	}
	var holds int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM balance_holds WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&holds); err != nil {
		t.Fatalf("count holds: %v", err)
	}
	if holds != 1 {
		t.Fatalf("balance_holds=%d want 1", holds)
	}
}

func TestMediaTaskSuccessSettlesActual(t *testing.T) {
	// MUTATION: capture estimated cost instead of actual_cents; balance becomes 8.77 instead of 9.23.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "success")
	svc := newIntegrationService(pool)
	store := svc.store.(*PostgresStore)

	task := submitAndMarkSubmitted(t, ctx, svc, store, seed, "req-success")
	leased := leaseTaskForTest(t, ctx, pool, task.ID, "worker-success")
	ok, err := store.CompleteSuccess(ctx, leased, "worker-success", PollResult{Status: StatusSucceeded, Progress: 100, ActualCents: 77, Result: []byte(`{"ok":true}`)}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CompleteSuccess: %v", err)
	}
	if !ok {
		t.Fatal("CompleteSuccess ok=false")
	}

	balance, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !balance.Equal(decimal.RequireFromString("9.23")) || !held.Equal(decimal.Zero) {
		t.Fatalf("balance/held=%s/%s want 9.23/0", balance, held)
	}
	assertTaskStatus(t, ctx, pool, task.ID, StatusSucceeded)
	assertClaimStatusCost(t, ctx, pool, task.HoldRef, "committed", decimal.RequireFromString("0.77"))
}

// TestMediaTaskSuccessOverEstimateClampsToEstimate 锁住"成功任务的实际成本超过预估"
// 这条亏钱缺陷的修复:上游真把任务跑成功、但 ActualCents(200) 高于预扣的
// EstimatedCents(123) 时,必须把成功任务推进到终态 succeeded,并按【预扣的预估】
// 结算(收费 clamp 到预估上限,平台吸收有界超出部分),而不是回滚整事务、把成功
// 任务卡死在 in_progress、最终被 TaskTimeout→ExpireTask 全额释放预扣致平台白吃上游成本。
//
// 变异证:把 store_money.go 的处理改回旧逻辑
//
//	if result.ActualCents > locked.EstimatedCents { return ErrActualExceedsEstimate }
//
// (即回滚而非 clamp 推进终态),本测试必然变红 —— CompleteSuccess 会返回
// ErrActualExceedsEstimate,ok=false,任务停在 in_progress、预扣 1.23 不释放,
// 下面对 balance=8.77 / held=0 / status=succeeded / claim=committed@1.23 的断言全部失败。
func TestMediaTaskSuccessOverEstimateClampsToEstimate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "over-estimate")
	svc := newIntegrationService(pool)
	store := svc.store.(*PostgresStore)

	task := submitAndMarkSubmitted(t, ctx, svc, store, seed, "req-over-estimate")
	leased := leaseTaskForTest(t, ctx, pool, task.ID, "worker-over")
	// ActualCents=200 严格大于预扣的 EstimatedCents=123,触发"超估价"分支。
	ok, err := store.CompleteSuccess(ctx, leased, "worker-over", PollResult{Status: StatusSucceeded, Progress: 100, ActualCents: 200, Result: []byte(`{"ok":true}`)}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CompleteSuccess(超估价): %v", err)
	}
	if !ok {
		t.Fatal("CompleteSuccess(超估价) ok=false,任务未走到终态")
	}

	// 结算 clamp 到预估:扣 1.23(而非 2.00,不超收客户),held 清零。
	balance, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !balance.Equal(decimal.RequireFromString("8.77")) || !held.Equal(decimal.Zero) {
		t.Fatalf("balance/held=%s/%s want 8.77/0(按预估 1.23 结算且不残留预扣)", balance, held)
	}
	// 终态必须是成功(而非卡死或被超时释放成 expired)。
	assertTaskStatus(t, ctx, pool, task.ID, StatusSucceeded)
	// claim 入账成本 clamp 到预估 0.77... 即 1.23;actual_cents 仍按真实 200 落媒体任务行做对账留痕。
	assertClaimStatusCost(t, ctx, pool, task.HoldRef, "committed", decimal.RequireFromString("1.23"))
	// 媒体任务行的 actual_cents 保留真实上游成本(200),供运维核对平台吸收了多少。
	var actualCents int64
	if err := pool.QueryRow(ctx, `SELECT actual_cents FROM media_tasks WHERE id=$1`, task.ID).Scan(&actualCents); err != nil {
		t.Fatalf("read media_tasks.actual_cents: %v", err)
	}
	if actualCents != 200 {
		t.Fatalf("media_tasks.actual_cents=%d want 200(保留真实上游成本做对账)", actualCents)
	}
}

func TestMediaTaskFailureRefundsFull(t *testing.T) {
	// MUTATION: use Capture(0) instead of Release on failure; claim may commit and failure audit is wrong.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "failure")
	svc := newIntegrationService(pool)
	store := svc.store.(*PostgresStore)

	task := submitAndMarkSubmitted(t, ctx, svc, store, seed, "req-failure")
	leased := leaseTaskForTest(t, ctx, pool, task.ID, "worker-failure")
	ok, err := store.CompleteFailure(ctx, leased, "worker-failure", "provider_failed", time.Now().UTC())
	if err != nil {
		t.Fatalf("CompleteFailure: %v", err)
	}
	if !ok {
		t.Fatal("CompleteFailure ok=false")
	}
	balance, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !balance.Equal(decimal.RequireFromString("10.00")) || !held.Equal(decimal.Zero) {
		t.Fatalf("balance/held=%s/%s want 10.00/0", balance, held)
	}
	assertTaskStatus(t, ctx, pool, task.ID, StatusFailed)
	assertClaimStatusCost(t, ctx, pool, task.HoldRef, "aborted", decimal.Zero)
}

func TestMediaTaskTimeoutExpiresAndRefunds(t *testing.T) {
	// MUTATION: mark timeout expired without billing.Release; held remains 1.23.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "timeout")
	svc := newIntegrationService(pool)
	store := svc.store.(*PostgresStore)

	task := submitAndMarkSubmitted(t, ctx, svc, store, seed, "req-timeout")
	leased := leaseTaskForTest(t, ctx, pool, task.ID, "worker-timeout")
	ok, err := store.ExpireTask(ctx, leased, "worker-timeout", time.Now().UTC())
	if err != nil {
		t.Fatalf("ExpireTask: %v", err)
	}
	if !ok {
		t.Fatal("ExpireTask ok=false")
	}
	balance, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !balance.Equal(decimal.RequireFromString("10.00")) || !held.Equal(decimal.Zero) {
		t.Fatalf("balance/held=%s/%s want 10.00/0", balance, held)
	}
	assertTaskStatus(t, ctx, pool, task.ID, StatusExpired)
	assertClaimStatusCost(t, ctx, pool, task.HoldRef, "aborted", decimal.Zero)
}

func TestMediaTaskWorkerFencing_NoDoubleSettle(t *testing.T) {
	// MUTATION: remove SKIP LOCKED / lease_owner from AcquireLease or terminal update;
	// two workers append two claim_committed events or debit twice.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "fencing")
	svc := newIntegrationService(pool)
	store := svc.store.(*PostgresStore)
	task := submitAndMarkSubmitted(t, ctx, svc, store, seed, "req-fencing")

	provider := &integrationProvider{poll: PollResult{Status: StatusSucceeded, Progress: 100, ActualCents: 77, Result: []byte(`{"ok":true}`)}}
	workerA := NewWorker(store, StaticConfigSource{Config: integrationConfig()}, StaticProviderRegistry{"http": provider}, WorkerOptions{Owner: "worker-a"})
	workerB := NewWorker(store, StaticConfigSource{Config: integrationConfig()}, StaticProviderRegistry{"http": provider}, WorkerOptions{Owner: "worker-b"})

	var wg sync.WaitGroup
	for _, w := range []*Worker{workerA, workerB} {
		wg.Add(1)
		go func(w *Worker) {
			defer wg.Done()
			_, _ = w.RunOnce(ctx)
		}(w)
	}
	wg.Wait()

	var committedEvents int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2 AND event_type='claim_committed'`,
		seed.tenantID, mustClaimID(t, task.HoldRef),
	).Scan(&committedEvents); err != nil {
		t.Fatalf("count committed events: %v", err)
	}
	if committedEvents != 1 {
		t.Fatalf("committedEvents=%d want 1", committedEvents)
	}
	balance, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !balance.Equal(decimal.RequireFromString("9.23")) || !held.Equal(decimal.Zero) {
		t.Fatalf("balance/held=%s/%s want 9.23/0", balance, held)
	}
}

func TestMediaTaskSubmitAtomic_ReserveAndRowTogether(t *testing.T) {
	// MUTATION: reserve outside the media task insert transaction; held remains 1.23 after injected insert failure.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "atomic")
	store := NewPostgresStore(pool, PostgresStoreConfig{BillingPolicyVersion: "test-policy", RequestClass: "standard"})
	store.beforeInsertTask = func() error { return errors.New("inject media task insert failure") }
	svc := NewService(store, StaticConfigSource{Config: integrationConfig()}, StaticProviderRegistry{"http": NewNoopProvider()})

	if _, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput("req-atomic")); err == nil {
		t.Fatal("Submit nil error, want injected failure")
	}
	balance, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !balance.Equal(decimal.RequireFromString("10.00")) || !held.Equal(decimal.Zero) {
		t.Fatalf("balance/held=%s/%s want 10.00/0 after rollback", balance, held)
	}
	var tasks, claims int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_tasks WHERE tenant_id=$1 AND request_id='req-atomic'`, seed.tenantID).Scan(&tasks); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_ledger_claims WHERE tenant_id=$1 AND logical_request_id='req-atomic'`, seed.tenantID).Scan(&claims); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if tasks != 0 || claims != 0 {
		t.Fatalf("rolled back tasks/claims=%d/%d want 0/0", tasks, claims)
	}
}

func TestMediaTaskTenantIsolation(t *testing.T) {
	// MUTATION: drop user_id from Get/List/idempotency guards; user B sees or replays user A's task.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seedA := seedMediaUser(t, ctx, pool, "tenant-a")
	seedAUserB := seedMediaUserInTenant(t, ctx, pool, seedA.tenantID, "tenant-a-user-b")
	seedB := seedMediaUser(t, ctx, pool, "tenant-b")
	svc := newIntegrationService(pool)

	task, err := svc.Submit(ctx, seedA.tenantID, seedA.userID, submitInput("req-isolation"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := svc.Status(ctx, seedB.tenantID, seedB.userID, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant Status err=%v want ErrNotFound", err)
	}
	if _, err := svc.Status(ctx, seedA.tenantID, seedAUserB.userID, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("same-tenant different-user Status err=%v want ErrNotFound", err)
	}
	list, err := svc.List(ctx, seedA.tenantID, seedAUserB.userID, 20)
	if err != nil {
		t.Fatalf("List same-tenant userB: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("same-tenant userB list=%+v want empty", list)
	}
	if _, err := svc.Submit(ctx, seedA.tenantID, seedAUserB.userID, submitInput("req-isolation")); !errors.Is(err, ErrRequestIDConflict) {
		t.Fatalf("same-tenant request_id replay err=%v want ErrRequestIDConflict", err)
	}
	_, held := readBalance(t, ctx, pool, seedA.tenantID, seedAUserB.userID)
	if !held.IsZero() {
		t.Fatalf("same-tenant conflicting replay held=%s want 0", held)
	}
}

func TestMigration0099(t *testing.T) {
	// MUTATION: omit unique(tenant_id, request_id) or runnable partial index; schema probes fail.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)

	var tableName string
	if err := pool.QueryRow(ctx,
		`SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_name='media_tasks'`,
	).Scan(&tableName); err != nil {
		t.Fatalf("media_tasks table missing; run migration 0099: %v", err)
	}
	var uniqueCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_indexes WHERE tablename='media_tasks' AND indexdef ILIKE '%UNIQUE%' AND indexdef ILIKE '%tenant_id%' AND indexdef ILIKE '%request_id%'`,
	).Scan(&uniqueCount); err != nil {
		t.Fatalf("unique index probe: %v", err)
	}
	if uniqueCount == 0 {
		t.Fatal("media_tasks unique(tenant_id, request_id) missing")
	}
	var runnableCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_indexes WHERE tablename='media_tasks' AND indexdef ILIKE '%WHERE%' AND indexdef ILIKE '%queued%' AND indexdef ILIKE '%in_progress%'`,
	).Scan(&runnableCount); err != nil {
		t.Fatalf("partial index probe: %v", err)
	}
	if runnableCount == 0 {
		t.Fatal("media_tasks runnable partial index missing")
	}
}

type mediaSeed struct {
	tenantID int64
	userID   int64
	apiKeyID int64
}

func openMediaPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedMediaUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) mediaSeed {
	t.Helper()
	var seed mediaSeed
	name := fmt.Sprintf("media-%s-%d", suffix, time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seed = seedMediaUserInTenant(t, ctx, pool, seed.tenantID, suffix)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM media_tasks WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM billing_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM balance_holds WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM user_balances WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, seed.tenantID)
	})
	return seed
}

func seedMediaUserInTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, suffix string) mediaSeed {
	t.Helper()
	seed := mediaSeed{tenantID: tenantID}
	unique := fmt.Sprintf("%s-%d", suffix, time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`, seed.tenantID, "user-"+suffix).Scan(&seed.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		seed.tenantID, seed.userID, "key-"+suffix, "$2a$10$media-placeholder", "hk_test_media_"+unique,
	).Scan(&seed.apiKeyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0)`, seed.tenantID, seed.userID); err != nil {
		t.Fatalf("seed balance: %v", err)
	}
	return seed
}

func newIntegrationService(pool *pgxpool.Pool) *Service {
	store := NewPostgresStore(pool, PostgresStoreConfig{BillingPolicyVersion: "test-policy", RequestClass: "standard"})
	return NewService(store, StaticConfigSource{Config: integrationConfig()}, StaticProviderRegistry{"http": NewNoopProvider()})
}

func integrationConfig() Config {
	return Config{
		Enabled: true, ProviderBaseURL: "http://provider.invalid",
		PollInterval: time.Millisecond, TaskTimeout: time.Minute,
		DefaultEstimatedCents: map[string]int64{"image_generation": 123},
		BillingPolicyVersion:  "test-policy", RequestClass: "standard",
	}
}

func submitInput(requestID string) SubmitInput {
	return SubmitInput{RequestID: requestID, TaskType: "image_generation", Provider: "http", InputParams: []byte(`{"prompt":"x"}`)}
}

func submitAndMarkSubmitted(t *testing.T, ctx context.Context, svc *Service, store *PostgresStore, seed mediaSeed, requestID string) Task {
	t.Helper()
	task, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput(requestID))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	leased := leaseTaskForTest(t, ctx, store.pool, task.ID, "submit-worker")
	task, err = store.MarkProviderSubmitted(ctx, leased, "submit-worker", "up-"+requestID, time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkProviderSubmitted: %v", err)
	}
	return task
}

func leaseTaskForTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskID int64, owner string) Task {
	t.Helper()
	now := time.Now().UTC()
	tag, err := pool.Exec(ctx, `
	UPDATE media_tasks
	SET lease_owner=$2, lease_expires_at=$3, updated_at=$4
	WHERE id=$1 AND status IN ('queued','in_progress')`,
		taskID, owner, now.Add(time.Minute), now)
	if err != nil {
		t.Fatalf("lease task %d: %v", taskID, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("lease task %d rows=%d want 1", taskID, tag.RowsAffected())
	}
	task, err := scanTask(pool.QueryRow(ctx, selectTaskSQL+` WHERE id=$1`, taskID))
	if err != nil {
		t.Fatalf("read leased task %d: %v", taskID, err)
	}
	return task
}

func readBalance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64) (decimal.Decimal, decimal.Decimal) {
	t.Helper()
	var balance, held decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance, held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID).Scan(&balance, &held); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return balance, held
}

func assertTaskStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64, want Status) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx, `SELECT status FROM media_tasks WHERE id=$1`, id).Scan(&got); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if Status(got) != want {
		t.Fatalf("task status=%q want %q", got, want)
	}
}

func assertClaimStatusCost(t *testing.T, ctx context.Context, pool *pgxpool.Pool, holdRef, wantStatus string, wantCost decimal.Decimal) {
	t.Helper()
	var status string
	var actual decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE(actual_cost, 0) FROM billing_ledger_claims WHERE id=$1`, mustClaimID(t, holdRef)).Scan(&status, &actual); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if status != wantStatus || !actual.Equal(wantCost) {
		t.Fatalf("claim status/cost=%q/%s want %q/%s", status, actual, wantStatus, wantCost)
	}
}

func mustClaimID(t *testing.T, holdRef string) int64 {
	t.Helper()
	id, err := claimIDFromHoldRef(holdRef)
	if err != nil {
		t.Fatalf("parse hold_ref %q: %v", holdRef, err)
	}
	return id
}

type integrationProvider struct {
	poll PollResult
}

func (p *integrationProvider) Submit(context.Context, SubmitReq) (string, error) {
	return "up-integration", nil
}

func (p *integrationProvider) Poll(context.Context, string) (PollResult, error) {
	return p.poll, nil
}
