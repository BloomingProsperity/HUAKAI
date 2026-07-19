//go:build integration_pg

package billing

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// 同幂等键只有在完整业务请求一致时才可重放；金额、原因或精确模式变化都必须
// 显式冲突，不能悄悄返回旧结果掩盖调用方错误。
// Mutation: 只按幂等键返回旧事件、不比较请求事实 -> 三个冲突子用例变绿失败。
func TestSettler_RefundIdempotentReplayRequiresSameRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-idem-stored")
	set := NewSettler(pool)
	settleCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))
	idempotencyKey := "refund-operation-" + uuid.NewString()
	request := RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        seed.claimID,
		AmountMicroUSD: 7_000,
		Reason:         "audit_mismatch",
		IdempotencyKey: idempotencyKey,
		AuditRequestID: "refund-trace-" + uuid.NewString(),
	}
	res1, err := set.Refund(ctx, request)
	if err != nil || res1.RefundMicroUSD != 7000 {
		t.Fatalf("first refund: res=%+v err=%v", res1, err)
	}
	replayRequest := request
	replayRequest.AuditRequestID = "retry-trace-" + uuid.NewString()
	res2, err := set.Refund(ctx, replayRequest)
	if err != nil {
		t.Fatalf("replay refund: %v", err)
	}
	if !res2.Idempotent {
		t.Fatal("replay must be Idempotent=true")
	}
	if res2.RefundMicroUSD != 7000 {
		t.Fatalf("replay refund micros=%d want 7000", res2.RefundMicroUSD)
	}

	tests := []struct {
		name   string
		mutate func(*RefundRequest)
	}{
		{name: "金额变化", mutate: func(req *RefundRequest) { req.AmountMicroUSD = 9_999 }},
		{name: "原因变化", mutate: func(req *RefundRequest) { req.Reason = "cost_dispute" }},
		{name: "精确模式变化", mutate: func(req *RefundRequest) { req.RequireExact = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conflict := request
			tt.mutate(&conflict)
			result, err := set.Refund(ctx, conflict)
			if !errors.Is(err, ErrRefundIdempotencyConflict) || result != nil {
				t.Fatalf("conflict result=%+v err=%v", result, err)
			}
		})
	}
	var balance decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if !balance.Equal(decimal.RequireFromString("9.98700000")) {
		t.Fatalf("balance=%s want 9.98700000", balance)
	}
	var eventCount, operationCount int
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2 AND event_type='reconciliation_appended' AND actual_cost_signed < 0),
    (SELECT count(*) FROM billing_refund_operations WHERE tenant_id=$1 AND idempotency_key=$3)`,
		seed.tenantID, seed.claimID, idempotencyKey).Scan(&eventCount, &operationCount); err != nil {
		t.Fatalf("read refund facts: %v", err)
	}
	if eventCount != 1 || operationCount != 1 {
		t.Fatalf("event_count=%d operation_count=%d want 1/1", eventCount, operationCount)
	}
}

func TestSettler_RefundIdempotencyKeyCannotMoveToAnotherClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-idem-cross-claim")
	set := NewSettler(pool)
	settleCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))

	key := "refund-cross-claim-" + uuid.NewString()
	first, err := set.Refund(ctx, RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        seed.claimID,
		AmountMicroUSD: 7_000,
		Reason:         "audit_mismatch",
		IdempotencyKey: key,
		AuditRequestID: "first-claim-" + uuid.NewString(),
	})
	if err != nil || first == nil || !first.BalanceCredited {
		t.Fatalf("first refund result=%+v err=%v", first, err)
	}

	secondClaim := seedAdditionalCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))
	var before decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&before); err != nil {
		t.Fatalf("read balance before conflict: %v", err)
	}
	conflict, err := set.Refund(ctx, RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        secondClaim.claimID,
		AmountMicroUSD: 7_000,
		Reason:         "audit_mismatch",
		IdempotencyKey: key,
		AuditRequestID: "second-claim-" + uuid.NewString(),
	})
	if !errors.Is(err, ErrRefundIdempotencyConflict) || conflict != nil {
		t.Fatalf("cross-claim conflict result=%+v err=%v", conflict, err)
	}
	var after decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&after); err != nil {
		t.Fatalf("read balance after conflict: %v", err)
	}
	if !after.Equal(before) {
		t.Fatalf("cross-claim conflict changed balance before=%s after=%s", before, after)
	}
	var secondEvents, operationCount int
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2 AND event_type='reconciliation_appended' AND actual_cost_signed < 0),
    (SELECT count(*) FROM billing_refund_operations WHERE tenant_id=$1 AND idempotency_key=$3)`,
		seed.tenantID, secondClaim.claimID, key).Scan(&secondEvents, &operationCount); err != nil {
		t.Fatalf("read cross-claim refund facts: %v", err)
	}
	if secondEvents != 0 || operationCount != 1 {
		t.Fatalf("second_events=%d operation_count=%d want 0/1", secondEvents, operationCount)
	}
}

func TestSettler_ConcurrentSameRefundRequestHasOneMoneyEffect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-idem-concurrent-same-key")
	set := NewSettler(pool)
	settleCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))

	request := RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        seed.claimID,
		AmountMicroUSD: 20_000,
		Reason:         "audit_mismatch",
		IdempotencyKey: "refund-concurrent-same-" + uuid.NewString(),
		AuditRequestID: "refund-concurrent-trace-" + uuid.NewString(),
		RequireExact:   true,
	}
	type outcome struct {
		result *RefundResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := set.Refund(ctx, request)
			outcomes <- outcome{result: res, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	var credited, replayed int
	for got := range outcomes {
		if got.err != nil || got.result == nil {
			t.Fatalf("concurrent same-key refund result=%+v err=%v", got.result, got.err)
		}
		if got.result.RefundMicroUSD != 20_000 || got.result.CoveredMicroUSD != 20_000 {
			t.Fatalf("concurrent same-key result=%+v", got.result)
		}
		if got.result.BalanceCredited {
			credited++
		}
		if got.result.Idempotent {
			replayed++
		}
	}
	if credited != 1 || replayed != 1 {
		t.Fatalf("credited=%d replayed=%d want 1/1", credited, replayed)
	}
	var balance decimal.Decimal
	var eventCount, operationCount int
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2),
    (SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$3 AND event_type='reconciliation_appended' AND actual_cost_signed < 0),
    (SELECT count(*) FROM billing_refund_operations WHERE tenant_id=$1 AND idempotency_key=$4)`,
		seed.tenantID, seed.userID, seed.claimID, request.IdempotencyKey).Scan(&balance, &eventCount, &operationCount); err != nil {
		t.Fatalf("read concurrent same-key facts: %v", err)
	}
	if !balance.Equal(decimal.RequireFromString("10.00000000")) || eventCount != 1 || operationCount != 1 {
		t.Fatalf("balance=%s events=%d operations=%d want 10/1/1", balance, eventCount, operationCount)
	}
}

func TestSettler_ZeroRefundPersistsStableIdempotencyFact(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-idem-zero")
	set := NewSettler(pool)
	settleCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))

	request := RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        seed.claimID,
		AmountMicroUSD: 0,
		Reason:         "audit_mismatch",
		IdempotencyKey: "refund-zero-" + uuid.NewString(),
		AuditRequestID: "refund-zero-trace-" + uuid.NewString(),
	}
	first, err := set.Refund(ctx, request)
	if err != nil || first == nil || first.AdjustmentRef != RefundSkippedAmountZeroRef || first.Idempotent {
		t.Fatalf("first zero refund result=%+v err=%v", first, err)
	}
	replay, err := set.Refund(ctx, request)
	if err != nil || replay == nil || !replay.Idempotent || replay.AdjustmentRef != RefundSkippedAmountZeroRef {
		t.Fatalf("zero replay result=%+v err=%v", replay, err)
	}
	changed := request
	changed.AmountMicroUSD = 1
	conflict, err := set.Refund(ctx, changed)
	if !errors.Is(err, ErrRefundIdempotencyConflict) || conflict != nil {
		t.Fatalf("changed zero request result=%+v err=%v", conflict, err)
	}
	var outcome string
	var operationCount, eventCount int
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT outcome FROM billing_refund_operations WHERE tenant_id=$1 AND idempotency_key=$2),
    (SELECT count(*) FROM billing_refund_operations WHERE tenant_id=$1 AND idempotency_key=$2),
    (SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$3 AND event_type='reconciliation_appended' AND actual_cost_signed < 0)`,
		seed.tenantID, request.IdempotencyKey, seed.claimID).Scan(&outcome, &operationCount, &eventCount); err != nil {
		t.Fatalf("read zero refund fact: %v", err)
	}
	if outcome != refundOutcomeSkippedZero || operationCount != 1 || eventCount != 0 {
		t.Fatalf("outcome=%q operations=%d events=%d", outcome, operationCount, eventCount)
	}
}

func TestSettler_LegacyRefundWithoutRequestFactRequiresManualDisambiguation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-idem-legacy")
	set := NewSettler(pool)
	settleCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))

	auditID := "legacy-refund-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
INSERT INTO billing_events (
    tenant_id, claim_id, event_type, actual_cost, actual_cost_signed,
    stream_state, delivered_token_count, fingerprint, audit_request_id
) VALUES ($1, $2, 'reconciliation_appended', 0, -0.007, 2, 0, $3, $4)`,
		seed.tenantID, seed.claimID, seed.fingerprint, auditID); err != nil {
		t.Fatalf("seed legacy refund event: %v", err)
	}
	result, err := set.Refund(ctx, RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        seed.claimID,
		AmountMicroUSD: 7_000,
		Reason:         "audit_mismatch",
		IdempotencyKey: "legacy-operation-" + uuid.NewString(),
		AuditRequestID: auditID,
	})
	if !errors.Is(err, ErrRefundIdempotencyConflict) || result != nil {
		t.Fatalf("legacy ambiguity result=%+v err=%v", result, err)
	}
	var operations, events int
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM billing_refund_operations WHERE tenant_id=$1 AND claim_id=$2),
    (SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2 AND event_type='reconciliation_appended' AND actual_cost_signed < 0)`,
		seed.tenantID, seed.claimID).Scan(&operations, &events); err != nil {
		t.Fatalf("read legacy ambiguity facts: %v", err)
	}
	if operations != 0 || events != 1 {
		t.Fatalf("operations=%d events=%d want 0/1", operations, events)
	}
}

func TestSettler_UnrelatedLegacyAdjustmentSharingAuditTraceDoesNotBlockRefund(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-idem-unrelated-legacy")
	set := NewSettler(pool)
	settleCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))

	auditID := "shared-unrelated-trace-" + uuid.NewString()
	var unrelatedClaimID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO billing_ledger_claims (
    tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
    logical_request_id, endpoint_family, requested_model, pooling_group_id,
    billing_policy_version, request_class, provider_account_id, acquisition_token,
    attempt_seq, predicted_cost, currency_code, lease_expires_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, 'chat', 'gpt-4.1-mini', $7,
    '1.0', 'standard', $8, $9,
    1, 0.001, 'USD', NOW() + interval '90 seconds'
) RETURNING id`,
		seed.tenantID,
		"unrelated-idempotency-"+uuid.NewString(),
		seed.fingerprint+"-unrelated",
		seed.apiKeyID,
		seed.userID,
		"unrelated-logical-"+uuid.NewString(),
		seed.poolGroupID,
		seed.providerAccountID,
		uuid.New(),
	).Scan(&unrelatedClaimID); err != nil {
		t.Fatalf("seed unrelated claim: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO billing_events (
    tenant_id, claim_id, event_type, actual_cost, actual_cost_signed,
    stream_state, delivered_token_count, fingerprint, audit_request_id
) VALUES ($1, $2, 'reconciliation_appended', 0, -0.001, 2, 0, $3, $4)`,
		seed.tenantID, unrelatedClaimID, seed.fingerprint+"-unrelated", auditID); err != nil {
		t.Fatalf("seed unrelated legacy adjustment: %v", err)
	}
	result, err := set.Refund(ctx, RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        seed.claimID,
		AmountMicroUSD: 7_000,
		Reason:         "audit_mismatch",
		IdempotencyKey: "unrelated-legacy-operation-" + uuid.NewString(),
		AuditRequestID: auditID,
	})
	if err != nil || result == nil || result.RefundMicroUSD != 7_000 {
		t.Fatalf("refund sharing unrelated audit trace result=%+v err=%v", result, err)
	}
	var operations, claimRefunds, unrelatedAdjustments int
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM billing_refund_operations WHERE tenant_id=$1 AND claim_id=$2),
    (SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2
        AND event_type='reconciliation_appended' AND actual_cost_signed < 0),
	    (SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$3
	        AND event_type='reconciliation_appended' AND audit_request_id=$4 AND actual_cost_signed < 0)`,
		seed.tenantID, seed.claimID, unrelatedClaimID, auditID).Scan(&operations, &claimRefunds, &unrelatedAdjustments); err != nil {
		t.Fatalf("read unrelated trace facts: %v", err)
	}
	if operations != 1 || claimRefunds != 1 || unrelatedAdjustments != 1 {
		t.Fatalf("operations=%d claim_refunds=%d unrelated=%d want 1/1/1", operations, claimRefunds, unrelatedAdjustments)
	}
}

func TestSettler_AuditRequestIDDoesNotActAsIdempotencyKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-audit-trace-not-key")
	set := NewSettler(pool)
	settleCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))

	auditID := "shared-refund-trace-" + uuid.NewString()
	first, err := set.Refund(ctx, RefundRequest{
		TenantID: seed.tenantID, ClaimID: seed.claimID, AmountMicroUSD: 7_000,
		Reason: "audit_mismatch", IdempotencyKey: "first-operation-" + uuid.NewString(), AuditRequestID: auditID,
	})
	if err != nil || first == nil || first.RefundMicroUSD != 7_000 {
		t.Fatalf("first refund result=%+v err=%v", first, err)
	}
	second, err := set.Refund(ctx, RefundRequest{
		TenantID: seed.tenantID, ClaimID: seed.claimID, AmountMicroUSD: 5_000,
		Reason: "cost_dispute", IdempotencyKey: "second-operation-" + uuid.NewString(), AuditRequestID: auditID,
	})
	if err != nil || second == nil || second.RefundMicroUSD != 5_000 {
		t.Fatalf("second refund sharing audit trace result=%+v err=%v", second, err)
	}
	var operations, events int
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM billing_refund_operations WHERE tenant_id=$1 AND claim_id=$2),
    (SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2
        AND event_type='reconciliation_appended' AND audit_request_id=$3 AND actual_cost_signed < 0)`,
		seed.tenantID, seed.claimID, auditID).Scan(&operations, &events); err != nil {
		t.Fatalf("read shared trace facts: %v", err)
	}
	if operations != 2 || events != 2 {
		t.Fatalf("operations=%d events=%d want 2/2", operations, events)
	}
}

func TestSettler_RefundRequiresSeparateAuditRequestID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-audit-trace-required")
	set := NewSettler(pool)
	settleCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))

	result, err := set.Refund(ctx, RefundRequest{
		TenantID: seed.tenantID, ClaimID: seed.claimID, AmountMicroUSD: 7_000,
		Reason: "audit_mismatch", IdempotencyKey: "missing-audit-trace-" + uuid.NewString(),
	})
	if err == nil || result != nil || !strings.Contains(err.Error(), "invalid refund audit request id") {
		t.Fatalf("missing audit trace result=%+v err=%v", result, err)
	}
	var operations, events int
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM billing_refund_operations WHERE tenant_id=$1 AND claim_id=$2),
    (SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2
        AND event_type='reconciliation_appended' AND actual_cost_signed < 0)`,
		seed.tenantID, seed.claimID).Scan(&operations, &events); err != nil {
		t.Fatalf("read missing trace facts: %v", err)
	}
	if operations != 0 || events != 0 {
		t.Fatalf("operations=%d events=%d want 0/0", operations, events)
	}
}

func TestSettler_RefundReplayRejectsFactLinkedToNonRefundEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-invalid-event-link")
	set := NewSettler(pool)
	settleCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))

	request := RefundRequest{
		TenantID: seed.tenantID, ClaimID: seed.claimID, AmountMicroUSD: 7_000,
		Reason: "audit_mismatch", IdempotencyKey: "invalid-event-link-" + uuid.NewString(), AuditRequestID: uuid.NewString(),
	}
	var committedEventID int64
	if err := pool.QueryRow(ctx, `
SELECT id FROM billing_events
WHERE tenant_id=$1 AND claim_id=$2 AND event_type='claim_committed'
ORDER BY id ASC LIMIT 1`, seed.tenantID, seed.claimID).Scan(&committedEventID); err != nil {
		t.Fatalf("find committed event: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing_refund_operations (
			tenant_id, claim_id, idempotency_key, request_fingerprint,
			requested_amount_micro_usd, reason, require_exact,
			applied_amount_micro_usd, covered_amount_micro_usd, outcome, billing_event_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 7000, 7000, 'applied', $8)`,
		request.TenantID,
		request.ClaimID,
		request.IdempotencyKey,
		refundRequestFingerprint(request),
		request.AmountMicroUSD,
		request.Reason,
		request.RequireExact,
		committedEventID,
	); err != nil {
		t.Fatalf("seed invalid immutable refund fact: %v", err)
	}
	var balanceBefore decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&balanceBefore); err != nil {
		t.Fatalf("read balance before invalid replay: %v", err)
	}

	replay, err := set.Refund(ctx, request)
	if !errors.Is(err, ErrRefundFactInvalid) || replay != nil {
		t.Fatalf("invalid fact replay result=%+v err=%v", replay, err)
	}
	var balanceAfter decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&balanceAfter); err != nil {
		t.Fatalf("read balance after invalid replay: %v", err)
	}
	if !balanceAfter.Equal(balanceBefore) {
		t.Fatalf("invalid replay changed balance before=%s after=%s", balanceBefore, balanceAfter)
	}
	var refundEvents int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM billing_events
		WHERE tenant_id=$1 AND claim_id=$2
		  AND event_type='reconciliation_appended' AND actual_cost_signed < 0`,
		seed.tenantID, seed.claimID,
	).Scan(&refundEvents); err != nil {
		t.Fatalf("count refund events after invalid replay: %v", err)
	}
	if refundEvents != 0 {
		t.Fatalf("invalid replay appended %d refund events; want 0", refundEvents)
	}
}

func TestSettler_RefundCapsByCapturedAndOriginalCost(t *testing.T) {
	tests := []struct {
		name             string
		capturedOverride decimal.Decimal
		wantMicros       int64
		wantBalance      decimal.Decimal
	}{
		{name: "captured_lower", capturedOverride: decimal.RequireFromString("0.00700000"), wantMicros: 7000, wantBalance: decimal.RequireFromString("9.98700000")},
		{name: "original_lower", capturedOverride: decimal.RequireFromString("0.03000000"), wantMicros: 20000, wantBalance: decimal.RequireFromString("10.00000000")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			pool := openPool(t, ctx)
			seed := seedSettlerGraph(t, ctx, pool, "refund-cap-"+tt.name)
			set := NewSettler(pool)
			settleCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))
			if _, err := pool.Exec(ctx, `UPDATE balance_holds SET captured=$2 WHERE claim_id=$1`, seed.claimID, tt.capturedOverride); err != nil {
				t.Fatalf("override captured evidence: %v", err)
			}

			res, err := set.Refund(ctx, RefundRequest{
				TenantID:       seed.tenantID,
				ClaimID:        seed.claimID,
				AmountMicroUSD: 50_000,
				Reason:         "audit_mismatch",
				IdempotencyKey: uuid.NewString(),
				AuditRequestID: uuid.NewString(),
			})
			if err != nil {
				t.Fatalf("Refund: %v", err)
			}
			if res.RefundMicroUSD != tt.wantMicros || !res.BalanceCredited || res.BillingEventID == 0 {
				t.Fatalf("refund=%+v want micros=%d credited event", res, tt.wantMicros)
			}
			var balance decimal.Decimal
			if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&balance); err != nil {
				t.Fatalf("read balance: %v", err)
			}
			if !balance.Equal(tt.wantBalance) {
				t.Fatalf("balance=%s want %s", balance, tt.wantBalance)
			}
		})
	}
}

func TestSettler_RefundCumulativeAmountCannotExceedCapturedCost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-cumulative-cap")
	set := NewSettler(pool)
	settleCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))

	first, err := set.Refund(ctx, RefundRequest{TenantID: seed.tenantID, ClaimID: seed.claimID, AmountMicroUSD: 7000, Reason: "audit_mismatch", IdempotencyKey: uuid.NewString(), AuditRequestID: uuid.NewString()})
	if err != nil || first.RefundMicroUSD != 7000 {
		t.Fatalf("first refund: result=%+v err=%v", first, err)
	}
	second, err := set.Refund(ctx, RefundRequest{TenantID: seed.tenantID, ClaimID: seed.claimID, AmountMicroUSD: 20_000, Reason: "audit_mismatch", IdempotencyKey: uuid.NewString(), AuditRequestID: uuid.NewString()})
	if err != nil || second.RefundMicroUSD != 13_000 {
		t.Fatalf("second capped refund: result=%+v err=%v", second, err)
	}
	third, err := set.Refund(ctx, RefundRequest{TenantID: seed.tenantID, ClaimID: seed.claimID, AmountMicroUSD: 1, Reason: "audit_mismatch", IdempotencyKey: uuid.NewString(), AuditRequestID: uuid.NewString()})
	if err != nil {
		t.Fatalf("third refund: %v", err)
	}
	if third.RefundMicroUSD != 0 || third.BalanceCredited || !third.AlreadySatisfied ||
		third.BillingEventID == 0 || third.AdjustmentRef == "" {
		t.Fatalf("exhausted refund cap must link prior adjustment: %+v", third)
	}
	var (
		balance   decimal.Decimal
		refundSum decimal.Decimal
		count     int
	)
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(-actual_cost_signed), 0), count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2 AND event_type='reconciliation_appended' AND actual_cost_signed < 0`, seed.tenantID, seed.claimID).Scan(&refundSum, &count); err != nil {
		t.Fatalf("read refund events: %v", err)
	}
	if !balance.Equal(decimal.RequireFromString("10.00000000")) || !refundSum.Equal(decimal.RequireFromString("0.02000000")) || count != 2 {
		t.Fatalf("cumulative refund balance=%s sum=%s count=%d", balance, refundSum, count)
	}
}

func TestSettler_ExactRefundTargetsCumulativeCoverage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-exact-coverage")
	set := NewSettler(pool)
	settleCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))

	prior, err := set.Refund(ctx, RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        seed.claimID,
		AmountMicroUSD: 5_000,
		Reason:         "cost_dispute",
		IdempotencyKey: "prior-refund-" + uuid.NewString(),
		AuditRequestID: "prior-adjustment-" + uuid.NewString(),
	})
	if err != nil || prior.RefundMicroUSD != 5_000 || prior.CoveredMicroUSD != 5_000 {
		t.Fatalf("prior refund result=%+v err=%v", prior, err)
	}

	exactAuditID := "exact-adjustment-" + uuid.NewString()
	exactIdempotencyKey := "exact-refund-" + uuid.NewString()
	exact, err := set.Refund(ctx, RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        seed.claimID,
		AmountMicroUSD: 8_000,
		Reason:         "audit_mismatch",
		IdempotencyKey: exactIdempotencyKey,
		AuditRequestID: exactAuditID,
		RequireExact:   true,
	})
	if err != nil {
		t.Fatalf("exact refund: %v", err)
	}
	if exact.RefundMicroUSD != 3_000 || exact.CoveredMicroUSD != 8_000 ||
		!exact.BalanceCredited || exact.AlreadySatisfied {
		t.Fatalf("exact refund result=%+v want new=3000 covered=8000", exact)
	}

	replay, err := set.Refund(ctx, RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        seed.claimID,
		AmountMicroUSD: 8_000,
		Reason:         "audit_mismatch",
		IdempotencyKey: exactIdempotencyKey,
		AuditRequestID: exactAuditID,
		RequireExact:   true,
	})
	if err != nil || !replay.Idempotent || replay.RefundMicroUSD != 3_000 || replay.CoveredMicroUSD != 8_000 {
		t.Fatalf("exact replay result=%+v err=%v", replay, err)
	}

	tooLarge, err := set.Refund(ctx, RefundRequest{
		TenantID:       seed.tenantID,
		ClaimID:        seed.claimID,
		AmountMicroUSD: 21_000,
		Reason:         "audit_mismatch",
		IdempotencyKey: "oversized-refund-" + uuid.NewString(),
		AuditRequestID: "oversized-adjustment-" + uuid.NewString(),
		RequireExact:   true,
	})
	if !errors.Is(err, ErrRefundAmountNotCovered) || tooLarge != nil {
		t.Fatalf("oversized exact refund result=%+v err=%v", tooLarge, err)
	}

	var (
		balance   decimal.Decimal
		refundSum decimal.Decimal
		count     int
	)
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&balance); err != nil {
		t.Fatalf("read exact refund balance: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(-actual_cost_signed), 0), count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2 AND event_type='reconciliation_appended' AND actual_cost_signed < 0`, seed.tenantID, seed.claimID).Scan(&refundSum, &count); err != nil {
		t.Fatalf("read exact refund events: %v", err)
	}
	if !balance.Equal(decimal.RequireFromString("9.98800000")) || !refundSum.Equal(decimal.RequireFromString("0.00800000")) || count != 2 {
		t.Fatalf("exact refund balance=%s sum=%s count=%d", balance, refundSum, count)
	}
}

func TestSettler_ConcurrentRefundsConvergeWithoutOverCredit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "refund-concurrent-cap")
	set := NewSettler(pool)
	settleCapturedRefundClaim(t, ctx, pool, set, seed, decimal.RequireFromString("0.02000000"))

	type outcome struct {
		result *RefundResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := set.Refund(ctx, RefundRequest{
				TenantID:       seed.tenantID,
				ClaimID:        seed.claimID,
				AmountMicroUSD: 20_000,
				Reason:         "audit_mismatch",
				IdempotencyKey: uuid.NewString(),
				AuditRequestID: uuid.NewString(),
			})
			outcomes <- outcome{result: res, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	var total int64
	for got := range outcomes {
		if got.err != nil {
			t.Fatalf("concurrent refund: %v", got.err)
		}
		if got.result == nil {
			t.Fatal("concurrent refund returned nil result")
		}
		total += got.result.RefundMicroUSD
	}
	if total != 20_000 {
		t.Fatalf("concurrent refund total=%d want 20000", total)
	}
	var (
		balance   decimal.Decimal
		refundSum decimal.Decimal
		count     int
	)
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(-actual_cost_signed), 0), count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2 AND event_type='reconciliation_appended' AND actual_cost_signed < 0`, seed.tenantID, seed.claimID).Scan(&refundSum, &count); err != nil {
		t.Fatalf("read refund events: %v", err)
	}
	if !balance.Equal(decimal.RequireFromString("10.00000000")) || !refundSum.Equal(decimal.RequireFromString("0.02000000")) || count != 1 {
		t.Fatalf("concurrent refund balance=%s sum=%s count=%d", balance, refundSum, count)
	}
}

func settleCapturedRefundClaim(t *testing.T, ctx context.Context, pool *pgxpool.Pool, set *DefaultSettler, seed settlerSeed, actualCost decimal.Decimal) {
	t.Helper()
	if err := reserveAndCommitBalanceHold(ctx, t, pool, seed.tenantID, seed.userID, seed.claimID, actualCost); err != nil {
		t.Fatalf("reserve refund hold: %v", err)
	}
	if _, err := set.Settle(ctx, settleRequest(seed, actualCost)); err != nil {
		t.Fatalf("settle captured refund claim: %v", err)
	}
}

func seedAdditionalCapturedRefundClaim(t *testing.T, ctx context.Context, pool *pgxpool.Pool, set *DefaultSettler, base settlerSeed, actualCost decimal.Decimal) settlerSeed {
	t.Helper()
	suffix := uuid.NewString()
	next := base
	next.claimID = 0
	next.acquisitionToken = uuid.New()
	next.fingerprint = "fingerprint-additional-" + suffix
	if err := pool.QueryRow(ctx, `
INSERT INTO billing_ledger_claims (
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
		next.tenantID,
		"idempotency-additional-"+suffix,
		next.fingerprint,
		next.apiKeyID,
		next.userID,
		"logical-additional-"+suffix,
		next.poolGroupID,
		next.providerAccountID,
		next.acquisitionToken,
		decimal.RequireFromString("0.01000000"),
	).Scan(&next.claimID); err != nil {
		t.Fatalf("seed additional claim: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO pool_slot_acquisitions (
    tenant_id, provider_account_id, acquisition_token, claim_id, attempt_seq, lease_expires_at
) VALUES ($1, $2, $3, $4, 1, NOW() + interval '90 seconds')`,
		next.tenantID, next.providerAccountID, next.acquisitionToken, next.claimID); err != nil {
		t.Fatalf("seed additional pool slot: %v", err)
	}
	settleCapturedRefundClaim(t, ctx, pool, set, next, actualCost)
	return next
}
