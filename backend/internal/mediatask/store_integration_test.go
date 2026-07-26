//go:build integration_pg

package mediatask

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	poolruntime "github.com/BloomingProsperity/HUAKAI/internal/pool"
)

func TestMediaTaskSubmitReservesOnce(t *testing.T) {
	// 变异：从 CreateTask 移除 billing.Reserve；held 会停在 0 而非 1.23。
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

func TestPostgresAccountRequestAdmitterUsesPersistedAccountRPM(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pg := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pg, "account-rate-admission")
	_, accountID, _ := seedMediaProviderAccount(t, ctx, pg, seed.tenantID, "account-rate-admission")
	if _, err := pg.Exec(ctx, `UPDATE provider_accounts SET rpm_limit=1, tpm_limit=0 WHERE id=$1 AND tenant_id=$2`, accountID, seed.tenantID); err != nil {
		t.Fatal(err)
	}
	admitter := NewPostgresAccountRequestAdmitter(pg, poolruntime.NewRatePrecheckCounter())
	if err := admitter.Admit(ctx, seed.tenantID, accountID); err != nil {
		t.Fatalf("第一次账号请求应放行: %v", err)
	}
	if err := admitter.Admit(ctx, seed.tenantID, accountID); !errors.Is(err, errAccountRequestRateLimited) {
		t.Fatalf("第二次账号请求应命中 RPM: %v", err)
	}
}

func TestMediaTaskSubmitRequiresExplicitKeyWhenUserHasMultipleActiveKeys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "multi-key")
	var secondKeyID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
VALUES ($1,$2,$3,$4,$5,'active') RETURNING id`,
		seed.tenantID, seed.userID, "key-multi-second", "$2a$10$media-placeholder",
		fmt.Sprintf("hk_test_media_second_%d", time.Now().UnixNano()),
	).Scan(&secondKeyID); err != nil {
		t.Fatalf("seed second api key: %v", err)
	}
	svc := newIntegrationService(pool)

	_, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput("req-key-ambiguous"))
	if !errors.Is(err, ErrAPIKeyAmbiguous) {
		t.Fatalf("未指定 Key 的 Submit err=%v want ErrAPIKeyAmbiguous", err)
	}
	var taskCount, claimCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_tasks WHERE tenant_id=$1 AND request_id=$2`, seed.tenantID, "req-key-ambiguous").Scan(&taskCount); err != nil {
		t.Fatalf("count ambiguous tasks: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_ledger_claims WHERE tenant_id=$1 AND logical_request_id=$2`, seed.tenantID, "req-key-ambiguous").Scan(&claimCount); err != nil {
		t.Fatalf("count ambiguous claims: %v", err)
	}
	if taskCount != 0 || claimCount != 0 {
		t.Fatalf("冲突请求产生 task/claim=%d/%d want 0/0", taskCount, claimCount)
	}

	explicit := submitInput("req-key-explicit")
	explicit.APIKeyID = secondKeyID
	task, err := svc.Submit(ctx, seed.tenantID, seed.userID, explicit)
	if err != nil {
		t.Fatalf("显式 Key Submit: %v", err)
	}
	if task.APIKeyID != secondKeyID {
		t.Fatalf("task api_key_id=%d want %d", task.APIKeyID, secondKeyID)
	}

	other := seedMediaUserInTenant(t, ctx, pool, seed.tenantID, "multi-key-other-user")
	foreign := submitInput("req-key-foreign")
	foreign.APIKeyID = other.apiKeyID
	if _, err := svc.Submit(ctx, seed.tenantID, seed.userID, foreign); !errors.Is(err, ErrNoActiveAPIKey) {
		t.Fatalf("跨用户 Key Submit err=%v want ErrNoActiveAPIKey", err)
	}
}

func TestMediaTaskIdempotentSubmit(t *testing.T) {
	// 变异：在 reserve 前跳过 (tenant_id, request_id) 查找；第二次提交会把 held 翻倍到 2.46。
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
	// 变异：入账时用预估成本而非 actual_cents；balance 会变成 8.77 而非 9.23。
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
	claimID, err := claimIDFromHoldRef(task.HoldRef)
	if err != nil {
		t.Fatalf("claimIDFromHoldRef: %v", err)
	}
	var source string
	var providerless, tokenless bool
	var usageCost decimal.Decimal
	if err := pool.QueryRow(ctx, `
SELECT settlement_source, provider_account_id IS NULL, acquisition_token IS NULL, actual_cost
FROM usage_records
WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, claimID,
	).Scan(&source, &providerless, &tokenless, &usageCost); err != nil {
		t.Fatalf("read external relay usage: %v", err)
	}
	if source != billing.SettlementSourceExternalMediaRelay || !providerless || !tokenless ||
		!usageCost.Equal(decimal.RequireFromString("0.77")) {
		t.Fatalf("external relay usage source/account/token/cost=%q/%v/%v/%s",
			source, providerless, tokenless, usageCost)
	}
}

// TestMediaTaskSuccessZeroActualAnchorsToEstimate 锁住 bug ② 修复:上游 Poll 未回实际用量
// (ActualCents=0,图像/视频任务创建型上游的常态)时,成功任务必须按【预扣的预估】结算(锚定
// EstimatedCents),绝不按 $0 结算,否则平台白吃真实上游成本、等同给客户做了全额退式 $0 结算。
// 变异(§14):删掉 store_money.go 里 `if billedCents <= 0 { billedCents = locked.EstimatedCents }`
// 这个下限,actual=0 会按 $0 入账 —— balance 停在 10.00(不扣费)、claim committed@0.00,下面对
// balance=8.77 / claim committed@1.23 的断言必然全红。
func TestMediaTaskSuccessZeroActualAnchorsToEstimate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "zeroactual")
	svc := newIntegrationService(pool)
	store := svc.store.(*PostgresStore)

	task := submitAndMarkSubmitted(t, ctx, svc, store, seed, "req-zeroactual")
	leased := leaseTaskForTest(t, ctx, pool, task.ID, "worker-zero")
	// 上游回成功但不带用量:ActualCents 保持 0。
	ok, err := store.CompleteSuccess(ctx, leased, "worker-zero", PollResult{Status: StatusSucceeded, Progress: 100, ActualCents: 0, Result: []byte(`{"ok":true}`)}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CompleteSuccess(零用量): %v", err)
	}
	if !ok {
		t.Fatal("CompleteSuccess(零用量) ok=false,任务未走到终态")
	}

	// 锚定预估结算:扣 1.23(预扣的预估,而非 0),held 清零。
	balance, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !balance.Equal(decimal.RequireFromString("8.77")) || !held.Equal(decimal.Zero) {
		t.Fatalf("balance/held=%s/%s want 8.77/0(actual=0 必须锚定预估 1.23 结算,不得 $0 白吃成本)", balance, held)
	}
	assertTaskStatus(t, ctx, pool, task.ID, StatusSucceeded)
	assertClaimStatusCost(t, ctx, pool, task.HoldRef, "committed", decimal.RequireFromString("1.23"))
	var actualCents int64
	if err := pool.QueryRow(ctx, `SELECT actual_cents FROM media_tasks WHERE id=$1`, task.ID).Scan(&actualCents); err != nil {
		t.Fatalf("读取媒体任务上游报告成本: %v", err)
	}
	if actualCents != 0 {
		t.Fatalf("media_tasks.actual_cents=%d，期望保留上游未报告成本的 0；客户收费应从 claim 读取", actualCents)
	}
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
	// 变异：失败时用 Capture(0) 而非 Release；claim 可能被 commit 且失败审计出错。
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
	// 变异：标记超时为 expired 时不做 billing.Release；held 会停在 1.23。
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
	if n := countSweptOrphans(t, ctx, store, task.ID); n != 1 {
		t.Fatalf("已有上游任务 ID 的超时任务应落孤儿线索,orphan rows=%d want 1", n)
	}
}

func TestMediaTaskTimeoutWithoutProviderTaskDoesNotCreateOrphan(t *testing.T) {
	// 变异：无上游任务 ID 时也无条件 persistOrphanTx；orphan rows 会从 0 变 1。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "timeout-no-provider")
	svc := newIntegrationService(pool)
	store := svc.store.(*PostgresStore)

	task, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput("req-timeout-no-provider"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	leased := leaseTaskForTest(t, ctx, pool, task.ID, "worker-timeout-no-provider")
	ok, err := store.ExpireTask(ctx, leased, "worker-timeout-no-provider", time.Now().UTC())
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
	if n := countSweptOrphans(t, ctx, store, task.ID); n != 0 {
		t.Fatalf("无上游任务 ID 时不应落孤儿线索,orphan rows=%d want 0", n)
	}
}

func TestMediaTaskWorkerFencing_NoDoubleSettle(t *testing.T) {
	// 变异：从 AcquireLease 或终态更新中移除 SKIP LOCKED / lease_owner；
	// 两个 worker 会追加两条 claim_committed 事件或重复扣费。
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

func TestDurablyBoundVideoUnifiedMoneySettlesExactAccountAndReleasesSlot(t *testing.T) {
	tests := []struct {
		name             string
		provider         string
		protocolFamily   string
		requestedModel   string
		providerModel    string
		expectedEndpoint string
	}{
		{name: "Grok", provider: grokVideoProviderName, protocolFamily: "grok_chat", requestedModel: "grok-video", providerModel: "grok-imagine-video", expectedEndpoint: "/v1/videos/generations"},
		{name: "Gemini", provider: geminiVideoProviderName, protocolFamily: "gemini_messages", requestedModel: "veo-video", providerModel: "veo-3.1-generate-preview", expectedEndpoint: "/v1beta/models/veo-3.1-generate-preview:predictLongRunning"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testDurablyBoundVideoUnifiedMoney(t, tt.provider, tt.protocolFamily, tt.requestedModel, tt.providerModel, tt.expectedEndpoint)
		})
	}
}

func TestMediaTaskDeferredLeaseIsNotRunnableBeforeRetryAt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pg := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pg, "deferred-lease")
	store := newIntegrationService(pg).store.(*PostgresStore)
	task, err := newIntegrationService(pg).Submit(ctx, seed.tenantID, seed.userID, submitInput("req-deferred-lease"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	now := time.Now().UTC()
	leased, err := store.AcquireLease(ctx, "defer-worker", time.Minute, now)
	if err != nil || leased.ID != task.ID {
		t.Fatalf("AcquireLease: task=%d err=%v", leased.ID, err)
	}
	retryAt := now.Add(time.Minute)
	if err := store.DeferLease(ctx, leased, "defer-worker", now, retryAt); err != nil {
		t.Fatalf("DeferLease: %v", err)
	}
	if _, err := store.AcquireLease(ctx, "too-early", time.Minute, now.Add(30*time.Second)); !errors.Is(err, ErrNoRunnableTask) {
		t.Fatalf("退避窗口内应无可运行任务: %v", err)
	}
	retried, err := store.AcquireLease(ctx, "retry-worker", time.Minute, retryAt)
	if err != nil || retried.ID != task.ID {
		t.Fatalf("到期后任务没有重新可运行: task=%d err=%v", retried.ID, err)
	}
}

func testDurablyBoundVideoUnifiedMoney(t *testing.T, providerName, protocolFamily, requestedModel, providerModel, expectedEndpoint string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pg := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pg, providerName+"-unified-money")
	poolGroupID, accountID, bindingID := seedMediaProviderAccount(t, ctx, pg, seed.tenantID, providerName+"-unified-money")
	store := NewPostgresStore(pg, PostgresStoreConfig{
		BillingPolicyVersion: "test-policy",
		RequestClass:         "standard",
		ClaimGate:            billing.NewClaimGate(pg),
		Settler:              billing.NewSettler(pg),
	})

	task, hit, err := store.CreateTask(ctx, CreateTaskInput{
		TenantID: seed.tenantID, UserID: seed.userID, APIKeyID: seed.apiKeyID,
		RequestID: "req-" + providerName + "-unified-money", TaskType: "video_generate",
		Provider: providerName, InputParams: []byte(fmt.Sprintf(`{"model":%q,"prompt":"x"}`, providerModel)),
		EstimatedCents: 123, BillingPolicyVersion: "test-policy", RequestClass: "standard",
		ProviderAccountID: accountID, PoolGroupID: poolGroupID, ProtocolFamily: protocolFamily,
		RequestedModel: requestedModel, ProviderModelID: providerModel, RouteID: "route-" + providerName,
		BindingID: bindingID, BindingRPMLimit: 9, BindingTPMLimit: 900, BindingMaxParallelRequests: 1,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if hit {
		t.Fatal("首次创建不应命中幂等任务")
	}
	if task.BindingID != bindingID || task.BindingRPMLimit != 9 || task.BindingTPMLimit != 900 || task.BindingMaxParallelRequests != 1 {
		t.Fatalf("任务没有持久化原绑定合同: %+v", task)
	}
	claimID := mustClaimID(t, task.HoldRef)
	_, held := readBalance(t, ctx, pg, seed.tenantID, seed.userID)
	if !held.Equal(decimal.RequireFromString("1.23")) {
		t.Fatalf("预留后 held=%s want 1.23", held)
	}

	acquired, err := poolruntime.NewDBSlotManager(pg).Acquire(ctx, &poolruntime.AccountSnapshot{
		ID: accountID, TenantID: seed.tenantID,
	}, poolruntime.SelectionRequest{
		TenantID: seed.tenantID, UserID: seed.userID, APIKeyID: seed.apiKeyID,
		PoolGroupID: poolGroupID, ClaimID: claimID, AttemptSeq: 1,
		BindingID: bindingID, BindingRPMLimit: 9, BindingTPMLimit: 900, MaxParallelRequests: 1,
		RequestedModel: requestedModel, EndpointFamily: "media_tasks",
	})
	if err != nil {
		t.Fatalf("Acquire slot: %v", err)
	}
	var storedBindingID int64
	if err := pg.QueryRow(ctx, `
SELECT binding_id FROM pool_slot_acquisitions WHERE acquisition_token=$1`, acquired.AcquisitionToken).Scan(&storedBindingID); err != nil {
		t.Fatalf("读取绑定槽: %v", err)
	}
	if storedBindingID != bindingID {
		t.Fatalf("槽位绑定=%d want %d", storedBindingID, bindingID)
	}
	if _, err := poolruntime.NewDBSlotManager(pg).Acquire(ctx, &poolruntime.AccountSnapshot{
		ID: accountID, TenantID: seed.tenantID,
	}, poolruntime.SelectionRequest{
		TenantID: seed.tenantID, PoolGroupID: poolGroupID,
		BindingID: bindingID, MaxParallelRequests: 1,
	}); !errors.Is(err, poolruntime.ErrBindingConcurrencyLimited) {
		t.Fatalf("同一绑定第二个并发槽必须拒绝: %v", err)
	}
	if err := poolruntime.NewDBClaimGate(dbbilling.New(pg)).WriteAcquisition(
		ctx, seed.tenantID, claimID, accountID, acquired.AcquisitionToken,
	); err != nil {
		t.Fatalf("WriteAcquisition: %v", err)
	}

	leased := leaseTaskForTest(t, ctx, pg, task.ID, providerName+"-submit-worker")
	leased, err = store.MarkSubmitting(ctx, leased, providerName+"-submit-worker", time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkSubmitting: %v", err)
	}
	if _, err := store.MarkProviderSubmitted(ctx, leased, providerName+"-submit-worker", "video-upstream-1", time.Now().UTC()); err != nil {
		t.Fatalf("MarkProviderSubmitted: %v", err)
	}
	leased = leaseTaskForTest(t, ctx, pg, task.ID, providerName+"-poll-worker")
	settled, err := store.CompleteSuccess(ctx, leased, providerName+"-poll-worker", PollResult{
		Status: StatusSucceeded, Progress: 100, ActualCents: 77,
		Result:           []byte(`{"url":"https://media.invalid/video.mp4"}`),
		AcquisitionToken: acquired.AcquisitionToken,
		RoutingReason:    []byte(`{"selected_account_id":1,"reason":"test"}`),
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CompleteSuccess: %v", err)
	}
	if !settled {
		t.Fatal("CompleteSuccess settled=false")
	}

	balance, held := readBalance(t, ctx, pg, seed.tenantID, seed.userID)
	if !balance.Equal(decimal.RequireFromString("9.23")) || !held.Equal(decimal.Zero) {
		t.Fatalf("balance/held=%s/%s want 9.23/0", balance, held)
	}
	assertTaskStatus(t, ctx, pg, task.ID, StatusSucceeded)
	assertClaimStatusCost(t, ctx, pg, task.HoldRef, "committed", decimal.RequireFromString("0.77"))

	var gotAPIKeyID, gotUserID, gotAccountID int64
	var gotRequestedModel, gotUpstreamModel string
	var gotCost decimal.Decimal
	if err := pg.QueryRow(ctx, `
SELECT api_key_id, user_id, provider_account_id, requested_model, upstream_model, actual_cost
FROM usage_records
WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, claimID,
	).Scan(&gotAPIKeyID, &gotUserID, &gotAccountID, &gotRequestedModel, &gotUpstreamModel, &gotCost); err != nil {
		t.Fatalf("read usage record: %v", err)
	}
	if gotAPIKeyID != seed.apiKeyID || gotUserID != seed.userID || gotAccountID != accountID ||
		gotRequestedModel != requestedModel || gotUpstreamModel != providerModel ||
		!gotCost.Equal(decimal.RequireFromString("0.77")) {
		t.Fatalf("usage 归属/模型/成本不一致: key=%d user=%d account=%d requested=%q upstream=%q cost=%s",
			gotAPIKeyID, gotUserID, gotAccountID, gotRequestedModel, gotUpstreamModel, gotCost)
	}
	var inFlight int
	var released int
	if err := pg.QueryRow(ctx, `SELECT in_flight_count FROM provider_accounts WHERE tenant_id=$1 AND id=$2`, seed.tenantID, accountID).Scan(&inFlight); err != nil {
		t.Fatalf("read account in_flight_count: %v", err)
	}
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM pool_slot_acquisitions WHERE tenant_id=$1 AND claim_id=$2 AND status='released_success'`, seed.tenantID, claimID).Scan(&released); err != nil {
		t.Fatalf("read released slot: %v", err)
	}
	if inFlight != 0 || released != 1 {
		t.Fatalf("账号槽未收敛: in_flight=%d released_success=%d", inFlight, released)
	}
	if got := durableVideoSubmitEndpoint(task); got != expectedEndpoint {
		t.Fatalf("结算日志端点=%q want %q", got, expectedEndpoint)
	}
}

func TestMediaTaskSubmitAtomic_ReserveAndRowTogether(t *testing.T) {
	// 变异：在媒体任务 insert 事务之外做 reserve；注入 insert 失败后 held 会停在 1.23。
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
	// 变异：从 Get/List/幂等守卫中去掉 user_id；用户 B 会看到或重放用户 A 的任务。
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
	// 变异：省略 unique(tenant_id, request_id) 或可运行的部分索引；schema 探测会失败。
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
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM media_task_orphans WHERE tenant_id=$1`, seed.tenantID)
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

func seedMediaProviderAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, suffix string) (int64, int64, int64) {
	t.Helper()
	unique := fmt.Sprintf("%s-%d", suffix, time.Now().UnixNano())
	var providerID, poolGroupID, channelID, accountID, modelID, bindingID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
VALUES ($1,$2,$3,'grok_chat') RETURNING id`, tenantID, "provider-"+unique, "Provider "+unique).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`, tenantID, "pool-"+unique).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1,$2,$3) RETURNING id`, tenantID, poolGroupID, "channel-"+unique).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, cap_concurrency, in_flight_count)
VALUES ($1,$2,$3,$4,'api_key',5,0) RETURNING id`, tenantID, providerID, channelID, "account-"+unique).Scan(&accountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO models (tenant_id, scope, canonical_id, protocol_family, default_provider_model_id, default_context_window, status)
VALUES ($1,'tenant',$2,'grok_chat',$2,128000,'active') RETURNING id`, tenantID, "model-"+unique).Scan(&modelID); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO model_pool_bindings (tenant_id, model_id, pool_group_id, priority, weight, enabled, rpm_limit, tpm_limit, max_parallel_requests)
VALUES ($1,$2,$3,100,1,true,9,900,2) RETURNING id`, tenantID, modelID, poolGroupID).Scan(&bindingID); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if err := cleanupUnifiedMediaMoneyRows(cleanupCtx, pool, tenantID); err != nil {
			t.Errorf("清理统一媒体钱账数据: %v", err)
			return
		}
		for _, item := range []struct {
			name      string
			statement string
			id        int64
		}{
			{name: "模型绑定", statement: `DELETE FROM model_pool_bindings WHERE tenant_id=$1 AND id=$2`, id: bindingID},
			{name: "模型", statement: `DELETE FROM models WHERE tenant_id=$1 AND id=$2`, id: modelID},
			{name: "上游账号", statement: `DELETE FROM provider_accounts WHERE tenant_id=$1 AND id=$2`, id: accountID},
			{name: "渠道", statement: `DELETE FROM channels WHERE tenant_id=$1 AND id=$2`, id: channelID},
			{name: "池组", statement: `DELETE FROM pool_groups WHERE tenant_id=$1 AND id=$2`, id: poolGroupID},
			{name: "上游", statement: `DELETE FROM providers WHERE tenant_id=$1 AND id=$2`, id: providerID},
		} {
			if _, err := pool.Exec(cleanupCtx, item.statement, tenantID, item.id); err != nil {
				t.Errorf("清理%s: %v", item.name, err)
				return
			}
		}
	})
	return poolGroupID, accountID, bindingID
}

func cleanupUnifiedMediaMoneyRows(ctx context.Context, pool *pgxpool.Pool, tenantID int64) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, statement := range []string{
		`ALTER TABLE usage_records DISABLE TRIGGER usage_records_append_only_delete`,
		`DELETE FROM usage_records WHERE tenant_id=$1`,
		`ALTER TABLE usage_records ENABLE TRIGGER usage_records_append_only_delete`,
		`ALTER TABLE billing_events DISABLE TRIGGER billing_events_append_only_delete`,
		`DELETE FROM billing_events WHERE tenant_id=$1`,
		`ALTER TABLE billing_events ENABLE TRIGGER billing_events_append_only_delete`,
		`DELETE FROM balance_holds WHERE tenant_id=$1`,
		`DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`,
		`DELETE FROM billing_ledger_claims WHERE tenant_id=$1`,
	} {
		if strings.Contains(statement, "$1") {
			_, err = tx.Exec(ctx, statement, tenantID)
		} else {
			_, err = tx.Exec(ctx, statement)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
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
	leased, err = store.MarkSubmitting(ctx, leased, "submit-worker", time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkSubmitting: %v", err)
	}
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
	WHERE id=$1 AND status IN ('queued','submitting','submission_releasing','in_progress','settlement_pending')`,
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
