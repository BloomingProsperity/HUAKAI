//go:build integration_pg

package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	poolbinding "github.com/BloomingProsperity/HUAKAI/internal/pool/binding"
	pooldispatcher "github.com/BloomingProsperity/HUAKAI/internal/pool/dispatcher"
)

func TestSettler_NilPool_ReturnsTypedError(t *testing.T) {
	settler := NewSettler(nil)
	_, err := settler.Settle(context.Background(), SettleRequest{TenantID: 1, ClaimID: 1})
	if !errors.Is(err, ErrPoolNotConfigured) {
		t.Fatalf("expected ErrPoolNotConfigured from Settle; got %v", err)
	}
	if err := settler.Abort(context.Background(), 1, 1, "test abort", "req-abort-nil-pool", 0, nil); !errors.Is(err, ErrPoolNotConfigured) {
		t.Fatalf("expected ErrPoolNotConfigured from Abort; got %v", err)
	}
}

func TestAT_OBS_004_AtomicFiveEffect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-004")
	settler := NewSettler(pool)

	actualCost := decimal.RequireFromString("0.02000000")
	req := settleRequest(seed, actualCost)
	res, err := settler.Settle(ctx, req)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if res == nil || !res.NewUserBalance.Equal(decimal.Zero) {
		t.Fatalf("NewUserBalance must be decimal.Zero in Phase B.5; got %+v", res)
	}

	var usageCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_records WHERE claim_id=$1 AND actual_cost=$2`,
		seed.claimID, actualCost,
	).Scan(&usageCount); err != nil {
		t.Fatalf("count usage_records: %v", err)
	}
	if usageCount != 1 {
		t.Fatalf("expected one usage_record with claim_id=%d; got %d", seed.claimID, usageCount)
	}

	// 成功路径的 usage 行携带来自迁移 0008 的 snapshot_version 列的
	// router+registry 标记。
	var snapshot *string
	if err := pool.QueryRow(ctx,
		`SELECT snapshot_version FROM usage_records WHERE claim_id=$1`,
		seed.claimID,
	).Scan(&snapshot); err != nil {
		t.Fatalf("read snapshot_version: %v", err)
	}
	if snapshot == nil || *snapshot != "registry:99:7;router:v0.1-phase-c" {
		got := "<nil>"
		if snapshot != nil {
			got = *snapshot
		}
		t.Fatalf("usage_records.snapshot_version mismatch; got %q want %q", got, "registry:99:7;router:v0.1-phase-c")
	}

	var eventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_committed' AND actual_cost=$2`,
		seed.claimID, actualCost,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count billing_events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("expected one claim_committed billing_event; got %d", eventCount)
	}

	var status string
	var storedCost decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT status, actual_cost FROM billing_ledger_claims WHERE id=$1`,
		seed.claimID,
	).Scan(&status, &storedCost); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if status != "committed" || !storedCost.Equal(actualCost) {
		t.Fatalf("claim not committed with actual cost; status=%q actual_cost=%s", status, storedCost)
	}

	var inFlight int
	if err := pool.QueryRow(ctx,
		`SELECT in_flight_count FROM provider_accounts WHERE id=$1`,
		seed.providerAccountID,
	).Scan(&inFlight); err != nil {
		t.Fatalf("read in_flight_count: %v", err)
	}
	if inFlight != 1 {
		t.Fatalf("provider account in_flight_count must decrement from 2 to 1; got %d", inFlight)
	}
}

func TestSettler_SettleRetriesSerializationConflictWithoutDuplicateEffects(t *testing.T) {
	// 变异检查:若 DefaultSettler.Settle 去掉 retryTx2 包装、直接调用 settleOnce,
	// 第一次 Tx2 在 claim committed 更新处抛 40001 后会直接失败,本测试红。
	// 若回滚边界被破坏,第一次尝试写过的 usage/billing/slot/hold 会留下重复证据。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-serialization-retry")
	settler := NewSettler(pool)

	if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0) ON CONFLICT (tenant_id, user_id) DO NOTHING`, seed.tenantID, seed.userID); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	if err := reserveAndCommitBalanceHold(ctx, t, pool, seed.tenantID, seed.userID, seed.claimID, decimal.RequireFromString("0.01000000")); err != nil {
		t.Fatalf("reserve hold: %v", err)
	}
	seqRef, cleanup := installSettlerClaimCommitSerializationFailure(t, ctx, pool, seed.claimID)
	defer cleanup()

	actualCost := decimal.RequireFromString("0.03000000")
	res, err := settler.Settle(ctx, settleRequest(seed, actualCost))
	if err != nil {
		t.Fatalf("Settle 应重试一次 40001 后成功: %v", err)
	}
	if res == nil || !res.NewUserBalance.Equal(decimal.RequireFromString("9.97")) {
		t.Fatalf("Settle result=%+v want NewUserBalance=9.97", res)
	}
	assertSequenceLastValue(t, ctx, pool, seqRef, 2)

	assertSettledEvidenceOnce(t, ctx, pool, seed, actualCost)
	var holdState string
	if err := pool.QueryRow(ctx, `SELECT state FROM balance_holds WHERE claim_id=$1`, seed.claimID).Scan(&holdState); err != nil {
		t.Fatalf("read hold state: %v", err)
	}
	if holdState != "captured" {
		t.Fatalf("hold state=%q want captured", holdState)
	}
	var balance, held decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance, held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&balance, &held); err != nil {
		t.Fatalf("read user balance: %v", err)
	}
	if !balance.Equal(decimal.RequireFromString("9.97")) || !held.Equal(decimal.Zero) {
		t.Fatalf("balance/held=%s/%s want 9.97/0", balance, held)
	}
}

// TestAT_OBS_004_RollbackOnSlotReleaseMiss 是 AT-OBS-004 / AT-POOL-019 的强(回滚)变体:Tx2 必须
// all-or-nothing。注入一个 mid-Tx2 失败——把 slot 提前置为非 'acquired',使 Settle 内的
// ReleaseSlotAndDecrementInFlight 返 0 → ErrSlotReleaseMissed(settler.go),而这发生在 billing_event
// + usage_record 已在同一事务写入之后。验证那两笔更早的写入被**完整回滚**(无孤儿 usage_record /
// 无新增 billing_event / claim 未 committed)。这正是 phase-b5/integration-sprint 计划要求过、但一直
// 缺失的 "kill mid-Tx2 → no partial rows" 测试,也是 pool AT-POOL-019(Tx2 原子性)的实质覆盖。
//
// Mutation:若 settler 把 billing_event/usage 写在独立事务(破坏单 Serializable Tx 原子性),回滚
// 不生效 → "无 usage_record" 断言转红。
func TestAT_OBS_004_RollbackOnSlotReleaseMiss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-rollback")
	settler := NewSettler(pool)

	// 同 claim 当前的 billing_events 基线(seed 可能已写过 reserve 类事件)。
	var eventsBefore int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_events WHERE claim_id=$1`, seed.claimID).Scan(&eventsBefore); err != nil {
		t.Fatalf("count billing_events before: %v", err)
	}

	// 注入 mid-Tx2 失败:把 slot 提前置为非 'acquired',ReleaseSlotAndDecrementInFlight 将返 0。
	if _, err := pool.Exec(ctx,
		`UPDATE pool_slot_acquisitions SET status='released_success', released_at=NOW() WHERE acquisition_token=$1`,
		seed.acquisitionToken); err != nil {
		t.Fatalf("预置 slot 为 released: %v", err)
	}

	actualCost := decimal.RequireFromString("0.02000000")
	if _, err := settler.Settle(ctx, settleRequest(seed, actualCost)); !errors.Is(err, ErrSlotReleaseMissed) {
		t.Fatalf("slot 非 acquired 时 Settle 应返回 ErrSlotReleaseMissed,got %v", err)
	}

	// 回滚验证 1:usage_record 必须为 0(它在 ReleaseSlot 失败前已写入同事务,失败后整体回滚;
	// 非原子=会留下孤儿 usage_record)。
	var usageCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM usage_records WHERE claim_id=$1`, seed.claimID).Scan(&usageCount); err != nil {
		t.Fatalf("count usage_records: %v", err)
	}
	if usageCount != 0 {
		t.Fatalf("Tx2 失败后不应有 usage_record(单 Tx 回滚被破坏=孤儿写),got %d", usageCount)
	}

	// 回滚验证 2:billing_events 数量不变(settle 写的事件被回滚,回到基线)。
	var eventsAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_events WHERE claim_id=$1`, seed.claimID).Scan(&eventsAfter); err != nil {
		t.Fatalf("count billing_events after: %v", err)
	}
	if eventsAfter != eventsBefore {
		t.Fatalf("Tx2 失败后 billing_events 应回滚回基线 %d,got %d", eventsBefore, eventsAfter)
	}

	// 回滚验证 3:claim 不应 committed(整事务回滚,状态保持)。
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM billing_ledger_claims WHERE id=$1`, seed.claimID).Scan(&status); err != nil {
		t.Fatalf("read claim status: %v", err)
	}
	if status == "committed" {
		t.Fatalf("Tx2 失败后 claim 不应 committed,got %q", status)
	}

	// 回滚验证 4(slot 维度强判别):ReleaseSlot 命中 0 行即不减 in_flight,且整事务回滚,
	// 故 provider_accounts.in_flight_count 必须保持 seed 的 2 不变(非幂等/非原子减会被抓)。
	var inFlightAfter int
	if err := pool.QueryRow(ctx, `SELECT in_flight_count FROM provider_accounts WHERE id=$1`, seed.providerAccountID).Scan(&inFlightAfter); err != nil {
		t.Fatalf("read in_flight after failed settle: %v", err)
	}
	if inFlightAfter != 2 {
		t.Fatalf("ReleaseSlot 失败后 in_flight_count 不应递减,应保持 2,got %d", inFlightAfter)
	}
}

func TestSettler_SettlePersistsCacheTierTokensAndCosts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-cache-tier-costs")
	settler := NewSettler(pool)

	req := settleRequest(seed, decimal.RequireFromString("0.24500000"))
	req.Draft.CacheCreationTokens = 150
	req.Draft.CacheCreation5mTokens = 100
	req.Draft.CacheCreation1hTokens = 50
	req.Draft.CacheReadTokens = 200
	req.Draft.CacheCreationCost = decimal.RequireFromString("0.225")
	req.Draft.CacheReadCost = decimal.RequireFromString("0.02")

	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	var got5m, got1h, gotRead int32
	var gotCreationCost, gotReadCost decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT cache_creation_5m_tokens, cache_creation_1h_tokens, cache_read_tokens,
		        cache_creation_cost, cache_read_cost
		   FROM usage_records WHERE claim_id=$1`,
		seed.claimID,
	).Scan(&got5m, &got1h, &gotRead, &gotCreationCost, &gotReadCost); err != nil {
		t.Fatalf("read cache-tier usage_record fields: %v", err)
	}

	// 变异守卫:把 settler 的 cache-tier 字段还原成硬编码零值,会使每个持久化值
	// 都与这些输入不同,于是本测试转红。
	if got5m != 100 {
		t.Fatalf("cache_creation_5m_tokens=%d want 100", got5m)
	}
	if got1h != 50 {
		t.Fatalf("cache_creation_1h_tokens=%d want 50", got1h)
	}
	if gotRead != 200 {
		t.Fatalf("cache_read_tokens=%d want 200", gotRead)
	}
	if !gotCreationCost.Equal(req.Draft.CacheCreationCost) {
		t.Fatalf("cache_creation_cost=%s want %s", gotCreationCost, req.Draft.CacheCreationCost)
	}
	if !gotReadCost.Equal(req.Draft.CacheReadCost) {
		t.Fatalf("cache_read_cost=%s want %s", gotReadCost, req.Draft.CacheReadCost)
	}
}

func TestSettler_SettleZerosCacheBucketCostsForNonChargeableAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-cache-tier-nonchargeable")
	settler := NewSettler(pool)

	req := settleRequest(seed, decimal.RequireFromString("0.24500000"))
	req.Stream = true
	req.StreamAttempt = &Attempt{
		State:                  StreamStateFailed,
		StreamTerminatedReason: "upstream_5xx",
	}
	req.Draft.CacheCreationTokens = 150
	req.Draft.CacheCreation5mTokens = 100
	req.Draft.CacheCreation1hTokens = 50
	req.Draft.CacheReadTokens = 200
	req.Draft.CacheCreationCost = decimal.RequireFromString("0.225")
	req.Draft.CacheReadCost = decimal.RequireFromString("0.02")

	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	var actualCost, gotCreationCost, gotReadCost decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT actual_cost, cache_creation_cost, cache_read_cost
		   FROM usage_records WHERE claim_id=$1`,
		seed.claimID,
	).Scan(&actualCost, &gotCreationCost, &gotReadCost); err != nil {
		t.Fatalf("read cache-tier cost fields: %v", err)
	}

	// 变异:若 cache 桶成本未经 CostForAttempt 门控,cache_creation_cost 会保持非零而 actual_cost=0 -> RED。
	if !actualCost.IsZero() {
		t.Fatalf("actual_cost=%s want 0", actualCost)
	}
	if !gotCreationCost.IsZero() {
		t.Fatalf("cache_creation_cost=%s want 0", gotCreationCost)
	}
	if !gotReadCost.IsZero() {
		t.Fatalf("cache_read_cost=%s want 0", gotReadCost)
	}
}

func TestSettler_AbortPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-abort")
	settler := NewSettler(pool)

	if err := settler.Abort(ctx, seed.tenantID, seed.claimID, "test abort", "req-settle-abort", 0, nil); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	var status string
	var abortedReason *string
	if err := pool.QueryRow(ctx,
		`SELECT status, aborted_reason FROM billing_ledger_claims WHERE id=$1`,
		seed.claimID,
	).Scan(&status, &abortedReason); err != nil {
		t.Fatalf("read aborted claim: %v", err)
	}
	if status != "aborted" || abortedReason == nil || *abortedReason != "test abort" {
		t.Fatalf("claim abort fields mismatch: status=%q reason=%v", status, abortedReason)
	}

	var eventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_aborted' AND actual_cost=0`,
		seed.claimID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count abort billing_events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("expected one claim_aborted billing_event; got %d", eventCount)
	}
	var auditRequestID *string
	if err := pool.QueryRow(ctx,
		`SELECT audit_request_id FROM billing_events WHERE claim_id=$1 AND event_type='claim_aborted'`,
		seed.claimID,
	).Scan(&auditRequestID); err != nil {
		t.Fatalf("read abort billing_event audit_request_id: %v", err)
	}
	if auditRequestID == nil || *auditRequestID != "req-settle-abort" {
		t.Fatalf("abort billing_event audit_request_id=%v want req-settle-abort", auditRequestID)
	}

	var usageCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_records
		 WHERE claim_id=$1 AND actual_cost=0 AND end_class='unknown_termination'`,
		seed.claimID,
	).Scan(&usageCount); err != nil {
		t.Fatalf("count usage_records: %v", err)
	}
	if usageCount != 1 {
		t.Fatalf("abort path must write one zero-cost usage_record per T2-INV-42; got %d", usageCount)
	}
	var abortTokensInput int32
	var abortActualCost decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT tokens_input, actual_cost FROM usage_records WHERE claim_id=$1`,
		seed.claimID,
	).Scan(&abortTokensInput, &abortActualCost); err != nil {
		t.Fatalf("read abort usage_record: %v", err)
	}
	if abortTokensInput != 0 || !abortActualCost.Equal(decimal.Zero) {
		t.Fatalf("default abort usage_record tokens_input=%d actual_cost=%s; want 0/0", abortTokensInput, abortActualCost)
	}

	var inFlight int
	if err := pool.QueryRow(ctx, `SELECT in_flight_count FROM provider_accounts WHERE id=$1`, seed.providerAccountID).Scan(&inFlight); err != nil {
		t.Fatalf("read in_flight_count: %v", err)
	}
	if inFlight != 1 {
		t.Fatalf("abort path must release slot + decrement in_flight_count from 2 to 1 per T2-INV-27 (slot acquired before abort); got %d", inFlight)
	}
}

func TestSettler_AbortRetriesSerializationConflictWithoutDuplicateEffects(t *testing.T) {
	// 变异检查:若 DefaultSettler.Abort 去掉 retryTx2 包装、直接调用 abortOnce,
	// 第一次 Tx2 在 claim_aborted 事件插入处抛 40001 后会直接失败,本测试红。
	// 该失败点位于 claim 状态更新和 hold release 之后,可验证整事务回滚后不会重复退款/审计。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "abort-serialization-retry")
	settler := NewSettler(pool)

	if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0) ON CONFLICT (tenant_id, user_id) DO NOTHING`, seed.tenantID, seed.userID); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	if err := reserveAndCommitBalanceHold(ctx, t, pool, seed.tenantID, seed.userID, seed.claimID, decimal.RequireFromString("0.01000000")); err != nil {
		t.Fatalf("reserve hold: %v", err)
	}
	seqRef, cleanup := installSettlerAbortEventSerializationFailure(t, ctx, pool, seed.claimID)
	defer cleanup()

	if err := settler.Abort(ctx, seed.tenantID, seed.claimID, "serialization_retry_abort", "req-abort-serialization-retry", 7, nil); err != nil {
		t.Fatalf("Abort 应重试一次 40001 后成功: %v", err)
	}
	assertSequenceLastValue(t, ctx, pool, seqRef, 2)
	assertAbortedEvidenceOnce(t, ctx, pool, seed, "serialization_retry_abort", 7)

	var holdState string
	if err := pool.QueryRow(ctx, `SELECT state FROM balance_holds WHERE claim_id=$1`, seed.claimID).Scan(&holdState); err != nil {
		t.Fatalf("read hold state: %v", err)
	}
	if holdState != "released" {
		t.Fatalf("hold state=%q want released", holdState)
	}
	var held decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&held); err != nil {
		t.Fatalf("read held: %v", err)
	}
	if !held.Equal(decimal.Zero) {
		t.Fatalf("held=%s want 0", held)
	}
}

func TestSettler_AbortRecordsObservedInputTokensAtZeroCost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-abort-observed-input")
	settler := NewSettler(pool)

	if err := settler.Abort(ctx, seed.tenantID, seed.claimID, "input_only_interrupted", "req-settle-abort-input", 37, nil); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	var tokensInput int32
	var actualCost decimal.Decimal
	var inputCost decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT tokens_input, actual_cost, input_cost FROM usage_records WHERE claim_id=$1`,
		seed.claimID,
	).Scan(&tokensInput, &actualCost, &inputCost); err != nil {
		t.Fatalf("read abort usage_record: %v", err)
	}
	if tokensInput != 37 {
		t.Fatalf("abort usage_record tokens_input=%d want 37", tokensInput)
	}
	if !actualCost.Equal(decimal.Zero) || !inputCost.Equal(decimal.Zero) {
		t.Fatalf("abort costs actual=%s input=%s want zero", actualCost, inputCost)
	}
}

func TestSettler_AbortWritesProtocolLossEvidence(t *testing.T) {
	// Mutation: 若将 Abort 写入固定 [] 会导致本断言失败（期望与输入不一致）。
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-abort-protocol-loss")
	set := NewSettler(pool)

	want := json.RawMessage(`[{"feature":"tool_choice_downgrade","severity":"warning"}]`)
	if err := set.Abort(ctx, seed.tenantID, seed.claimID, "protocol_loss_test", "req-settle-abort-protocol-loss", 0, want); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	var got []byte
	if err := pool.QueryRow(ctx,
		`SELECT protocol_loss FROM usage_records WHERE claim_id=$1 ORDER BY id DESC LIMIT 1`,
		seed.claimID,
	).Scan(&got); err != nil {
		t.Fatalf("read protocol_loss: %v", err)
	}
	var gotNormalized any
	var wantNormalized any
	if err := json.Unmarshal(got, &gotNormalized); err != nil {
		t.Fatalf("unmarshal persisted protocol_loss: %v", err)
	}
	if err := json.Unmarshal(want, &wantNormalized); err != nil {
		t.Fatalf("unmarshal want protocol_loss: %v", err)
	}
	if !reflect.DeepEqual(gotNormalized, wantNormalized) {
		t.Fatalf("protocol_loss persisted=%s want=%s", string(got), string(want))
	}
}

func TestSettler_SettleWritesProtocolLossEvidence(t *testing.T) {
	// 变异:settler 硬编码 []byte("[]")(修复前的 bug)而非读取 req.ProtocolLoss
	// → Settle 持久化的是 [] ≠ want → RED。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-protocol-loss")
	set := NewSettler(pool)

	if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0) ON CONFLICT (tenant_id, user_id) DO NOTHING`, seed.tenantID, seed.userID); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	if err := reserveAndCommitBalanceHold(ctx, t, pool, seed.tenantID, seed.userID, seed.claimID, decimal.RequireFromString("0.01000000")); err != nil {
		t.Fatalf("reserve hold: %v", err)
	}

	want := json.RawMessage(`[{"feature":"tool_choice_downgrade","severity":"warning"}]`)
	req := settleRequest(seed, decimal.NewFromFloat(0.03))
	req.ProtocolLoss = want
	if _, err := set.Settle(ctx, req); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	var got []byte
	if err := pool.QueryRow(ctx,
		`SELECT protocol_loss FROM usage_records WHERE claim_id=$1 ORDER BY id DESC LIMIT 1`,
		seed.claimID,
	).Scan(&got); err != nil {
		t.Fatalf("read protocol_loss: %v", err)
	}
	var gotNormalized any
	var wantNormalized any
	if err := json.Unmarshal(got, &gotNormalized); err != nil {
		t.Fatalf("unmarshal persisted protocol_loss: %v", err)
	}
	if err := json.Unmarshal(want, &wantNormalized); err != nil {
		t.Fatalf("unmarshal want protocol_loss: %v", err)
	}
	if !reflect.DeepEqual(gotNormalized, wantNormalized) {
		t.Fatalf("protocol_loss persisted=%s want=%s", string(got), string(want))
	}
}

func TestPR4_AbortReReserveCrossPoolFinalSettleOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pg := openPool(t, ctx)
	graph := seedRetryAtomicityGraph(t, ctx, pg, "pr4-final")
	gate := NewClaimGate(pg)
	settler := NewSettler(pg)
	slotManager := pooldispatcher.NewDBSlotManager(pg)
	claimWriter := poolbinding.NewDBClaimGate(dbbilling.New(pg))

	req := baseRequest(graph.tenantID, graph.apiKeyID, graph.userID)
	req.LogicalRequestID = "logical-" + uuid.NewString()
	req.NormalizedPayloadHash = "payload-" + uuid.NewString()
	req.PoolingGroupID = graph.firstPoolID
	req.PredictedCost = decimal.RequireFromString("0.01000000")

	first, err := gate.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	firstAcq, err := slotManager.Acquire(ctx, &pool.AccountSnapshot{
		ID:       graph.firstAccountID,
		TenantID: graph.tenantID,
	}, pool.SelectionRequest{
		TenantID:       graph.tenantID,
		UserID:         graph.userID,
		APIKeyID:       graph.apiKeyID,
		PoolGroupID:    graph.firstPoolID,
		ClaimID:        first.ClaimID,
		AttemptSeq:     1,
		RequestedModel: req.RequestedModel,
		EndpointFamily: req.EndpointFamily,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := claimWriter.WriteAcquisition(ctx, graph.tenantID, first.ClaimID, graph.firstAccountID, firstAcq.AcquisitionToken); err != nil {
		t.Fatalf("first write acquisition: %v", err)
	}
	if err := settler.Abort(ctx, graph.tenantID, first.ClaimID, "upstream_5xx", "req-pr4-attempt-1", 0, nil); err != nil {
		t.Fatalf("first abort: %v", err)
	}
	assertAccountInFlight(t, ctx, pg, graph.firstAccountID, 0)
	assertAbortEvidence(t, ctx, pg, first.ClaimID, 1, 1)

	req.PoolingGroupID = graph.secondPoolID
	req.PredictedCost = decimal.RequireFromString("0.02000000")
	second, err := gate.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("second Reserve re-reserve: %v", err)
	}
	if second.ClaimID != first.ClaimID {
		t.Fatalf("re-reserve claim id=%d want same %d", second.ClaimID, first.ClaimID)
	}
	assertClaimReReservedClean(t, ctx, pg, first.ClaimID, graph.secondPoolID, 2)

	secondAcq, err := slotManager.Acquire(ctx, &pool.AccountSnapshot{
		ID:       graph.secondAccountID,
		TenantID: graph.tenantID,
	}, pool.SelectionRequest{
		TenantID:       graph.tenantID,
		UserID:         graph.userID,
		APIKeyID:       graph.apiKeyID,
		PoolGroupID:    graph.secondPoolID,
		ClaimID:        second.ClaimID,
		AttemptSeq:     2,
		RequestedModel: req.RequestedModel,
		EndpointFamily: req.EndpointFamily,
	})
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if err := claimWriter.WriteAcquisition(ctx, graph.tenantID, second.ClaimID, graph.secondAccountID, secondAcq.AcquisitionToken); err != nil {
		t.Fatalf("second write acquisition: %v", err)
	}
	settleReq := retryAtomicitySettleRequest(graph, second.ClaimID, graph.secondAccountID, secondAcq.AcquisitionToken, 2, decimal.RequireFromString("0.03000000"))
	if _, err := settler.Settle(ctx, settleReq); err != nil {
		t.Fatalf("final Settle: %v", err)
	}
	assertAccountInFlight(t, ctx, pg, graph.secondAccountID, 0)
	assertPositiveCommittedUsageOnce(t, ctx, pg, second.ClaimID)
	assertFinalClaimPool(t, ctx, pg, second.ClaimID, graph.secondPoolID)

	replay, err := gate.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("idempotent replay Reserve: %v", err)
	}
	if !replay.IdempotencyHit || replay.ClaimID != first.ClaimID {
		t.Fatalf("idempotent replay result=%+v want hit on claim %d", replay, first.ClaimID)
	}
}

func TestPR4_ReReserveClearsStaleAcquisitionBeforePreAcquireAbort(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pg := openPool(t, ctx)
	graph := seedRetryAtomicityGraph(t, ctx, pg, "pr4-clear")
	gate := NewClaimGate(pg)
	settler := NewSettler(pg)
	slotManager := pooldispatcher.NewDBSlotManager(pg)
	claimWriter := poolbinding.NewDBClaimGate(dbbilling.New(pg))

	req := baseRequest(graph.tenantID, graph.apiKeyID, graph.userID)
	req.LogicalRequestID = "logical-" + uuid.NewString()
	req.NormalizedPayloadHash = "payload-" + uuid.NewString()
	req.PoolingGroupID = graph.firstPoolID

	first, err := gate.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	firstAcq, err := slotManager.Acquire(ctx, &pool.AccountSnapshot{
		ID:       graph.firstAccountID,
		TenantID: graph.tenantID,
	}, pool.SelectionRequest{
		TenantID:       graph.tenantID,
		UserID:         graph.userID,
		APIKeyID:       graph.apiKeyID,
		PoolGroupID:    graph.firstPoolID,
		ClaimID:        first.ClaimID,
		AttemptSeq:     1,
		RequestedModel: req.RequestedModel,
		EndpointFamily: req.EndpointFamily,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := claimWriter.WriteAcquisition(ctx, graph.tenantID, first.ClaimID, graph.firstAccountID, firstAcq.AcquisitionToken); err != nil {
		t.Fatalf("first write acquisition: %v", err)
	}
	if err := settler.Abort(ctx, graph.tenantID, first.ClaimID, "upstream_5xx", "req-pr4-clear-attempt-1", 0, nil); err != nil {
		t.Fatalf("first abort: %v", err)
	}
	assertAccountInFlight(t, ctx, pg, graph.firstAccountID, 0)

	req.PoolingGroupID = graph.secondPoolID
	second, err := gate.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("second Reserve re-reserve: %v", err)
	}
	assertClaimReReservedClean(t, ctx, pg, second.ClaimID, graph.secondPoolID, 2)
	if err := settler.Abort(ctx, graph.tenantID, second.ClaimID, "pre_acquire_retry_exhausted", "req-pr4-clear-attempt-2", 0, nil); err != nil {
		t.Fatalf("pre-acquire abort after re-reserve must not release stale token: %v", err)
	}
	assertAccountInFlight(t, ctx, pg, graph.firstAccountID, 0)
	assertAbortEvidence(t, ctx, pg, second.ClaimID, 2, 1)
}

func TestSettler_AbortCrossTenantRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-abort-xtenant")
	settler := NewSettler(pool)

	wrongTenant := seed.tenantID + 99999
	if err := settler.Abort(ctx, wrongTenant, seed.claimID, "cross-tenant", "req-settle-abort-xtenant", 0, nil); !errors.Is(err, ErrClaimNotReserving) {
		t.Fatalf("cross-tenant abort must be rejected with ErrClaimNotReserving; got %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM billing_ledger_claims WHERE id=$1`,
		seed.claimID,
	).Scan(&status); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if status != "reserving" {
		t.Fatalf("cross-tenant abort must leave claim untouched; status=%q", status)
	}
}

func TestSettler_TokenMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-token-mismatch")
	settler := NewSettler(pool)

	req := settleRequest(seed, decimal.RequireFromString("0.02000000"))
	req.AcquisitionToken = uuid.New()
	_, err := settler.Settle(ctx, req)
	if !errors.Is(err, ErrAcquisitionTokenMismatch) {
		t.Fatalf("expected ErrAcquisitionTokenMismatch; got %v", err)
	}

	var status string
	var actualCost *decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT status, actual_cost FROM billing_ledger_claims WHERE id=$1`,
		seed.claimID,
	).Scan(&status, &actualCost); err != nil {
		t.Fatalf("read claim after mismatch: %v", err)
	}
	if status != "reserving" || actualCost != nil {
		t.Fatalf("token mismatch must leave claim untouched; status=%q actual_cost=%v", status, actualCost)
	}
}

func TestSettler_AmbiguousUsageEndClassMapsToDBEnum(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-ambiguous")
	settler := NewSettler(pool)

	req := settleRequest(seed, decimal.RequireFromString("0.02000000"))
	req.Draft.EndClass = gateway.AmbiguousUsage
	req.Draft.UsageSource = gateway.UsageSourceAmbiguous
	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("Settle with AmbiguousUsage end class must map to DB enum and succeed; got %v", err)
	}

	var dbEndClass string
	if err := pool.QueryRow(ctx,
		`SELECT end_class FROM usage_records WHERE claim_id=$1`, seed.claimID,
	).Scan(&dbEndClass); err != nil {
		t.Fatalf("read usage_record end_class: %v", err)
	}
	if dbEndClass != "usage_ambiguous" {
		t.Fatalf("AmbiguousUsage gateway value must map to DB enum 'usage_ambiguous'; got %q", dbEndClass)
	}
}

func TestSettler_AlreadyCommitted_NoOp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-already-committed")
	settler := NewSettler(pool)

	req := settleRequest(seed, decimal.RequireFromString("0.02000000"))
	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("first Settle: %v", err)
	}
	_, err := settler.Settle(ctx, req)
	if !errors.Is(err, ErrClaimNotReserving) {
		t.Fatalf("second Settle expected ErrClaimNotReserving; got %v", err)
	}
}

func TestSettler_UsageInsertFailureKeepsBillingEventAndDLQ(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-usage-dlq")
	settler := NewSettler(pool)

	req := settleRequest(seed, decimal.RequireFromString("0.02000000"))
	req.Draft.EndClass = gateway.StreamEndClass("bad_end_class_for_dlq")
	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("Settle must commit billing_event + DLQ when usage insert fails; got %v", err)
	}

	var eventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_committed'`,
		seed.claimID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count billing_events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("billing_event must survive usage insert failure; got %d", eventCount)
	}

	var usageCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM usage_records WHERE claim_id=$1`, seed.claimID).Scan(&usageCount); err != nil {
		t.Fatalf("count usage_records: %v", err)
	}
	if usageCount != 0 {
		t.Fatalf("bad usage row must not be inserted; got %d", usageCount)
	}

	var dlqCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_record_dlq
		 WHERE tenant_id=$1 AND claim_id=$2 AND event_kind='usage_record'
		   AND lane='HIGH' AND status='pending'`,
		seed.tenantID, seed.claimID,
	).Scan(&dlqCount); err != nil {
		t.Fatalf("count usage_record_dlq: %v", err)
	}
	if dlqCount != 1 {
		t.Fatalf("usage insert failure must enqueue one HIGH usage_record DLQ row; got %d", dlqCount)
	}
}

func TestSettler_ReplicaIntentQueuedPrimaryStillCommits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "settle-replica-intent")
	settler := NewSettler(pool, WithReplicaTarget("replica-test"))

	req := settleRequest(seed, decimal.RequireFromString("0.02000000"))
	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("Settle with async replica intent: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM billing_ledger_claims WHERE id=$1`, seed.claimID).Scan(&status); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if status != "committed" {
		t.Fatalf("primary claim must commit while replica is async; status=%q", status)
	}

	var replicaRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_record_dlq
		 WHERE tenant_id=$1 AND claim_id=$2 AND event_kind='billing_event_replica'
		   AND replica_target='replica-test' AND replica_status='pending'`,
		seed.tenantID, seed.claimID,
	).Scan(&replicaRows); err != nil {
		t.Fatalf("count replica intent: %v", err)
	}
	if replicaRows != 1 {
		t.Fatalf("expected one async replica intent; got %d", replicaRows)
	}
}

func TestAT_AUDIT_001_028_RefundQueuesBillingEventReplica(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-replica-intent")
	settler := NewSettler(pool, WithReplicaTarget("refund-replica-test"))

	if _, err := settler.Settle(ctx, settleRequest(seed, decimal.RequireFromString("0.02000000"))); err != nil {
		t.Fatalf("Settle before refund: %v", err)
	}
	refund, err := settler.Refund(ctx, RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        seed.claimID,
		AmountMicroUSD: 7000,
		Reason:         "audit_mismatch",
		AuditRequestID: "req-refund-replica#audit_refund",
	})
	if err != nil {
		t.Fatalf("Refund with async replica intent: %v", err)
	}
	if refund == nil || refund.BillingEventID == 0 {
		t.Fatalf("refund result missing billing event id: %+v", refund)
	}

	var replicaRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_record_dlq
		 WHERE tenant_id=$1 AND claim_id=$2 AND event_kind='billing_event_replica'
		   AND replica_target='refund-replica-test' AND replica_status='pending'
		   AND source_table='billing_events' AND source_id=$3
		   AND payload->>'event_type'='reconciliation_appended'
		   AND payload->>'actual_cost_signed'='-0.00700000'`,
		seed.tenantID, seed.claimID, refund.BillingEventID,
	).Scan(&replicaRows); err != nil {
		t.Fatalf("count refund replica intent: %v", err)
	}
	if replicaRows != 1 {
		t.Fatalf("expected one refund replica intent; got %d", replicaRows)
	}
}

func TestUsageRecordImageColumns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "usage-image-audit")
	settler := NewSettler(pool)
	actualCost := decimal.RequireFromString("0.00600000")
	req := settleRequest(seed, actualCost)
	size := "1024x1024"
	req.RequestedModel = "dall-e-3"
	req.Draft.ImageCount = 2
	req.Draft.ImageSize = &size
	req.Draft.ImageSizeBreakdown = []byte(`{"1024x1024":2}`)

	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	var gotCount int32
	var gotSize *string
	var gotBreakdown map[string]int
	if err := pool.QueryRow(ctx,
		`SELECT image_count, image_size, image_size_breakdown FROM usage_records WHERE claim_id=$1`,
		seed.claimID,
	).Scan(&gotCount, &gotSize, &gotBreakdown); err != nil {
		t.Fatalf("read image audit columns: %v", err)
	}
	if gotCount != 2 {
		t.Fatalf("image_count=%d want 2", gotCount)
	}
	if gotSize == nil || *gotSize != "1024x1024" {
		t.Fatalf("image_size=%v want 1024x1024", gotSize)
	}
	if gotBreakdown["1024x1024"] != 2 {
		t.Fatalf("image_size_breakdown=%v want 1024x1024=2", gotBreakdown)
	}
}

func TestUsageRecordOriginColumns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "usage-origin-audit")
	settler := NewSettler(pool)
	req := settleRequest(seed, decimal.RequireFromString("0.02000000"))
	ip := "198.51.100.9"
	ua := "huakai-origin-audit/1.0"
	req.Draft.IPAddress = &ip
	req.Draft.UserAgent = &ua

	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	var gotIP, gotUA *string
	if err := pool.QueryRow(ctx,
		`SELECT ip_address, user_agent FROM usage_records WHERE claim_id=$1`,
		seed.claimID,
	).Scan(&gotIP, &gotUA); err != nil {
		t.Fatalf("read origin audit columns: %v", err)
	}
	if gotIP == nil || *gotIP != ip {
		t.Fatalf("ip_address=%v want %s", gotIP, ip)
	}
	if gotUA == nil || *gotUA != ua {
		t.Fatalf("user_agent=%v want %s", gotUA, ua)
	}
}

func TestSettler_ChargeUnchangedByAuditColumns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "usage-audit-cost")
	settler := NewSettler(pool)
	actualCost := decimal.RequireFromString("0.02000000")
	req := settleRequest(seed, actualCost)
	size := "1024x1024"
	ip := "198.51.100.9"
	ua := "huakai-audit-cost/1.0"
	req.Draft.ImageCount = 2
	req.Draft.ImageSize = &size
	req.Draft.ImageSizeBreakdown = []byte(`{"1024x1024":2}`)
	req.Draft.IPAddress = &ip
	req.Draft.UserAgent = &ua

	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	var usageCost, claimCost decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT ur.actual_cost, blc.actual_cost
		   FROM usage_records ur
		   JOIN billing_ledger_claims blc ON blc.id = ur.claim_id
		  WHERE ur.claim_id=$1`,
		seed.claimID,
	).Scan(&usageCost, &claimCost); err != nil {
		t.Fatalf("read settled costs: %v", err)
	}
	if !usageCost.Equal(actualCost) || !claimCost.Equal(actualCost) {
		t.Fatalf("audit columns changed charge: usage_cost=%s claim_cost=%s want %s", usageCost, claimCost, actualCost)
	}
}

type settlerSeed struct {
	tenantID          int64
	apiKeyID          int64
	userID            int64
	providerID        int64
	poolGroupID       int64
	channelID         int64
	providerAccountID int64
	claimID           int64
	acquisitionToken  uuid.UUID
	fingerprint       string
}

type retryAtomicityGraph struct {
	tenantID        int64
	apiKeyID        int64
	userID          int64
	providerID      int64
	firstPoolID     int64
	secondPoolID    int64
	firstChannelID  int64
	secondChannelID int64
	firstAccountID  int64
	secondAccountID int64
	fingerprint     string
}

func seedRetryAtomicityGraph(t *testing.T, ctx context.Context, pg *pgxpool.Pool, suffix string) retryAtomicityGraph {
	t.Helper()
	unique := fmt.Sprintf("%s-%s", suffix, uuid.NewString())
	tenantID, apiKeyID, userID := seedTenant(t, ctx, pg, unique)
	graph := retryAtomicityGraph{
		tenantID:    tenantID,
		apiKeyID:    apiKeyID,
		userID:      userID,
		fingerprint: "fingerprint-" + unique,
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM usage_record_dlq WHERE tenant_id=$1`, tenantID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM usage_records WHERE tenant_id=$1`, tenantID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM billing_events WHERE tenant_id=$1`, tenantID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, tenantID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, tenantID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
	})

	if err := pg.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		tenantID, "provider-"+unique, "Provider "+unique,
	).Scan(&graph.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	graph.firstPoolID = seedPoolGroup(t, ctx, pg, tenantID, "pool-a-"+unique)
	graph.secondPoolID = seedPoolGroup(t, ctx, pg, tenantID, "pool-b-"+unique)
	graph.firstChannelID = seedChannel(t, ctx, pg, tenantID, graph.firstPoolID, "channel-a-"+unique)
	graph.secondChannelID = seedChannel(t, ctx, pg, tenantID, graph.secondPoolID, "channel-b-"+unique)
	graph.firstAccountID = seedProviderAccount(t, ctx, pg, tenantID, graph.providerID, graph.firstChannelID, "account-a-"+unique)
	graph.secondAccountID = seedProviderAccount(t, ctx, pg, tenantID, graph.providerID, graph.secondChannelID, "account-b-"+unique)
	return graph
}

func seedPoolGroup(t *testing.T, ctx context.Context, pg *pgxpool.Pool, tenantID int64, name string) int64 {
	t.Helper()
	var id int64
	if err := pg.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, name,
	).Scan(&id); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	return id
}

func seedChannel(t *testing.T, ctx context.Context, pg *pgxpool.Pool, tenantID, poolGroupID int64, name string) int64 {
	t.Helper()
	var id int64
	if err := pg.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, name,
	).Scan(&id); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return id
}

func seedProviderAccount(t *testing.T, ctx context.Context, pg *pgxpool.Pool, tenantID, providerID, channelID int64, name string) int64 {
	t.Helper()
	var id int64
	if err := pg.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, cap_concurrency, in_flight_count)
		 VALUES ($1, $2, $3, $4, 'api_key', 4, 0) RETURNING id`,
		tenantID, providerID, channelID, name,
	).Scan(&id); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}
	return id
}

func retryAtomicitySettleRequest(graph retryAtomicityGraph, claimID, accountID int64, token uuid.UUID, attemptSeq int32, actualCost decimal.Decimal) SettleRequest {
	return SettleRequest{
		ClaimID:           claimID,
		AccountID:         accountID,
		AcquisitionToken:  token,
		ActualCost:        actualCost,
		TenantID:          graph.tenantID,
		APIKeyID:          graph.apiKeyID,
		UserID:            graph.userID,
		ProviderAccountID: accountID,
		AttemptSeq:        attemptSeq,
		RequestedModel:    "claude-3-5-sonnet",
		RequestedAt:       time.Now().UTC(),
		UpstreamModel:     "claude-3-5-sonnet",
		Stream:            false,
		Fingerprint:       graph.fingerprint,
		SnapshotVersion:   "registry:pr4;router:retry-atomicity",
		Draft: gateway.UsageRecordDraft{
			TokensInput:           11,
			TokensOutput:          22,
			ActualCost:            actualCost,
			RoutingReason:         []byte(`{"route":"pr4_retry_atomicity"}`),
			EndClass:              gateway.StreamEndClass("non_streaming"),
			UsageSource:           gateway.UsageSourceReported,
			PendingReconciliation: false,
		},
	}
}

func assertAccountInFlight(t *testing.T, ctx context.Context, pg *pgxpool.Pool, accountID int64, want int) {
	t.Helper()
	var got int
	if err := pg.QueryRow(ctx, `SELECT in_flight_count FROM provider_accounts WHERE id=$1`, accountID).Scan(&got); err != nil {
		t.Fatalf("read account in_flight_count: %v", err)
	}
	if got != want {
		t.Fatalf("account %d in_flight_count=%d want %d", accountID, got, want)
	}
}

func assertAbortEvidence(t *testing.T, ctx context.Context, pg *pgxpool.Pool, claimID int64, wantEvents, wantZeroUsage int) {
	t.Helper()
	var events int
	if err := pg.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_aborted' AND actual_cost=0`,
		claimID,
	).Scan(&events); err != nil {
		t.Fatalf("count abort events: %v", err)
	}
	if events != wantEvents {
		t.Fatalf("abort events=%d want %d", events, wantEvents)
	}
	var zeroUsage int
	if err := pg.QueryRow(ctx,
		`SELECT count(*) FROM usage_records WHERE claim_id=$1 AND actual_cost=0`,
		claimID,
	).Scan(&zeroUsage); err != nil {
		t.Fatalf("count zero usage records: %v", err)
	}
	if zeroUsage != wantZeroUsage {
		t.Fatalf("zero-cost usage records=%d want %d", zeroUsage, wantZeroUsage)
	}
}

func assertClaimReReservedClean(t *testing.T, ctx context.Context, pg *pgxpool.Pool, claimID, wantPoolID int64, wantAttemptSeq int32) {
	t.Helper()
	var status string
	var gotPoolID *int64
	var providerAccountID *int64
	var acquisitionToken pgtype.UUID
	var attemptSeq int32
	if err := pg.QueryRow(ctx,
		`SELECT status, pooling_group_id, provider_account_id, acquisition_token, attempt_seq
		 FROM billing_ledger_claims WHERE id=$1`,
		claimID,
	).Scan(&status, &gotPoolID, &providerAccountID, &acquisitionToken, &attemptSeq); err != nil {
		t.Fatalf("read re-reserved claim: %v", err)
	}
	if status != "reserving" {
		t.Fatalf("claim status=%q want reserving", status)
	}
	if gotPoolID == nil || *gotPoolID != wantPoolID {
		t.Fatalf("claim pooling_group_id=%v want %d", gotPoolID, wantPoolID)
	}
	if providerAccountID != nil {
		t.Fatalf("re-reserved claim kept stale provider_account_id=%d", *providerAccountID)
	}
	if acquisitionToken.Valid {
		t.Fatalf("re-reserved claim kept stale acquisition_token=%x", acquisitionToken.Bytes)
	}
	if attemptSeq != wantAttemptSeq {
		t.Fatalf("claim attempt_seq=%d want %d", attemptSeq, wantAttemptSeq)
	}
}

func assertPositiveCommittedUsageOnce(t *testing.T, ctx context.Context, pg *pgxpool.Pool, claimID int64) {
	t.Helper()
	var positiveUsage int
	if err := pg.QueryRow(ctx,
		`SELECT count(*) FROM usage_records WHERE claim_id=$1 AND actual_cost > 0`,
		claimID,
	).Scan(&positiveUsage); err != nil {
		t.Fatalf("count positive usage records: %v", err)
	}
	if positiveUsage != 1 {
		t.Fatalf("positive committed usage records=%d want 1", positiveUsage)
	}
	var commitEvents int
	if err := pg.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_committed' AND actual_cost > 0`,
		claimID,
	).Scan(&commitEvents); err != nil {
		t.Fatalf("count commit events: %v", err)
	}
	if commitEvents != 1 {
		t.Fatalf("positive claim_committed events=%d want 1", commitEvents)
	}
}

func assertSettledEvidenceOnce(t *testing.T, ctx context.Context, pg *pgxpool.Pool, seed settlerSeed, actualCost decimal.Decimal) {
	t.Helper()
	var status string
	var storedCost decimal.Decimal
	if err := pg.QueryRow(ctx, `SELECT status, actual_cost FROM billing_ledger_claims WHERE id=$1`, seed.claimID).Scan(&status, &storedCost); err != nil {
		t.Fatalf("read settled claim: %v", err)
	}
	if status != "committed" || !storedCost.Equal(actualCost) {
		t.Fatalf("claim status/cost=%q/%s want committed/%s", status, storedCost, actualCost)
	}
	var usageCount int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM usage_records WHERE claim_id=$1 AND actual_cost=$2`, seed.claimID, actualCost).Scan(&usageCount); err != nil {
		t.Fatalf("count settled usage_records: %v", err)
	}
	if usageCount != 1 {
		t.Fatalf("settle retry 后 usage_records=%d want 1", usageCount)
	}
	var eventCount int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_committed' AND actual_cost=$2`, seed.claimID, actualCost).Scan(&eventCount); err != nil {
		t.Fatalf("count claim_committed events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("settle retry 后 claim_committed events=%d want 1", eventCount)
	}
	var releasedSlots int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM pool_slot_acquisitions WHERE claim_id=$1 AND status='released_success'`, seed.claimID).Scan(&releasedSlots); err != nil {
		t.Fatalf("count released slots: %v", err)
	}
	if releasedSlots != 1 {
		t.Fatalf("settle retry 后 released slots=%d want 1", releasedSlots)
	}
	assertAccountInFlight(t, ctx, pg, seed.providerAccountID, 1)
}

func assertAbortedEvidenceOnce(t *testing.T, ctx context.Context, pg *pgxpool.Pool, seed settlerSeed, reason string, observedInputTokens int32) {
	t.Helper()
	var status string
	var abortedReason *string
	if err := pg.QueryRow(ctx, `SELECT status, aborted_reason FROM billing_ledger_claims WHERE id=$1`, seed.claimID).Scan(&status, &abortedReason); err != nil {
		t.Fatalf("read aborted claim: %v", err)
	}
	if status != "aborted" || abortedReason == nil || *abortedReason != reason {
		t.Fatalf("claim status/reason=%q/%v want aborted/%s", status, abortedReason, reason)
	}
	var eventCount int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_aborted' AND actual_cost=0`, seed.claimID).Scan(&eventCount); err != nil {
		t.Fatalf("count claim_aborted events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("abort retry 后 claim_aborted events=%d want 1", eventCount)
	}
	var usageCount int
	var tokensInput int32
	if err := pg.QueryRow(ctx, `SELECT count(*), COALESCE(max(tokens_input), 0) FROM usage_records WHERE claim_id=$1 AND actual_cost=0`, seed.claimID).Scan(&usageCount, &tokensInput); err != nil {
		t.Fatalf("count abort usage_records: %v", err)
	}
	if usageCount != 1 || tokensInput != observedInputTokens {
		t.Fatalf("abort retry 后 usage_records/tokens_input=%d/%d want 1/%d", usageCount, tokensInput, observedInputTokens)
	}
	var releasedSlots int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM pool_slot_acquisitions WHERE claim_id=$1 AND status='released_success'`, seed.claimID).Scan(&releasedSlots); err != nil {
		t.Fatalf("count released slots: %v", err)
	}
	if releasedSlots != 1 {
		t.Fatalf("abort retry 后 released slots=%d want 1", releasedSlots)
	}
	assertAccountInFlight(t, ctx, pg, seed.providerAccountID, 1)
}

func assertFinalClaimPool(t *testing.T, ctx context.Context, pg *pgxpool.Pool, claimID, wantPoolID int64) {
	t.Helper()
	var status string
	var poolID *int64
	if err := pg.QueryRow(ctx,
		`SELECT status, pooling_group_id FROM billing_ledger_claims WHERE id=$1`,
		claimID,
	).Scan(&status, &poolID); err != nil {
		t.Fatalf("read final claim pool: %v", err)
	}
	if status != "committed" {
		t.Fatalf("final claim status=%q want committed", status)
	}
	if poolID == nil || *poolID != wantPoolID {
		t.Fatalf("final claim pooling_group_id=%v want %d", poolID, wantPoolID)
	}
}

func installSettlerClaimCommitSerializationFailure(t *testing.T, ctx context.Context, pg *pgxpool.Pool, claimID int64) (string, func()) {
	t.Helper()
	suffix := fmt.Sprintf("%d_%d", claimID, time.Now().UTC().UnixNano())
	seqName := "huakai_tx2_settle_seq_" + suffix
	fnName := "huakai_tx2_settle_fail_" + suffix
	triggerName := "huakai_tx2_settle_fail_" + suffix
	seqIdent := pgx.Identifier{"public", seqName}.Sanitize()
	fnIdent := pgx.Identifier{"public", fnName}.Sanitize()
	triggerIdent := pgx.Identifier{triggerName}.Sanitize()
	seqRef := "public." + seqName

	if _, err := pg.Exec(ctx, fmt.Sprintf(`CREATE SEQUENCE %s`, seqIdent)); err != nil {
		t.Fatalf("create settle serialization sequence: %v", err)
	}
	createFn := fmt.Sprintf(`
CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.id = %d
		AND OLD.status = 'reserving'
		AND NEW.status = 'committed'
		AND nextval('%s'::regclass) = 1 THEN
		RAISE EXCEPTION 'forced settle Tx2 serialization conflict' USING ERRCODE = '40001';
	END IF;
	RETURN NEW;
END;
$$`, fnIdent, claimID, seqRef)
	if _, err := pg.Exec(ctx, createFn); err != nil {
		_, _ = pg.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, seqIdent))
		t.Fatalf("create settle serialization function: %v", err)
	}
	createTrigger := fmt.Sprintf(`
CREATE TRIGGER %s
BEFORE UPDATE OF status ON billing_ledger_claims
FOR EACH ROW EXECUTE FUNCTION %s()`, triggerIdent, fnIdent)
	if _, err := pg.Exec(ctx, createTrigger); err != nil {
		_, _ = pg.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fnIdent))
		_, _ = pg.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, seqIdent))
		t.Fatalf("create settle serialization trigger: %v", err)
	}
	return seqRef, func() {
		_, _ = pg.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON billing_ledger_claims`, triggerIdent))
		_, _ = pg.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fnIdent))
		_, _ = pg.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, seqIdent))
	}
}

func installSettlerAbortEventSerializationFailure(t *testing.T, ctx context.Context, pg *pgxpool.Pool, claimID int64) (string, func()) {
	t.Helper()
	suffix := fmt.Sprintf("%d_%d", claimID, time.Now().UTC().UnixNano())
	seqName := "huakai_tx2_abort_seq_" + suffix
	fnName := "huakai_tx2_abort_fail_" + suffix
	triggerName := "huakai_tx2_abort_fail_" + suffix
	seqIdent := pgx.Identifier{"public", seqName}.Sanitize()
	fnIdent := pgx.Identifier{"public", fnName}.Sanitize()
	triggerIdent := pgx.Identifier{triggerName}.Sanitize()
	seqRef := "public." + seqName

	if _, err := pg.Exec(ctx, fmt.Sprintf(`CREATE SEQUENCE %s`, seqIdent)); err != nil {
		t.Fatalf("create abort serialization sequence: %v", err)
	}
	createFn := fmt.Sprintf(`
CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.claim_id = %d
		AND NEW.event_type = 'claim_aborted'
		AND nextval('%s'::regclass) = 1 THEN
		RAISE EXCEPTION 'forced abort Tx2 serialization conflict' USING ERRCODE = '40001';
	END IF;
	RETURN NEW;
END;
$$`, fnIdent, claimID, seqRef)
	if _, err := pg.Exec(ctx, createFn); err != nil {
		_, _ = pg.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, seqIdent))
		t.Fatalf("create abort serialization function: %v", err)
	}
	createTrigger := fmt.Sprintf(`
CREATE TRIGGER %s
BEFORE INSERT ON billing_events
FOR EACH ROW EXECUTE FUNCTION %s()`, triggerIdent, fnIdent)
	if _, err := pg.Exec(ctx, createTrigger); err != nil {
		_, _ = pg.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fnIdent))
		_, _ = pg.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, seqIdent))
		t.Fatalf("create abort serialization trigger: %v", err)
	}
	return seqRef, func() {
		_, _ = pg.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON billing_events`, triggerIdent))
		_, _ = pg.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fnIdent))
		_, _ = pg.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, seqIdent))
	}
}

func assertSequenceLastValue(t *testing.T, ctx context.Context, pg *pgxpool.Pool, seqRef string, want int64) {
	t.Helper()
	var got int64
	if err := pg.QueryRow(ctx, fmt.Sprintf(`SELECT last_value FROM %s`, seqRef)).Scan(&got); err != nil {
		t.Fatalf("read serialization sequence %s: %v", seqRef, err)
	}
	if got != want {
		t.Fatalf("serialization trigger fire count=%d want %d", got, want)
	}
}

func seedSettlerGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) settlerSeed {
	t.Helper()
	unique := fmt.Sprintf("%s-%s", suffix, uuid.NewString())
	tenantID, apiKeyID, userID := seedTenant(t, ctx, pool, unique)
	seed := settlerSeed{
		tenantID:         tenantID,
		apiKeyID:         apiKeyID,
		userID:           userID,
		acquisitionToken: uuid.New(),
		fingerprint:      "fingerprint-" + unique,
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM usage_record_dlq WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM usage_records WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
	})

	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		tenantID, "provider-"+unique, "Provider "+unique,
	).Scan(&seed.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "pool-"+unique,
	).Scan(&seed.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, seed.poolGroupID, "channel-"+unique,
	).Scan(&seed.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, in_flight_count)
		 VALUES ($1, $2, $3, $4, 'api_key', 2) RETURNING id`,
		tenantID, seed.providerID, seed.channelID, "account-"+unique,
	).Scan(&seed.providerAccountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, provider_account_id, acquisition_token,
			attempt_seq, predicted_cost, currency_code, lease_expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4.1-mini', $7,
			'1.0', 'standard', $8, $9,
			1, $10, 'USD', NOW() + interval '90 seconds'
		) RETURNING id`,
		tenantID, "idempotency-"+unique, seed.fingerprint, apiKeyID, userID,
		"logical-"+unique, seed.poolGroupID, seed.providerAccountID, seed.acquisitionToken,
		decimal.RequireFromString("0.01000000"),
	).Scan(&seed.claimID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO pool_slot_acquisitions (
			tenant_id, provider_account_id, acquisition_token, claim_id, attempt_seq, lease_expires_at
		) VALUES ($1, $2, $3, $4, 1, NOW() + interval '90 seconds')`,
		tenantID, seed.providerAccountID, seed.acquisitionToken, seed.claimID,
	); err != nil {
		t.Fatalf("seed pool slot acquisition: %v", err)
	}
	return seed
}

func settleRequest(seed settlerSeed, actualCost decimal.Decimal) SettleRequest {
	return SettleRequest{
		ClaimID:           seed.claimID,
		AccountID:         seed.providerAccountID,
		AcquisitionToken:  seed.acquisitionToken,
		ActualCost:        actualCost,
		TenantID:          seed.tenantID,
		APIKeyID:          seed.apiKeyID,
		UserID:            seed.userID,
		ProviderAccountID: seed.providerAccountID,
		AttemptSeq:        1,
		RequestedModel:    "gpt-4.1-mini",
		RequestedAt:       time.Now().UTC(),
		UpstreamModel:     "gpt-4.1-mini",
		Stream:            false,
		Fingerprint:       seed.fingerprint,
		SnapshotVersion:   "registry:99:7;router:v0.1-phase-c",
		Draft: gateway.UsageRecordDraft{
			TokensInput:           10,
			TokensOutput:          20,
			ActualCost:            actualCost,
			RoutingReason:         []byte(`{"route":"test"}`),
			EndClass:              gateway.StreamEndClass("non_streaming"),
			UsageSource:           gateway.UsageSourceReported,
			PendingReconciliation: false,
		},
	}
}
