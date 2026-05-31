package voucher

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) CreateVoucher(ctx context.Context, rec createVoucherRecord) (Voucher, error) {
	if s == nil || s.pool == nil {
		return Voucher{}, ErrStoreNotConfigured
	}
	row := s.pool.QueryRow(ctx, `
INSERT INTO voucher (
	tenant_id, batch_id, code_hash, code_fingerprint, amount_cents, currency_code,
	valid_from, valid_until, max_redemptions, single_use_per_user, status,
	eligible_user_id, created_by_admin_id, created_at, updated_at
) VALUES (
	$1, $2, $3, $4, $5, $6,
	$7, $8, $9, $10,
	CASE WHEN $8 <= $13 THEN 'expired' ELSE 'active' END,
	$14, $11, $12, $12
)
RETURNING id, tenant_id, batch_id, code_hash, code_fingerprint, amount_cents, currency_code,
	valid_from, valid_until, max_redemptions, redeemed_count, single_use_per_user, status,
	eligible_user_id, created_by_admin_id, revoked_by_admin_id, revoked_reason, created_at, updated_at, revoked_at`,
		rec.TenantID, rec.BatchID, rec.CodeHash, rec.CodeFingerprint, rec.AmountCents, rec.CurrencyCode,
		rec.ValidFrom, rec.ValidUntil, rec.MaxRedemptions, rec.SingleUsePerUser, nullableAdminID(rec.AdminID),
		rec.Now, rec.Now, rec.EligibleUserID)
	v, err := scanVoucher(row)
	if isUniqueViolation(err) {
		return Voucher{}, ErrVoucherDuplicate
	}
	if err != nil {
		return Voucher{}, fmt.Errorf("voucher: insert voucher: %w", err)
	}
	return v, nil
}

func (s *PostgresStore) CreateBatch(ctx context.Context, batchRec createBatchRecord, voucherRecs []createVoucherRecord) (Batch, []Voucher, error) {
	if s == nil || s.pool == nil {
		return Batch{}, nil, ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Batch{}, nil, fmt.Errorf("voucher: begin batch create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	b := Batch{}
	err = tx.QueryRow(ctx, `
INSERT INTO voucher_batch (
	tenant_id, created_by_admin_id, requested_count, created_count, amount_cents,
	currency_code, valid_from, valid_until, max_redemptions, single_use_per_user,
	status, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'completed', $11)
RETURNING id, tenant_id, created_by_admin_id, requested_count, created_count, amount_cents,
	currency_code, valid_from, valid_until, max_redemptions, single_use_per_user, status, created_at`,
		batchRec.TenantID, nullableAdminID(batchRec.AdminID), batchRec.RequestedCount, len(voucherRecs),
		batchRec.AmountCents, batchRec.CurrencyCode, batchRec.ValidFrom, batchRec.ValidUntil,
		batchRec.MaxRedemptions, batchRec.SingleUsePerUser, batchRec.Now).Scan(
		&b.ID, &b.TenantID, &b.CreatedByAdminID, &b.RequestedCount, &b.CreatedCount,
		&b.AmountCents, &b.CurrencyCode, &b.ValidFrom, &b.ValidUntil, &b.MaxRedemptions,
		&b.SingleUsePerUser, &b.Status, &b.CreatedAt,
	)
	if err != nil {
		return Batch{}, nil, fmt.Errorf("voucher: insert batch: %w", err)
	}
	vouchers := make([]Voucher, 0, len(voucherRecs))
	for _, rec := range voucherRecs {
		batchID := b.ID
		rec.BatchID = &batchID
		v, err := insertVoucherTx(ctx, tx, rec)
		if isUniqueViolation(err) {
			return Batch{}, nil, ErrVoucherDuplicate
		}
		if err != nil {
			return Batch{}, nil, err
		}
		vouchers = append(vouchers, v)
	}
	if err := tx.Commit(ctx); err != nil {
		return Batch{}, nil, fmt.Errorf("voucher: commit batch create: %w", err)
	}
	return b, vouchers, nil
}

func (s *PostgresStore) ListVouchers(ctx context.Context, input ListInput) ([]Voucher, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, batch_id, code_hash, code_fingerprint, amount_cents, currency_code,
	valid_from, valid_until, max_redemptions, redeemed_count, single_use_per_user, status,
	eligible_user_id, created_by_admin_id, revoked_by_admin_id, revoked_reason, created_at, updated_at, revoked_at
FROM voucher
WHERE tenant_id=$1
ORDER BY id DESC
LIMIT $2`, input.TenantID, input.Limit)
	if err != nil {
		return nil, fmt.Errorf("voucher: list vouchers: %w", err)
	}
	defer rows.Close()
	var out []Voucher
	for rows.Next() {
		v, err := scanVoucher(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetBatch(ctx context.Context, tenantID, id int64) (GetBatchResult, error) {
	if s == nil || s.pool == nil {
		return GetBatchResult{}, ErrStoreNotConfigured
	}
	var b Batch
	err := s.pool.QueryRow(ctx, `
SELECT id, tenant_id, created_by_admin_id, requested_count, created_count, amount_cents,
	currency_code, valid_from, valid_until, max_redemptions, single_use_per_user, status, created_at
FROM voucher_batch
WHERE tenant_id=$1 AND id=$2`, tenantID, id).Scan(
		&b.ID, &b.TenantID, &b.CreatedByAdminID, &b.RequestedCount, &b.CreatedCount,
		&b.AmountCents, &b.CurrencyCode, &b.ValidFrom, &b.ValidUntil, &b.MaxRedemptions,
		&b.SingleUsePerUser, &b.Status, &b.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return GetBatchResult{}, ErrVoucherNotFound
	}
	if err != nil {
		return GetBatchResult{}, fmt.Errorf("voucher: get batch: %w", err)
	}
	vouchers, err := s.listVouchersByBatch(ctx, tenantID, id)
	if err != nil {
		return GetBatchResult{}, err
	}
	return GetBatchResult{Batch: b, Vouchers: vouchers}, nil
}

// listVouchersByBatch 按 (tenant_id, batch_id) 直接查询该批次的全部 voucher。此前 GetBatch 复用了
// 租户级 ListVouchers(LIMIT 1000, ORDER BY id DESC)再在内存里按 batch_id 过滤 —— 一旦该租户在目标
// 批次之后又产生了 1000 张更新的 voucher,目标批次就会落在最新 1000 之外,被静默漏掉或只返回部分
// (批次上限正是 1000,极端情况下整批返回为空),且接口仍返回 200(S2-091)。批次级 WHERE 把行集
// 限定在该批次内(schema 限定单批 ≤1000 张),无需再加可能重新引入"静默截断"的全局 LIMIT。
func (s *PostgresStore) listVouchersByBatch(ctx context.Context, tenantID, batchID int64) ([]Voucher, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, batch_id, code_hash, code_fingerprint, amount_cents, currency_code,
	valid_from, valid_until, max_redemptions, redeemed_count, single_use_per_user, status,
	eligible_user_id, created_by_admin_id, revoked_by_admin_id, revoked_reason, created_at, updated_at, revoked_at
FROM voucher
WHERE tenant_id=$1 AND batch_id=$2
ORDER BY id DESC`, tenantID, batchID)
	if err != nil {
		return nil, fmt.Errorf("voucher: list batch vouchers: %w", err)
	}
	defer rows.Close()
	var out []Voucher
	for rows.Next() {
		v, err := scanVoucher(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *PostgresStore) RevokeVoucher(ctx context.Context, input RevokeInput) (Voucher, error) {
	if s == nil || s.pool == nil {
		return Voucher{}, ErrStoreNotConfigured
	}
	row := s.pool.QueryRow(ctx, `
UPDATE voucher
SET status='revoked', revoked_by_admin_id=$3, revoked_reason=$4, revoked_at=$5, updated_at=$5
WHERE tenant_id=$1 AND id=$2
RETURNING id, tenant_id, batch_id, code_hash, code_fingerprint, amount_cents, currency_code,
	valid_from, valid_until, max_redemptions, redeemed_count, single_use_per_user, status,
	eligible_user_id, created_by_admin_id, revoked_by_admin_id, revoked_reason, created_at, updated_at, revoked_at`,
		input.TenantID, input.ID, nullableAdminID(input.AdminID), input.Reason, input.Now)
	v, err := scanVoucher(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Voucher{}, ErrVoucherNotFound
	}
	if err != nil {
		return Voucher{}, fmt.Errorf("voucher: revoke: %w", err)
	}
	return v, nil
}

func (s *PostgresStore) Redeem(ctx context.Context, rec redeemRecord) (RedeemResult, error) {
	if s == nil || s.pool == nil {
		return RedeemResult{}, ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return RedeemResult{}, fmt.Errorf("voucher: begin redeem: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if rec.IdempotencyKey != "" {
		result, ok, err := s.getIdempotentRedemption(ctx, tx, rec)
		if err != nil {
			return RedeemResult{}, err
		}
		if ok {
			if err := tx.Commit(ctx); err != nil {
				return RedeemResult{}, fmt.Errorf("voucher: commit idempotent redeem: %w", err)
			}
			return result, nil
		}
	}
	v, err := getVoucherByCodeHashForUpdate(ctx, tx, rec.TenantID, rec.CodeHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return RedeemResult{}, ErrVoucherNotFound
	}
	if err != nil {
		return RedeemResult{}, err
	}
	if err := evaluateVoucher(v, rec.UserID, rec.Now, func(v Voucher, userID int64) bool {
		var exists bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM voucher_redemption
			WHERE tenant_id=$1 AND voucher_id=$2 AND user_id=$3 AND single_use_per_user
		)`, v.TenantID, v.ID, userID).Scan(&exists)
		return exists
	}); err != nil {
		if errors.Is(err, ErrVoucherExpired) && v.Status == StatusActive {
			_, _ = tx.Exec(ctx, `UPDATE voucher SET status='expired', updated_at=$3 WHERE tenant_id=$1 AND id=$2`, v.TenantID, v.ID, rec.Now)
		}
		return RedeemResult{}, err
	}
	amount, err := redeemedBalanceAmount(v)
	if err != nil {
		return RedeemResult{}, err
	}
	var red Redemption
	err = tx.QueryRow(ctx, `
INSERT INTO voucher_redemption (
	tenant_id, voucher_id, user_id, idempotency_key, amount_cents, currency_code,
	single_use_per_user, source_ip_hash, request_id, redeemed_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id, tenant_id, voucher_id, user_id, COALESCE(idempotency_key, ''), amount_cents, currency_code,
	single_use_per_user, source_ip_hash, COALESCE(request_id, ''), COALESCE(billing_event_id, 0), redeemed_at`,
		rec.TenantID, v.ID, rec.UserID, nullableText(rec.IdempotencyKey), v.AmountCents, v.CurrencyCode,
		v.SingleUsePerUser, rec.SourceIPHash, nullableText(rec.RequestID), rec.Now).Scan(
		&red.ID, &red.TenantID, &red.VoucherID, &red.UserID, &red.IdempotencyKey,
		&red.AmountCents, &red.CurrencyCode, &red.SingleUsePerUser, &red.SourceIPHash,
		&red.RequestID, &red.BillingEventID, &red.RedeemedAt,
	)
	if isUniqueViolation(err) {
		return RedeemResult{}, ErrAlreadyRedeemed
	}
	if err != nil {
		return RedeemResult{}, fmt.Errorf("voucher: insert redemption: %w", err)
	}
	red.CodeFingerprint = v.CodeFingerprint
	v.RedeemedCount++
	nextStatus := v.Status
	if v.RedeemedCount >= v.MaxRedemptions {
		nextStatus = StatusExhausted
	}
	if _, err := tx.Exec(ctx, `
UPDATE voucher
SET redeemed_count=redeemed_count+1,
    status=CASE WHEN redeemed_count+1 >= max_redemptions THEN 'exhausted' ELSE status END,
    updated_at=$3
WHERE tenant_id=$1 AND id=$2`, v.TenantID, v.ID, rec.Now); err != nil {
		return RedeemResult{}, fmt.Errorf("voucher: update voucher redemption count: %w", err)
	}
	v.Status = nextStatus
	v.UpdatedAt = rec.Now
	var billingID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO billing_events (
	tenant_id, event_type, actual_cost, actual_cost_signed,
	stream_state, delivered_token_count, fingerprint, voucher_redemption_id
) VALUES ($1, 'voucher_redeemed', $2, $2, 2, 0, $3, $4)
RETURNING id`, rec.TenantID, amount, v.CodeFingerprint, red.ID).Scan(&billingID); err != nil {
		return RedeemResult{}, fmt.Errorf("voucher: insert billing event: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE voucher_redemption SET billing_event_id=$3 WHERE tenant_id=$1 AND id=$2`, rec.TenantID, red.ID, billingID); err != nil {
		return RedeemResult{}, fmt.Errorf("voucher: link billing event: %w", err)
	}
	red.BillingEventID = billingID
	if err := creditRedeemedBalance(ctx, tx, rec.TenantID, rec.UserID, amount, rec.Now); err != nil {
		return RedeemResult{}, err
	}
	balance, err := userBalanceCents(ctx, tx, rec.TenantID, rec.UserID)
	if err != nil {
		return RedeemResult{}, fmt.Errorf("voucher: read credited user balance: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RedeemResult{}, fmt.Errorf("voucher: commit redeem: %w", err)
	}
	return RedeemResult{Voucher: v, Redemption: red, BalanceCents: balance}, nil
}

func (s *PostgresStore) BillingEvents(ctx context.Context, tenantID, userID int64) ([]BillingEvent, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT be.id, be.tenant_id, be.event_type, vr.id, vr.voucher_id, vr.user_id,
	vr.amount_cents, be.fingerprint, be.occurred_at
FROM billing_events be
JOIN voucher_redemption vr ON vr.tenant_id=be.tenant_id AND vr.id=be.voucher_redemption_id
WHERE be.tenant_id=$1 AND ($2::bigint=0 OR vr.user_id=$2)
ORDER BY be.id`, tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BillingEvent
	for rows.Next() {
		var e BillingEvent
		if err := rows.Scan(&e.ID, &e.TenantID, &e.EventType, &e.RedemptionID, &e.VoucherID, &e.UserID, &e.AmountCents, &e.Fingerprint, &e.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PostgresStore) getIdempotentRedemption(ctx context.Context, tx pgx.Tx, rec redeemRecord) (RedeemResult, bool, error) {
	var red Redemption
	err := tx.QueryRow(ctx, `
SELECT id, tenant_id, voucher_id, user_id, COALESCE(idempotency_key, ''), amount_cents, currency_code,
	single_use_per_user, source_ip_hash, COALESCE(request_id, ''), COALESCE(billing_event_id, 0), redeemed_at
FROM voucher_redemption
WHERE tenant_id=$1 AND user_id=$2 AND idempotency_key=$3
FOR UPDATE`, rec.TenantID, rec.UserID, rec.IdempotencyKey).Scan(
		&red.ID, &red.TenantID, &red.VoucherID, &red.UserID, &red.IdempotencyKey,
		&red.AmountCents, &red.CurrencyCode, &red.SingleUsePerUser, &red.SourceIPHash,
		&red.RequestID, &red.BillingEventID, &red.RedeemedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return RedeemResult{}, false, nil
	}
	if err != nil {
		return RedeemResult{}, false, fmt.Errorf("voucher: read idempotent redemption: %w", err)
	}
	v, err := getVoucherByID(ctx, tx, red.TenantID, red.VoucherID)
	if err != nil {
		return RedeemResult{}, false, err
	}
	if string(v.CodeHash) != string(rec.CodeHash) {
		return RedeemResult{}, false, ErrIdempotencyConflict
	}
	red.CodeFingerprint = v.CodeFingerprint
	balance, err := idempotentBalanceCents(ctx, tx, rec.TenantID, rec.UserID, red)
	if err != nil {
		return RedeemResult{}, false, err
	}
	return RedeemResult{Voucher: v, Redemption: red, BalanceCents: balance, Idempotent: true}, true, nil
}

func redeemedBalanceAmount(v Voucher) (decimal.Decimal, error) {
	if v.CurrencyCode != supportedVoucherBalanceCurrency {
		return decimal.Decimal{}, fmt.Errorf("%w: unsupported user balance currency %s", ErrInvalidInput, v.CurrencyCode)
	}
	return decimal.NewFromInt(v.AmountCents).Div(decimal.NewFromInt(100)), nil
}

func creditRedeemedBalance(ctx context.Context, tx pgx.Tx, tenantID, userID int64, amount decimal.Decimal, now time.Time) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
VALUES ($1, $2, $3, 0, 1, $4)
ON CONFLICT (tenant_id, user_id) DO UPDATE
SET balance = user_balances.balance + EXCLUDED.balance,
    version = user_balances.version + 1,
    updated_at = EXCLUDED.updated_at`,
		tenantID, userID, amount, now); err != nil {
		return fmt.Errorf("voucher: credit user balance: %w", err)
	}
	return nil
}

func userBalanceCents(ctx context.Context, tx pgx.Tx, tenantID, userID int64) (int64, error) {
	balance, err := userBalanceAmount(ctx, tx, tenantID, userID)
	if err != nil {
		return 0, err
	}
	return balanceCents(balance), nil
}

func userBalanceAmount(ctx context.Context, tx pgx.Tx, tenantID, userID int64) (decimal.Decimal, error) {
	var balance decimal.Decimal
	if err := tx.QueryRow(ctx, `
SELECT balance
FROM user_balances
WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID).Scan(&balance); err != nil {
		return decimal.Decimal{}, err
	}
	return balance, nil
}

func balanceCents(balance decimal.Decimal) int64 {
	return balance.Mul(decimal.NewFromInt(100)).Round(0).IntPart()
}

func idempotentBalanceCents(ctx context.Context, tx pgx.Tx, tenantID, userID int64, red Redemption) (int64, error) {
	if red.BillingEventID <= 0 {
		return redemptionBalanceThrough(ctx, tx, tenantID, userID, red.ID)
	}
	currentBalance, err := userBalanceAmount(ctx, tx, tenantID, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return redemptionBalanceThrough(ctx, tx, tenantID, userID, red.ID)
	}
	if err != nil {
		return 0, fmt.Errorf("voucher: read idempotent user balance: %w", err)
	}
	redemptionEventTime, err := billingEventOccurredAt(ctx, tx, tenantID, red.BillingEventID)
	if err != nil {
		return 0, err
	}
	var postRedemptionDelta decimal.Decimal
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(SUM(CASE
	WHEN be.event_type = 'claim_committed' AND bh.claim_id IS NOT NULL AND bh.resolved_at > $2 THEN be.actual_cost
	WHEN be.event_type = 'voucher_redeemed' AND (be.occurred_at > $2 OR (be.occurred_at = $2 AND be.id > $3)) THEN -be.actual_cost
	WHEN be.event_type = 'reconciliation_appended' AND be.occurred_at > $2 THEN be.actual_cost_signed
	ELSE 0
END), 0)
FROM billing_events be
LEFT JOIN billing_ledger_claims blc ON blc.tenant_id = be.tenant_id AND blc.id = be.claim_id
LEFT JOIN balance_holds bh ON bh.tenant_id = be.tenant_id AND bh.claim_id = be.claim_id AND bh.state = 'captured'
LEFT JOIN voucher_redemption vr ON vr.tenant_id = be.tenant_id AND vr.id = be.voucher_redemption_id
WHERE be.tenant_id = $1
  AND (
	(be.event_type IN ('claim_committed', 'reconciliation_appended') AND blc.user_id = $4)
	OR (be.event_type = 'voucher_redeemed' AND vr.user_id = $4)
  )`, tenantID, redemptionEventTime, red.BillingEventID, userID).Scan(&postRedemptionDelta); err != nil {
		return 0, fmt.Errorf("voucher: read idempotent balance deltas: %w", err)
	}
	walletCents := balanceCents(currentBalance.Add(postRedemptionDelta))
	redemptionCents, err := redemptionBalanceThrough(ctx, tx, tenantID, userID, red.ID)
	if err != nil {
		return 0, err
	}
	if redemptionCents > walletCents {
		hasPriorDebit, err := hasWalletDebitBeforeTime(ctx, tx, tenantID, userID, redemptionEventTime)
		if err != nil {
			return 0, err
		}
		if !hasPriorDebit {
			return redemptionCents, nil
		}
	}
	return walletCents, nil
}

func insertVoucherTx(ctx context.Context, tx pgx.Tx, rec createVoucherRecord) (Voucher, error) {
	row := tx.QueryRow(ctx, `
INSERT INTO voucher (
	tenant_id, batch_id, code_hash, code_fingerprint, amount_cents, currency_code,
	valid_from, valid_until, max_redemptions, single_use_per_user, status,
	eligible_user_id, created_by_admin_id, created_at, updated_at
) VALUES (
	$1, $2, $3, $4, $5, $6,
	$7, $8, $9, $10,
	CASE WHEN $8 <= $13 THEN 'expired' ELSE 'active' END,
	$14, $11, $12, $12
)
RETURNING id, tenant_id, batch_id, code_hash, code_fingerprint, amount_cents, currency_code,
	valid_from, valid_until, max_redemptions, redeemed_count, single_use_per_user, status,
	eligible_user_id, created_by_admin_id, revoked_by_admin_id, revoked_reason, created_at, updated_at, revoked_at`,
		rec.TenantID, rec.BatchID, rec.CodeHash, rec.CodeFingerprint, rec.AmountCents, rec.CurrencyCode,
		rec.ValidFrom, rec.ValidUntil, rec.MaxRedemptions, rec.SingleUsePerUser, nullableAdminID(rec.AdminID),
		rec.Now, rec.Now, rec.EligibleUserID)
	v, err := scanVoucher(row)
	if err != nil {
		return Voucher{}, fmt.Errorf("voucher: insert voucher: %w", err)
	}
	return v, nil
}

func getVoucherByCodeHashForUpdate(ctx context.Context, tx pgx.Tx, tenantID int64, hash []byte) (Voucher, error) {
	return scanVoucher(tx.QueryRow(ctx, `
SELECT id, tenant_id, batch_id, code_hash, code_fingerprint, amount_cents, currency_code,
	valid_from, valid_until, max_redemptions, redeemed_count, single_use_per_user, status,
	eligible_user_id, created_by_admin_id, revoked_by_admin_id, revoked_reason, created_at, updated_at, revoked_at
FROM voucher
WHERE tenant_id=$1 AND code_hash=$2
FOR UPDATE`, tenantID, hash))
}

func getVoucherByID(ctx context.Context, tx pgx.Tx, tenantID, id int64) (Voucher, error) {
	return scanVoucher(tx.QueryRow(ctx, `
SELECT id, tenant_id, batch_id, code_hash, code_fingerprint, amount_cents, currency_code,
	valid_from, valid_until, max_redemptions, redeemed_count, single_use_per_user, status,
	eligible_user_id, created_by_admin_id, revoked_by_admin_id, revoked_reason, created_at, updated_at, revoked_at
FROM voucher
WHERE tenant_id=$1 AND id=$2`, tenantID, id))
}

type voucherScanner interface {
	Scan(dest ...any) error
}

func scanVoucher(row voucherScanner) (Voucher, error) {
	var v Voucher
	var batchID, eligibleUserID, createdBy, revokedBy sql.NullInt64
	var revokedAt sql.NullTime
	if err := row.Scan(
		&v.ID, &v.TenantID, &batchID, &v.CodeHash, &v.CodeFingerprint,
		&v.AmountCents, &v.CurrencyCode, &v.ValidFrom, &v.ValidUntil,
		&v.MaxRedemptions, &v.RedeemedCount, &v.SingleUsePerUser, &v.Status,
		&eligibleUserID, &createdBy, &revokedBy, &v.RevokedReason, &v.CreatedAt, &v.UpdatedAt, &revokedAt,
	); err != nil {
		return Voucher{}, err
	}
	if batchID.Valid {
		v.BatchID = &batchID.Int64
	}
	if eligibleUserID.Valid {
		v.EligibleUserID = &eligibleUserID.Int64
	}
	if createdBy.Valid {
		v.CreatedByAdminID = createdBy.Int64
	}
	if revokedBy.Valid {
		v.RevokedByAdminID = revokedBy.Int64
	}
	if revokedAt.Valid {
		t := revokedAt.Time.UTC()
		v.RevokedAt = &t
	}
	v.CodeHash = append([]byte(nil), v.CodeHash...)
	v.ValidFrom = v.ValidFrom.UTC()
	v.ValidUntil = v.ValidUntil.UTC()
	v.CreatedAt = v.CreatedAt.UTC()
	v.UpdatedAt = v.UpdatedAt.UTC()
	return v, nil
}

func redemptionBalanceThrough(ctx context.Context, tx pgx.Tx, tenantID, userID, redemptionID int64) (int64, error) {
	var balance int64
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(SUM(amount_cents), 0)::bigint
FROM voucher_redemption
WHERE tenant_id=$1 AND user_id=$2 AND id <= $3`, tenantID, userID, redemptionID).Scan(&balance); err != nil {
		return 0, fmt.Errorf("voucher: read balance: %w", err)
	}
	return balance, nil
}

func billingEventOccurredAt(ctx context.Context, tx pgx.Tx, tenantID, billingEventID int64) (time.Time, error) {
	var occurredAt time.Time
	if err := tx.QueryRow(ctx, `
SELECT occurred_at
FROM billing_events
WHERE tenant_id=$1 AND id=$2`, tenantID, billingEventID).Scan(&occurredAt); err != nil {
		return time.Time{}, fmt.Errorf("voucher: read redemption billing event time: %w", err)
	}
	return occurredAt.UTC(), nil
}

func hasWalletDebitBeforeTime(ctx context.Context, tx pgx.Tx, tenantID, userID int64, before time.Time) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM billing_events be
	JOIN billing_ledger_claims blc ON blc.tenant_id = be.tenant_id AND blc.id = be.claim_id
	JOIN balance_holds bh ON bh.tenant_id = be.tenant_id AND bh.claim_id = be.claim_id AND bh.state = 'captured'
	WHERE be.tenant_id = $1
	  AND bh.resolved_at < $2
	  AND be.event_type = 'claim_committed'
	  AND blc.user_id = $3
)`, tenantID, before, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("voucher: read prior wallet debit evidence: %w", err)
	}
	return exists, nil
}

func nullableAdminID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

var _ Store = (*PostgresStore)(nil)
var _ = time.Time{}
