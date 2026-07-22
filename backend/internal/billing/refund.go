package billing

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	maxCostMicroUSDInt64          int64 = 1<<63 - 1
	refundIdempotencyKeyMaxLength       = 256
	refundReasonMaxLength               = 512
	refundOutcomeApplied                = "applied"
	refundOutcomeAlreadySatisfied       = "already_satisfied"
	refundOutcomeSkippedZero            = "skipped_zero"
	RefundSkippedAmountZeroRef          = "refund_skipped_amount_zero"
)

var maxCostMicroUSDDecimal = decimal.NewFromInt(maxCostMicroUSDInt64)

func (s *DefaultSettler) Refund(ctx context.Context, req RefundRequest) (*RefundResult, error) {
	if s == nil || s.pool == nil {
		return nil, ErrPoolNotConfigured
	}
	var res *RefundResult
	err := retryTx2(ctx, "refund", settleTx2RetryPolicy, func(ctx context.Context) error {
		next, err := s.refundOnce(ctx, req)
		if err != nil {
			return err
		}
		res = next
		return nil
	}, nil, nil)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// VerifyRefundableCharge 在创建异步退款任务前确认 claim 确实存在可回补的余额扣款证据。
// worker 执行时仍会在资金事务内重新锁定并校验，避免检查与执行之间的状态变化。
func (s *DefaultSettler) VerifyRefundableCharge(ctx context.Context, req RefundRequest) error {
	if s == nil || s.pool == nil {
		return ErrPoolNotConfigured
	}
	if req.TenantID <= 0 || req.ClaimID <= 0 || req.AmountMicroUSD <= 0 {
		return fmt.Errorf("billing: invalid refund eligibility request")
	}
	var refundableCost decimal.Decimal
	err := s.pool.QueryRow(ctx, `
SELECT LEAST(claim.actual_cost, hold.captured)
FROM billing_ledger_claims AS claim
JOIN balance_holds AS hold
  ON hold.claim_id = claim.id
 AND hold.tenant_id = claim.tenant_id
 AND hold.user_id = claim.user_id
JOIN user_balances AS balance
  ON balance.tenant_id = claim.tenant_id
 AND balance.user_id = claim.user_id
WHERE claim.tenant_id = $1
  AND claim.id = $2
  AND claim.status = 'committed'
  AND claim.billing_effect = 'user_charge'
  AND claim.actual_cost > 0
  AND hold.state = 'captured'
  AND hold.captured > 0`, req.TenantID, req.ClaimID).Scan(&refundableCost)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRefundNoCapturedCharge
		}
		return fmt.Errorf("billing: verify refundable captured charge: %w", err)
	}
	refundableMicros, err := costUSDToMicros(refundableCost)
	if err != nil {
		return err
	}
	if req.AmountMicroUSD > refundableMicros {
		return ErrRefundAmountNotCovered
	}
	return nil
}

// refundOnce 包含一个完整退款事务，供外层在 Serializable 冲突时整体重跑。
// 每轮都重新获取 claim 与 hold 锁、重新计算累计退款上限，避免只重放部分写入。
func (s *DefaultSettler) refundOnce(ctx context.Context, req RefundRequest) (*RefundResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("billing: begin refund Tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	res, err := s.RefundInTx(ctx, tx, req)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("billing: commit refund Tx: %w", err)
	}
	return res, nil
}

func (s *DefaultSettler) RefundInTx(ctx context.Context, tx pgx.Tx, req RefundRequest) (*RefundResult, error) {
	if s == nil || s.q == nil {
		return nil, ErrPoolNotConfigured
	}
	if tx == nil {
		return nil, fmt.Errorf("billing: refund tx required")
	}
	var requestFingerprint string
	req, requestFingerprint, err := normalizeRefundRequest(req)
	if err != nil {
		return nil, err
	}
	if err := lockRefundIdempotencyKey(ctx, tx, req.TenantID, req.IdempotencyKey); err != nil {
		return nil, err
	}
	if existing, found, err := getRefundOperationByKey(ctx, tx, req.TenantID, req.IdempotencyKey); err != nil {
		return nil, err
	} else if found {
		if !existing.matches(req, requestFingerprint) {
			return nil, ErrRefundIdempotencyConflict
		}
		return existing.result()
	}

	// 幂等键锁固定在 claim 行锁之前；同键和同 claim 的并发请求都按同一顺序收敛。
	var (
		fingerprint  string
		status       string
		actualCost   decimal.Decimal
		userID       int64
		storedEffect string
	)
	if err := tx.QueryRow(ctx, `
SELECT request_fingerprint, status, actual_cost, user_id, billing_effect
FROM billing_ledger_claims
WHERE tenant_id = $1 AND id = $2
FOR UPDATE`, req.TenantID, req.ClaimID).Scan(&fingerprint, &status, &actualCost, &userID, &storedEffect); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrClaimNotReserving
		}
		return nil, fmt.Errorf("billing: get claim for refund: %w", err)
	}

	// 旧版本只在 billing_events 留审计号，没有保存完整请求合同。遇到这类事实时
	// 不猜测“可能是同一请求”，显式冲突并交给运营核验，避免二次回补。
	var legacyEventID int64
	if err := tx.QueryRow(ctx, `
SELECT event.id
FROM billing_events AS event
LEFT JOIN billing_refund_operations AS operation
  ON operation.tenant_id = event.tenant_id
 AND operation.billing_event_id = event.id
WHERE event.tenant_id = $1
	  AND event.claim_id = $3
	  AND event.event_type = 'reconciliation_appended'
	  AND event.audit_request_id = $2
  AND event.actual_cost_signed < 0
  AND operation.id IS NULL
ORDER BY event.id ASC
LIMIT 1`, req.TenantID, req.AuditRequestID, req.ClaimID).Scan(&legacyEventID); err == nil {
		return nil, fmt.Errorf("%w: legacy billing event %d has no request fact", ErrRefundIdempotencyConflict, legacyEventID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("billing: lookup legacy refund event: %w", err)
	}

	if status != "committed" {
		return nil, ErrClaimNotReserving
	}
	billingEffect, err := NormalizeBillingEffect(BillingEffect(storedEffect))
	if err != nil {
		return nil, fmt.Errorf("billing: stored billing effect for refund claim %d: %w", req.ClaimID, err)
	}
	if billingEffect != BillingEffectUserCharge {
		return nil, ErrRefundNoCapturedCharge
	}
	refundMicros := req.AmountMicroUSD
	if refundMicros == 0 {
		if err := insertRefundOperation(ctx, tx, req, requestFingerprint, refundOutcomeSkippedZero, 0, 0, 0); err != nil {
			return nil, err
		}
		return zeroRefundResult(), nil
	}
	hold, err := s.q.WithTx(tx).GetBalanceHoldForUpdate(ctx, req.ClaimID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRefundNoCapturedCharge
		}
		return nil, fmt.Errorf("billing: get captured hold for refund: %w", err)
	}
	if hold.TenantID != req.TenantID || hold.UserID != userID {
		return nil, fmt.Errorf("billing: refund hold identity mismatch")
	}
	if hold.State != "captured" || !hold.Captured.IsPositive() {
		return nil, ErrRefundNoCapturedCharge
	}

	originalMicros, err := costUSDToMicros(actualCost)
	if err != nil {
		return nil, err
	}
	capturedMicros, err := costUSDToMicros(hold.Captured)
	if err != nil {
		return nil, err
	}
	if capturedMicros < originalMicros {
		originalMicros = capturedMicros
	}
	var (
		alreadyRefundedMicros int64
		latestRefundEventID   int64
	)
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(SUM(ROUND(-actual_cost_signed * 1000000)), 0)::bigint,
       COALESCE(MAX(id), 0)::bigint
FROM billing_events
WHERE tenant_id = $1 AND claim_id = $2 AND event_type = 'reconciliation_appended'
  AND actual_cost_signed < 0`, req.TenantID, req.ClaimID).Scan(&alreadyRefundedMicros, &latestRefundEventID); err != nil {
		return nil, fmt.Errorf("billing: sum prior reconciliation refunds: %w", err)
	}
	remainingMicros := originalMicros - alreadyRefundedMicros
	if remainingMicros < 0 {
		remainingMicros = 0
	}
	if req.RequireExact {
		if req.AmountMicroUSD > originalMicros {
			return nil, ErrRefundAmountNotCovered
		}
		if alreadyRefundedMicros >= req.AmountMicroUSD {
			if latestRefundEventID <= 0 {
				return nil, ErrRefundAmountNotCovered
			}
			if err := insertRefundOperation(ctx, tx, req, requestFingerprint, refundOutcomeAlreadySatisfied, 0, alreadyRefundedMicros, latestRefundEventID); err != nil {
				return nil, err
			}
			return &RefundResult{
				BillingEventID:   latestRefundEventID,
				AdjustmentRef:    billingAdjustmentRef(latestRefundEventID),
				CoveredMicroUSD:  alreadyRefundedMicros,
				AlreadySatisfied: true,
			}, nil
		}
		refundMicros = req.AmountMicroUSD - alreadyRefundedMicros
		if refundMicros <= 0 || refundMicros > remainingMicros {
			return nil, ErrRefundAmountNotCovered
		}
	} else {
		if refundMicros > remainingMicros {
			refundMicros = remainingMicros
		}
		if refundMicros == 0 {
			if latestRefundEventID > 0 && alreadyRefundedMicros >= originalMicros && originalMicros > 0 {
				if err := insertRefundOperation(ctx, tx, req, requestFingerprint, refundOutcomeAlreadySatisfied, 0, alreadyRefundedMicros, latestRefundEventID); err != nil {
					return nil, err
				}
				return &RefundResult{
					BillingEventID:   latestRefundEventID,
					AdjustmentRef:    billingAdjustmentRef(latestRefundEventID),
					CoveredMicroUSD:  alreadyRefundedMicros,
					AlreadySatisfied: true,
				}, nil
			}
			return nil, ErrRefundAmountNotCovered
		}
	}

	refundUSD := decimal.NewFromInt(refundMicros).Div(decimal.NewFromInt(1_000_000))
	refundEndClass := req.Reason
	refundUsageSource := req.Reason
	refundEventParams := dbbilling.InsertBillingEventParams{
		TenantID:         req.TenantID,
		ClaimID:          nullableInt64(req.ClaimID),
		EventType:        "reconciliation_appended",
		ActualCost:       decimal.Zero,
		ActualCostSigned: refundUSD.Neg(),
		EndClass:         &refundEndClass,
		UsageSource:      &refundUsageSource,
		StreamState:      StreamStatePartial.DBValue(),
		Fingerprint:      fingerprint,
		AuditRequestID:   nullableString(req.AuditRequestID),
		BillingEffect:    string(BillingEffectUserCharge),
	}
	row, err := s.q.WithTx(tx).InsertBillingEvent(ctx, refundEventParams)
	if err != nil {
		return nil, fmt.Errorf("billing: insert refund event: %w", err)
	}
	creditTag, err := tx.Exec(ctx,
		`UPDATE user_balances
		 SET balance = balance + $1, version = version + 1, updated_at = now()
		 WHERE tenant_id = $2 AND user_id = $3`,
		refundUSD, req.TenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("billing: credit refund to user balance: %w", err)
	}
	if creditTag.RowsAffected() != 1 {
		return nil, ErrRefundBalanceRowMissing
	}
	if err := s.enqueueBillingEventReplica(ctx, tx, row, refundEventParams); err != nil {
		return nil, err
	}
	coveredMicros := addCostMicrosCapped(alreadyRefundedMicros, refundMicros)
	if err := insertRefundOperation(ctx, tx, req, requestFingerprint, refundOutcomeApplied, refundMicros, coveredMicros, row.ID); err != nil {
		return nil, err
	}
	return &RefundResult{
		RefundMicroUSD:  refundMicros,
		BillingEventID:  row.ID,
		AdjustmentRef:   billingAdjustmentRef(row.ID),
		CoveredMicroUSD: coveredMicros,
		BalanceCredited: true,
	}, nil
}

type storedRefundOperation struct {
	ClaimID                 int64
	RequestFingerprint      string
	RequestedAmountMicroUSD int64
	Reason                  string
	RequireExact            bool
	AppliedAmountMicroUSD   int64
	CoveredAmountMicroUSD   int64
	Outcome                 string
	BillingEventID          sql.NullInt64
	BillingEventClaimID     sql.NullInt64
	BillingEventType        sql.NullString
	BillingEventAmount      sql.NullString
}

func normalizeRefundRequest(req RefundRequest) (RefundRequest, string, error) {
	if req.TenantID <= 0 || req.ClaimID <= 0 || req.AmountMicroUSD < 0 {
		return RefundRequest{}, "", fmt.Errorf("billing: invalid refund request")
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > refundIdempotencyKeyMaxLength {
		return RefundRequest{}, "", fmt.Errorf("billing: invalid refund idempotency key")
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		req.Reason = "refund"
	}
	if len(req.Reason) > refundReasonMaxLength {
		return RefundRequest{}, "", fmt.Errorf("billing: invalid refund reason")
	}
	req.AuditRequestID = strings.TrimSpace(req.AuditRequestID)
	if req.AuditRequestID == "" {
		return RefundRequest{}, "", fmt.Errorf("billing: invalid refund audit request id")
	}
	return req, refundRequestFingerprint(req), nil
}

func refundRequestFingerprint(req RefundRequest) string {
	canonical := struct {
		Version        string `json:"version"`
		TenantID       int64  `json:"tenant_id"`
		ClaimID        int64  `json:"claim_id"`
		AmountMicroUSD int64  `json:"amount_micro_usd"`
		Reason         string `json:"reason"`
		RequireExact   bool   `json:"require_exact"`
	}{
		Version:        "billing-refund-request/v1",
		TenantID:       req.TenantID,
		ClaimID:        req.ClaimID,
		AmountMicroUSD: req.AmountMicroUSD,
		Reason:         req.Reason,
		RequireExact:   req.RequireExact,
	}
	raw, _ := json.Marshal(canonical)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func lockRefundIdempotencyKey(ctx context.Context, tx pgx.Tx, tenantID int64, key string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		fmt.Sprintf("billing-refund:%d", tenantID), key); err != nil {
		return fmt.Errorf("billing: lock refund idempotency key: %w", err)
	}
	return nil
}

func getRefundOperationByKey(ctx context.Context, tx pgx.Tx, tenantID int64, key string) (storedRefundOperation, bool, error) {
	var op storedRefundOperation
	err := tx.QueryRow(ctx, `
SELECT operation.claim_id, operation.request_fingerprint, operation.requested_amount_micro_usd,
       operation.reason, operation.require_exact, operation.applied_amount_micro_usd,
       operation.covered_amount_micro_usd, operation.outcome, operation.billing_event_id,
       event.claim_id, event.event_type, event.actual_cost_signed::text
FROM billing_refund_operations AS operation
LEFT JOIN billing_events AS event
  ON event.tenant_id = operation.tenant_id
 AND event.claim_id = operation.claim_id
 AND event.id = operation.billing_event_id
WHERE operation.tenant_id = $1 AND operation.idempotency_key = $2`, tenantID, key).Scan(
		&op.ClaimID,
		&op.RequestFingerprint,
		&op.RequestedAmountMicroUSD,
		&op.Reason,
		&op.RequireExact,
		&op.AppliedAmountMicroUSD,
		&op.CoveredAmountMicroUSD,
		&op.Outcome,
		&op.BillingEventID,
		&op.BillingEventClaimID,
		&op.BillingEventType,
		&op.BillingEventAmount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedRefundOperation{}, false, nil
	}
	if err != nil {
		return storedRefundOperation{}, false, fmt.Errorf("billing: lookup refund operation: %w", err)
	}
	return op, true, nil
}

func (op storedRefundOperation) matches(req RefundRequest, fingerprint string) bool {
	return op.ClaimID == req.ClaimID &&
		op.RequestFingerprint == fingerprint &&
		op.RequestedAmountMicroUSD == req.AmountMicroUSD &&
		op.Reason == req.Reason &&
		op.RequireExact == req.RequireExact
}

func (op storedRefundOperation) result() (*RefundResult, error) {
	result := &RefundResult{
		RefundMicroUSD:  op.AppliedAmountMicroUSD,
		CoveredMicroUSD: op.CoveredAmountMicroUSD,
		Idempotent:      true,
	}
	switch op.Outcome {
	case refundOutcomeApplied:
		eventMicros, valid := op.validRefundEvent()
		if op.AppliedAmountMicroUSD <= 0 || !valid || eventMicros != op.AppliedAmountMicroUSD ||
			op.CoveredAmountMicroUSD < op.AppliedAmountMicroUSD {
			return nil, ErrRefundFactInvalid
		}
		result.BillingEventID = op.BillingEventID.Int64
		result.AdjustmentRef = billingAdjustmentRef(op.BillingEventID.Int64)
	case refundOutcomeAlreadySatisfied:
		eventMicros, valid := op.validRefundEvent()
		if op.AppliedAmountMicroUSD != 0 || op.CoveredAmountMicroUSD <= 0 || !valid ||
			eventMicros <= 0 || eventMicros > op.CoveredAmountMicroUSD {
			return nil, ErrRefundFactInvalid
		}
		result.BillingEventID = op.BillingEventID.Int64
		result.AdjustmentRef = billingAdjustmentRef(op.BillingEventID.Int64)
		result.AlreadySatisfied = true
	case refundOutcomeSkippedZero:
		if op.AppliedAmountMicroUSD != 0 || op.CoveredAmountMicroUSD != 0 || op.BillingEventID.Valid ||
			op.BillingEventClaimID.Valid || op.BillingEventType.Valid || op.BillingEventAmount.Valid {
			return nil, ErrRefundFactInvalid
		}
		result.AdjustmentRef = RefundSkippedAmountZeroRef
	default:
		return nil, ErrRefundFactInvalid
	}
	return result, nil
}

func (op storedRefundOperation) validRefundEvent() (int64, bool) {
	if !op.BillingEventID.Valid || op.BillingEventID.Int64 <= 0 ||
		!op.BillingEventClaimID.Valid || op.BillingEventClaimID.Int64 != op.ClaimID ||
		!op.BillingEventType.Valid || op.BillingEventType.String != "reconciliation_appended" ||
		!op.BillingEventAmount.Valid {
		return 0, false
	}
	signed, err := decimal.NewFromString(op.BillingEventAmount.String)
	if err != nil || !signed.IsNegative() {
		return 0, false
	}
	micros, err := costUSDToMicros(signed.Neg())
	return micros, err == nil && micros > 0
}

func insertRefundOperation(ctx context.Context, tx pgx.Tx, req RefundRequest, fingerprint, outcome string, appliedMicros, coveredMicros, billingEventID int64) error {
	_, err := tx.Exec(ctx, `
INSERT INTO billing_refund_operations (
    tenant_id, claim_id, idempotency_key, request_fingerprint,
    requested_amount_micro_usd, reason, require_exact,
    applied_amount_micro_usd, covered_amount_micro_usd, outcome, billing_event_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		req.TenantID,
		req.ClaimID,
		req.IdempotencyKey,
		fingerprint,
		req.AmountMicroUSD,
		req.Reason,
		req.RequireExact,
		appliedMicros,
		coveredMicros,
		outcome,
		nullableInt64(billingEventID),
	)
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrRefundIdempotencyConflict
	}
	return fmt.Errorf("billing: insert refund operation: %w", err)
}

func zeroRefundResult() *RefundResult {
	return &RefundResult{RefundMicroUSD: 0, AdjustmentRef: RefundSkippedAmountZeroRef}
}

func costUSDToMicros(cost decimal.Decimal) (int64, error) {
	micros := cost.Mul(decimal.NewFromInt(1_000_000)).Round(0)
	if micros.IsNegative() {
		return 0, nil
	}
	if micros.GreaterThan(maxCostMicroUSDDecimal) {
		return 0, ErrCostOverflow
	}
	return micros.IntPart(), nil
}

func billingAdjustmentRef(eventID int64) string {
	if eventID <= 0 {
		return "billing_event:0"
	}
	return fmt.Sprintf("billing_event:%d", eventID)
}

func addCostMicrosCapped(current, delta int64) int64 {
	if current < 0 {
		current = 0
	}
	if delta <= 0 {
		return current
	}
	if current >= maxCostMicroUSDInt64-delta {
		return maxCostMicroUSDInt64
	}
	return current + delta
}
