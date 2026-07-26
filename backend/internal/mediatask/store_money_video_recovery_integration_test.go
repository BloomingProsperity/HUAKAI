//go:build integration_pg

package mediatask

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	poolruntime "github.com/BloomingProsperity/HUAKAI/internal/pool"
)

func TestDurableVideoSettlementPendingSurvivesFailureAndClaimSweep(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pg := openMediaPool(t, ctx)
	realSettler := billing.NewSettler(pg)
	injected := errors.New("注入结算中断")
	flaky := &failOnceMediaSettler{Settler: realSettler, err: injected}
	graph := seedDurableVideoRecoveryGraph(t, ctx, pg, geminiVideoProviderName, flaky)
	task := markDurableVideoSubmitted(t, ctx, graph, "pending-submit", "operations/pending-1")
	leased := leaseTaskForTest(t, ctx, pg, task.ID, "pending-poll")
	result := PollResult{
		Status: StatusSucceeded, Progress: 100, ActualCents: 77,
		Result:        []byte(`{"status":"completed","uri":"https://media.invalid/pending.mp4"}`),
		RoutingReason: durableMediaRoutingReason(task),
	}

	settled, err := graph.store.CompleteSuccess(ctx, leased, "pending-poll", result, time.Now().UTC())
	if !errors.Is(err, injected) || settled {
		t.Fatalf("首次结算 settled=%v err=%v，期望持久化结果后返回注入错误", settled, err)
	}
	pending, err := graph.store.GetTask(ctx, task.TenantID, task.UserID, task.ID)
	if err != nil {
		t.Fatalf("读取待结算任务: %v", err)
	}
	if pending.Status != StatusSettlementPending || len(pending.Result) == 0 ||
		pending.ActualCents == nil || *pending.ActualCents != 77 ||
		pending.LeaseOwner != "pending-poll" {
		t.Fatalf("成功结果没有耐久进入待结算态: %+v", pending)
	}
	if ok, err := graph.store.CompleteFailure(
		ctx, pending, "pending-poll", "provider_failed", time.Now().UTC(),
	); ok || !errors.Is(err, ErrSettlementPending) {
		t.Fatalf("待结算任务被失败退款: ok=%v err=%v", ok, err)
	}
	if ok, err := graph.store.ExpireTask(
		ctx, pending, "pending-poll", time.Now().UTC(),
	); ok || !errors.Is(err, ErrSettlementPending) {
		t.Fatalf("待结算任务被超时退款: ok=%v err=%v", ok, err)
	}

	if _, err := pg.Exec(ctx, `
UPDATE media_tasks
SET lease_expires_at=NOW()-interval '1 minute'
WHERE id=$1`, task.ID); err != nil {
		t.Fatalf("令任务租约过期: %v", err)
	}
	if _, err := pg.Exec(ctx, `
UPDATE billing_ledger_claims
SET lease_expires_at=NOW()-interval '1 minute'
WHERE tenant_id=$1 AND id=$2`, task.TenantID, graph.claimID); err != nil {
		t.Fatalf("令 claim 租约过期: %v", err)
	}
	if _, err := billing.NewLeaseSweeper(pg, realSettler, 1000).SweepOnce(ctx); err != nil {
		t.Fatalf("清扫待结算 claim: %v", err)
	}
	assertClaimStatusCost(t, ctx, pg, task.HoldRef, "reserving", decimal.Zero)
	_, held := readBalance(t, ctx, pg, task.TenantID, task.UserID)
	if !held.Equal(decimal.RequireFromString("1.23")) {
		t.Fatalf("待结算 claim 被清扫器释放: held=%s", held)
	}

	worker := NewWorker(
		graph.store,
		StaticConfigSource{Config: integrationConfig()},
		StaticProviderRegistry{geminiVideoProviderName: NewNoopProvider()},
		WorkerOptions{Owner: "pending-recovery"},
	)
	processed, err := worker.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("恢复 worker processed=%v err=%v", processed, err)
	}
	assertTaskStatus(t, ctx, pg, task.ID, StatusSucceeded)
	assertClaimStatusCost(t, ctx, pg, task.HoldRef, "committed", decimal.RequireFromString("0.77"))
	balance, held := readBalance(t, ctx, pg, task.TenantID, task.UserID)
	if !balance.Equal(decimal.RequireFromString("9.23")) || !held.IsZero() {
		t.Fatalf("恢复结算后 balance/held=%s/%s want 9.23/0", balance, held)
	}
	assertDurableVideoMoneyEffectCounts(t, ctx, pg, graph, 1, 1, "released_success")

	processed, err = worker.RunOnce(ctx)
	if err != nil || processed {
		t.Fatalf("终态任务被重复结算: processed=%v err=%v", processed, err)
	}
	assertDurableVideoMoneyEffectCounts(t, ctx, pg, graph, 1, 1, "released_success")
}

func TestDurableVideoUnknownReleaseUsesUnifiedMoneyExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pg := openMediaPool(t, ctx)
	graph := seedDurableVideoRecoveryGraph(
		t, ctx, pg, grokVideoProviderName, billing.NewSettler(pg),
	)
	leased := leaseTaskForTest(t, ctx, pg, graph.task.ID, "unknown-submit")
	submitting, err := graph.store.MarkSubmitting(ctx, leased, "unknown-submit", time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkSubmitting: %v", err)
	}
	unknown, err := graph.store.MarkSubmissionUnknown(
		ctx, submitting, "unknown-submit", "provider_submit_outcome_unknown",
		DeriveIdempotencyKey(graph.task.ID, graph.task.RequestID), time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("MarkSubmissionUnknown: %v", err)
	}
	var orphanID int64
	if err := pg.QueryRow(ctx, `
SELECT id
FROM media_task_orphans
WHERE task_id=$1 AND orphan_kind='submission_unknown'`, graph.task.ID).Scan(&orphanID); err != nil {
		t.Fatalf("读取未知提交恢复事实: %v", err)
	}
	if _, advanced, err := graph.store.RequestUnknownSubmissionRelease(
		ctx, orphanID, time.Now().UTC(),
		allowSubmissionRecoveryForTest, nil,
	); err != nil || !advanced {
		t.Fatalf("请求确认未受理 advanced=%v err=%v", advanced, err)
	}

	worker := NewWorker(
		graph.store,
		StaticConfigSource{Config: integrationConfig()},
		StaticProviderRegistry{grokVideoProviderName: NewNoopProvider()},
		WorkerOptions{Owner: "unknown-release"},
	)
	processed, err := worker.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("释放 worker processed=%v err=%v", processed, err)
	}
	assertTaskStatus(t, ctx, pg, graph.task.ID, StatusFailed)
	assertClaimStatusCost(t, ctx, pg, unknown.HoldRef, "aborted", decimal.Zero)
	balance, held := readBalance(t, ctx, pg, unknown.TenantID, unknown.UserID)
	if !balance.Equal(decimal.RequireFromString("10.00")) || !held.IsZero() {
		t.Fatalf("确认未受理后 balance/held=%s/%s want 10/0", balance, held)
	}
	assertDurableVideoMoneyEffectCounts(t, ctx, pg, graph, 1, 1, "released_failure")

	processed, err = worker.RunOnce(ctx)
	if err != nil || processed {
		t.Fatalf("已释放任务被重复处理: processed=%v err=%v", processed, err)
	}
	assertDurableVideoMoneyEffectCounts(t, ctx, pg, graph, 1, 1, "released_failure")
}

func TestDurableVideoRecoveryConvergesAfterCommittedOrAbortedClaim(t *testing.T) {
	t.Run("结算已提交但任务终态未落库", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pg := openMediaPool(t, ctx)
		graph := seedDurableVideoRecoveryGraph(
			t, ctx, pg, geminiVideoProviderName, billing.NewSettler(pg),
		)
		task := markDurableVideoSubmitted(t, ctx, graph, "commit-submit", "operations/commit-1")
		leased := leaseTaskForTest(t, ctx, pg, task.ID, "commit-poll")
		result := PollResult{
			Status: StatusSucceeded, Progress: 100, ActualCents: 77,
			Result: []byte(`{"status":"completed"}`), RoutingReason: durableMediaRoutingReason(task),
		}
		if ok, err := graph.store.CompleteSuccess(
			ctx, leased, "commit-poll", result, time.Now().UTC(),
		); err != nil || !ok {
			t.Fatalf("初次完整结算 ok=%v err=%v", ok, err)
		}
		if _, err := pg.Exec(ctx, `
UPDATE media_tasks
SET status='settlement_pending', progress=99, finished_at=NULL,
    lease_owner=NULL, lease_expires_at=NULL
WHERE id=$1 AND status='succeeded'`, task.ID); err != nil {
			t.Fatalf("模拟结算提交后进程中断: %v", err)
		}
		worker := NewWorker(
			graph.store,
			StaticConfigSource{Config: integrationConfig()},
			StaticProviderRegistry{geminiVideoProviderName: NewNoopProvider()},
			WorkerOptions{Owner: "commit-recovery"},
		)
		if processed, err := worker.RunOnce(ctx); err != nil || !processed {
			t.Fatalf("已提交 claim 恢复 processed=%v err=%v", processed, err)
		}
		assertTaskStatus(t, ctx, pg, task.ID, StatusSucceeded)
		assertDurableVideoMoneyEffectCounts(t, ctx, pg, graph, 1, 1, "released_success")
	})

	t.Run("claim 已释放但上游后来确认成功", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pg := openMediaPool(t, ctx)
		realSettler := billing.NewSettler(pg)
		graph := seedDurableVideoRecoveryGraph(
			t, ctx, pg, grokVideoProviderName, realSettler,
		)
		task := markDurableVideoSubmitted(t, ctx, graph, "abort-submit", "video-abort-1")
		if err := realSettler.Abort(
			ctx, task.TenantID, graph.claimID, "lease_expired", task.RequestID, 0, nil,
		); err != nil {
			t.Fatalf("模拟 claim 抢先释放: %v", err)
		}
		leased := leaseTaskForTest(t, ctx, pg, task.ID, "abort-poll")
		if ok, err := graph.store.CompleteSuccess(ctx, leased, "abort-poll", PollResult{
			Status: StatusSucceeded, Progress: 100, ActualCents: 77,
			Result: []byte(`{"status":"completed"}`), RoutingReason: durableMediaRoutingReason(task),
		}, time.Now().UTC()); err != nil || !ok {
			t.Fatalf("claim 释放后成功收敛 ok=%v err=%v", ok, err)
		}
		assertTaskStatus(t, ctx, pg, task.ID, StatusSucceeded)
		assertClaimStatusCost(t, ctx, pg, task.HoldRef, "aborted", decimal.Zero)
		balance, held := readBalance(t, ctx, pg, task.TenantID, task.UserID)
		if !balance.Equal(decimal.RequireFromString("10.00")) || !held.IsZero() {
			t.Fatalf("抢先释放后不得补扣: balance/held=%s/%s", balance, held)
		}
		if n := countSweptOrphans(t, ctx, graph.store, task.ID); n != 1 {
			t.Fatalf("抢先释放后的成功任务缺少人工追账线索: %d", n)
		}
		assertDurableVideoMoneyEffectCounts(t, ctx, pg, graph, 1, 1, "released_failure")
	})
}

type durableVideoRecoveryGraph struct {
	store     *PostgresStore
	task      Task
	seed      mediaSeed
	claimID   int64
	accountID int64
	token     uuid.UUID
}

func seedDurableVideoRecoveryGraph(
	t *testing.T,
	ctx context.Context,
	pg *pgxpool.Pool,
	providerName string,
	settler billing.Settler,
) durableVideoRecoveryGraph {
	t.Helper()
	seed := seedMediaUser(t, ctx, pg, providerName+"-recovery")
	poolGroupID, accountID, bindingID := seedMediaProviderAccount(
		t, ctx, pg, seed.tenantID, providerName+"-recovery",
	)
	protocolFamily := "grok_chat"
	requestedModel := "grok-video"
	providerModel := "grok-imagine-video"
	if providerName == geminiVideoProviderName {
		protocolFamily = "gemini_messages"
		requestedModel = "veo-video"
		providerModel = "veo-3.1-generate-preview"
	}
	store := NewPostgresStore(pg, PostgresStoreConfig{
		BillingPolicyVersion: "test-policy", RequestClass: "standard",
		ClaimGate: billing.NewClaimGate(pg), Settler: settler,
	})
	task, hit, err := store.CreateTask(ctx, CreateTaskInput{
		TenantID: seed.tenantID, UserID: seed.userID, APIKeyID: seed.apiKeyID,
		RequestID: "req-" + providerName + "-recovery", TaskType: "video_generate",
		Provider: providerName, InputParams: []byte(`{"prompt":"recovery"}`),
		EstimatedCents: 123, BillingPolicyVersion: "test-policy", RequestClass: "standard",
		ProviderAccountID: accountID, PoolGroupID: poolGroupID, ProtocolFamily: protocolFamily,
		RequestedModel: requestedModel, ProviderModelID: providerModel,
		RouteID: "route-" + providerName, BindingID: bindingID,
		BindingRPMLimit: 9, BindingTPMLimit: 900, BindingMaxParallelRequests: 1,
	})
	if err != nil || hit {
		t.Fatalf("创建耐久视频任务 hit=%v err=%v", hit, err)
	}
	claimID := mustClaimID(t, task.HoldRef)
	acquired, err := poolruntime.NewDBSlotManager(pg).Acquire(
		ctx,
		&poolruntime.AccountSnapshot{ID: accountID, TenantID: seed.tenantID},
		poolruntime.SelectionRequest{
			TenantID: seed.tenantID, UserID: seed.userID, APIKeyID: seed.apiKeyID,
			PoolGroupID: poolGroupID, ClaimID: claimID, AttemptSeq: 1,
			BindingID: bindingID, BindingRPMLimit: 9, BindingTPMLimit: 900,
			MaxParallelRequests: 1, RequestedModel: requestedModel,
			EndpointFamily: "media_tasks",
		},
	)
	if err != nil {
		t.Fatalf("占用耐久视频账号槽: %v", err)
	}
	if err := poolruntime.NewDBClaimGate(dbbilling.New(pg)).WriteAcquisition(
		ctx, seed.tenantID, claimID, accountID, acquired.AcquisitionToken,
	); err != nil {
		t.Fatalf("回写账号槽到 claim: %v", err)
	}
	return durableVideoRecoveryGraph{
		store: store, task: task, seed: seed, claimID: claimID,
		accountID: accountID, token: acquired.AcquisitionToken,
	}
}

func markDurableVideoSubmitted(
	t *testing.T,
	ctx context.Context,
	graph durableVideoRecoveryGraph,
	owner string,
	providerTaskID string,
) Task {
	t.Helper()
	leased := leaseTaskForTest(t, ctx, graph.store.pool, graph.task.ID, owner)
	submitting, err := graph.store.MarkSubmitting(ctx, leased, owner, time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkSubmitting: %v", err)
	}
	submitted, err := graph.store.MarkProviderSubmitted(
		ctx, submitting, owner, providerTaskID, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("MarkProviderSubmitted: %v", err)
	}
	return submitted
}

func assertDurableVideoMoneyEffectCounts(
	t *testing.T,
	ctx context.Context,
	pg *pgxpool.Pool,
	graph durableVideoRecoveryGraph,
	wantUsage int,
	wantBillingEvents int,
	wantSlotStatus string,
) {
	t.Helper()
	var usageCount, billingEventCount, slotCount int
	if err := pg.QueryRow(ctx, `
SELECT count(*) FROM usage_records WHERE tenant_id=$1 AND claim_id=$2`,
		graph.seed.tenantID, graph.claimID,
	).Scan(&usageCount); err != nil {
		t.Fatalf("统计用量事实: %v", err)
	}
	if err := pg.QueryRow(ctx, `
SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2`,
		graph.seed.tenantID, graph.claimID,
	).Scan(&billingEventCount); err != nil {
		t.Fatalf("统计账务事件: %v", err)
	}
	if err := pg.QueryRow(ctx, `
SELECT count(*)
FROM pool_slot_acquisitions
WHERE tenant_id=$1 AND claim_id=$2 AND status=$3`,
		graph.seed.tenantID, graph.claimID, wantSlotStatus,
	).Scan(&slotCount); err != nil {
		t.Fatalf("统计账号槽状态: %v", err)
	}
	if usageCount != wantUsage || billingEventCount != wantBillingEvents || slotCount != 1 {
		t.Fatalf("钱账副作用 usage/events/slot=%d/%d/%d want %d/%d/1(%s)",
			usageCount, billingEventCount, slotCount,
			wantUsage, wantBillingEvents, wantSlotStatus)
	}
}

type failOnceMediaSettler struct {
	billing.Settler
	mu     sync.Mutex
	err    error
	failed bool
}

func (s *failOnceMediaSettler) Settle(
	ctx context.Context,
	req billing.SettleRequest,
) (*billing.SettleResult, error) {
	s.mu.Lock()
	if !s.failed {
		s.failed = true
		err := s.err
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()
	return s.Settler.Settle(ctx, req)
}
