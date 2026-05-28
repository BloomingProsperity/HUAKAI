package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGXReceiptStorage 让 gateway 直接复用 pgxpool 读取 receipt snapshot。
type PGXReceiptStorage struct {
	pool *pgxpool.Pool
}

// NewPGXReceiptStorage 构造 pgxpool 版本的 receipt reader。
func NewPGXReceiptStorage(pool *pgxpool.Pool) (*PGXReceiptStorage, error) {
	if pool == nil {
		return nil, errors.New("audit: pgx pool required")
	}
	return &PGXReceiptStorage{pool: pool}, nil
}

// AppendReceipt 只在 settlement 成功后写入 signed receipt snapshot。
func (rs *PGXReceiptStorage) AppendReceipt(ctx context.Context, receipt *CostReceipt) error {
	if rs == nil || rs.pool == nil {
		return ErrReceiptStorageRequired
	}
	tx, err := rs.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("audit: begin receipt append: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	if err := appendReceiptPGX(ctx, tx, receipt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("audit: commit receipt append: %w", err)
	}
	committed = true
	return nil
}

func (rs *PGXReceiptStorage) AppendRefundReceipt(ctx context.Context, receipt *CostReceipt) error {
	return rs.AppendReceipt(ctx, receipt)
}

func (rs *PGXReceiptStorage) AppendInTx(ctx context.Context, tx pgx.Tx, receipt *CostReceipt) error {
	if rs == nil {
		return ErrReceiptStorageRequired
	}
	if tx == nil {
		return ErrReceiptStorageRequired
	}
	return appendReceiptPGX(ctx, tx, receipt)
}

func (rs *PGXReceiptStorage) AppendRefundReceiptInTx(ctx context.Context, tx pgx.Tx, receipt *CostReceipt) error {
	return rs.AppendInTx(ctx, tx, receipt)
}

type pgxReceiptExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func appendReceiptPGX(ctx context.Context, execer pgxReceiptExecer, receipt *CostReceipt) error {
	if execer == nil {
		return ErrReceiptStorageRequired
	}
	if err := validateReceiptForStorage(receipt); err != nil {
		return err
	}
	owner := receiptOwnerFromReceipt(receipt)
	if err := validateReceiptOwner(owner); err != nil {
		return err
	}
	adjustmentRefs, err := json.Marshal(normalizedAdjustmentRefs(receipt.AdjustmentRefs))
	if err != nil {
		return fmt.Errorf("audit: marshal receipt adjustment refs: %w", err)
	}
	_, err = execer.Exec(ctx, `
INSERT INTO user_cost_receipts (
    tenant_id, request_id, receipt_sequence, model, input_tokens, output_tokens, cached_tokens,
    cost_usd_micros, rate_table_snapshot_id, signer_fingerprint, signed_hash,
    created_at, validation_state, verdict, adjustment_refs
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb)`,
		receipt.TenantID,
		receipt.RequestID,
		receipt.ReceiptSequence,
		receipt.Model,
		receipt.InputTokens,
		receipt.OutputTokens,
		receipt.CachedTokens,
		receipt.CostUSDMicros,
		receipt.RateTableSnapshotID,
		receiptBytea(receipt.SignerFingerprint),
		receiptBytea(receipt.SignedHash),
		receipt.CreatedAt.UTC(),
		NormalizeReceiptValidationState(receipt.ValidationState),
		NormalizeReceiptVerdict(receipt.Verdict),
		adjustmentRefs,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: request_id %s sequence %d", ErrReceiptDuplicate, receipt.RequestID, receipt.ReceiptSequence)
		}
		return fmt.Errorf("audit: append receipt: %w", err)
	}
	if err := insertReceiptOwnerPGX(ctx, execer, receipt, owner); err != nil {
		return err
	}
	return nil
}

func insertReceiptOwnerPGX(ctx context.Context, execer pgxReceiptExecer, receipt *CostReceipt, owner ReceiptOwner) error {
	_, err := execer.Exec(ctx, `
INSERT INTO user_cost_receipt_owners (
    tenant_id, request_id, receipt_sequence, user_id, claim_id, owner_source
) VALUES ($1, $2, $3, $4, $5, $6)`,
		receipt.TenantID,
		receipt.RequestID,
		receipt.ReceiptSequence,
		owner.UserID,
		owner.ClaimID,
		owner.OwnerSource,
	)
	if err != nil {
		return fmt.Errorf("audit: append receipt owner: %w", err)
	}
	return nil
}

// GetReceiptForUser 按 tenant + request_id + user_id 读取已签名 snapshot。
func (rs *PGXReceiptStorage) GetReceiptForUser(ctx context.Context, requestID string, tenantID, userID int64) (*CostReceipt, error) {
	if rs == nil || rs.pool == nil {
		return nil, ErrReceiptStorageRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, ErrReceiptNotFound
	}
	row := rs.pool.QueryRow(ctx, `
SELECT `+receiptSelectColumns+`
FROM user_cost_receipts r
INNER JOIN user_cost_receipt_owners o
  ON o.tenant_id = r.tenant_id
 AND o.request_id = r.request_id
 AND o.receipt_sequence = r.receipt_sequence
WHERE r.request_id = $1 AND r.tenant_id = $2 AND o.user_id = $3
ORDER BY r.receipt_sequence DESC
LIMIT 1`,
		requestID,
		tenantID,
		userID,
	)
	receipt, err := scanReceiptRow(row, pgx.ErrNoRows)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("audit: get receipt for user: %w", err)
	}
	return receipt, nil
}

// GetReceiptForAdmin 按 tenant + request_id 读取已签名 snapshot，不做 user owner 过滤。
func (rs *PGXReceiptStorage) GetReceiptForAdmin(ctx context.Context, requestID string, tenantID int64) (*CostReceipt, error) {
	if rs == nil || rs.pool == nil {
		return nil, ErrReceiptStorageRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return nil, err
	}
	row := rs.pool.QueryRow(ctx, `
SELECT `+receiptSelectColumns+`
FROM user_cost_receipts r
LEFT JOIN user_cost_receipt_owners o
  ON o.tenant_id = r.tenant_id
 AND o.request_id = r.request_id
 AND o.receipt_sequence = r.receipt_sequence
WHERE r.request_id = $1 AND r.tenant_id = $2
ORDER BY r.receipt_sequence DESC
LIMIT 1`,
		requestID,
		tenantID,
	)
	receipt, err := scanReceiptRow(row, pgx.ErrNoRows)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("audit: get receipt for admin: %w", err)
	}
	return receipt, nil
}

// GetReceipt preserves the legacy admin-only storage semantics until callers split.
func (rs *PGXReceiptStorage) GetReceipt(ctx context.Context, requestID string, tenantID int64) (*CostReceipt, error) {
	return rs.GetReceiptForAdmin(ctx, requestID, tenantID)
}

// GetReceiptBySequence 按 tenant + request_id + receipt_sequence 读取指定 snapshot。
func (rs *PGXReceiptStorage) GetReceiptBySequence(ctx context.Context, requestID string, tenantID int64, sequence int32) (*CostReceipt, error) {
	if rs == nil || rs.pool == nil {
		return nil, ErrReceiptStorageRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return nil, err
	}
	if sequence < 0 {
		return nil, fmt.Errorf("%w: receipt_sequence must be non-negative", ErrReceiptInvalidDerivedData)
	}
	row := rs.pool.QueryRow(ctx, `
SELECT `+receiptSelectColumns+`
FROM user_cost_receipts r
LEFT JOIN user_cost_receipt_owners o
  ON o.tenant_id = r.tenant_id
 AND o.request_id = r.request_id
 AND o.receipt_sequence = r.receipt_sequence
WHERE r.request_id = $1 AND r.tenant_id = $2 AND r.receipt_sequence = $3
LIMIT 1`,
		requestID,
		tenantID,
		sequence,
	)
	receipt, err := scanReceiptRow(row, pgx.ErrNoRows)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("audit: get receipt by sequence: %w", err)
	}
	return receipt, nil
}

func (rs *PGXReceiptStorage) GetReceiptByDisplayID(ctx context.Context, displayID string, tenantID, userID int64) (*CostReceipt, error) {
	if rs == nil || rs.pool == nil {
		return nil, ErrReceiptStorageRequired
	}
	displayID = strings.TrimSpace(displayID)
	if !strings.HasPrefix(displayID, "receipt_") {
		return nil, ErrReceiptNotFound
	}
	if tenantID <= 0 || userID <= 0 {
		return nil, ErrReceiptNotFound
	}
	rows, err := rs.pool.Query(ctx, `
SELECT `+receiptSelectColumns+`
FROM user_cost_receipts r
INNER JOIN user_cost_receipt_owners o
  ON o.tenant_id = r.tenant_id
 AND o.request_id = r.request_id
 AND o.receipt_sequence = r.receipt_sequence
WHERE r.tenant_id = $1 AND o.user_id = $2
ORDER BY r.created_at DESC, r.receipt_sequence DESC`,
		tenantID,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("audit: query receipt by display id: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		receipt, err := scanReceiptRow(rows, pgx.ErrNoRows)
		if err != nil {
			return nil, fmt.Errorf("audit: scan receipt by display id: %w", err)
		}
		got, err := FinalTrustReceiptDisplayID(receipt)
		if err != nil {
			return nil, fmt.Errorf("audit: compute receipt display id: %w", err)
		}
		if got == displayID {
			return receipt, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: iterate receipt by display id: %w", err)
	}
	return nil, ErrReceiptNotFound
}

func (rs *PGXReceiptStorage) GetByRefundIdempotency(ctx context.Context, requestID string, tenantID int64, idempotencyKey string) (*CostReceipt, error) {
	if rs == nil || rs.pool == nil {
		return nil, ErrReceiptStorageRequired
	}
	return getReceiptByRefundIdempotencyPGX(ctx, rs.pool, requestID, tenantID, idempotencyKey)
}

// GetRefundedReceipt 按 tenant + request_id 读取任意序号的已退款 snapshot。
func (rs *PGXReceiptStorage) GetRefundedReceipt(ctx context.Context, requestID string, tenantID int64) (*CostReceipt, error) {
	if rs == nil || rs.pool == nil {
		return nil, ErrReceiptStorageRequired
	}
	return getRefundedReceiptPGX(ctx, rs.pool, requestID, tenantID)
}

func (rs *PGXReceiptStorage) GetRefundedReceiptInTx(ctx context.Context, tx pgx.Tx, requestID string, tenantID int64) (*CostReceipt, error) {
	if rs == nil || tx == nil {
		return nil, ErrReceiptStorageRequired
	}
	return getRefundedReceiptPGX(ctx, tx, requestID, tenantID)
}

func (rs *PGXReceiptStorage) MaxReceiptSequence(ctx context.Context, requestID string, tenantID int64) (int32, error) {
	if rs == nil || rs.pool == nil {
		return 0, ErrReceiptStorageRequired
	}
	return maxReceiptSequencePGX(ctx, rs.pool, requestID, tenantID)
}

func (rs *PGXReceiptStorage) MaxReceiptSequenceInTx(ctx context.Context, tx pgx.Tx, requestID string, tenantID int64) (int32, error) {
	if rs == nil || tx == nil {
		return 0, ErrReceiptStorageRequired
	}
	return maxReceiptSequencePGX(ctx, tx, requestID, tenantID)
}

type pgxReceiptQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getReceiptByRefundIdempotencyPGX(ctx context.Context, queryer pgxReceiptQueryer, requestID string, tenantID int64, idempotencyKey string) (*CostReceipt, error) {
	if queryer == nil {
		return nil, ErrReceiptStorageRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return nil, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return nil, fmt.Errorf("%w: refund idempotency key missing", ErrReceiptInvalidDerivedData)
	}
	needle, err := json.Marshal([]string{idempotencyKey})
	if err != nil {
		return nil, fmt.Errorf("audit: marshal refund idempotency lookup: %w", err)
	}
	row := queryer.QueryRow(ctx, `
SELECT `+receiptSelectColumns+`
FROM user_cost_receipts r
LEFT JOIN user_cost_receipt_owners o
  ON o.tenant_id = r.tenant_id
 AND o.request_id = r.request_id
 AND o.receipt_sequence = r.receipt_sequence
WHERE r.request_id = $1 AND r.tenant_id = $2 AND r.adjustment_refs @> $3::jsonb
ORDER BY r.receipt_sequence DESC
LIMIT 1`,
		requestID,
		tenantID,
		string(needle),
	)
	receipt, err := scanReceiptRow(row, pgx.ErrNoRows)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("audit: get refund receipt by idempotency: %w", err)
	}
	return receipt, nil
}

func getRefundedReceiptPGX(ctx context.Context, queryer pgxReceiptQueryer, requestID string, tenantID int64) (*CostReceipt, error) {
	if queryer == nil {
		return nil, ErrReceiptStorageRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return nil, err
	}
	row := queryer.QueryRow(ctx, `
SELECT `+receiptSelectColumns+`
FROM user_cost_receipts r
LEFT JOIN user_cost_receipt_owners o
  ON o.tenant_id = r.tenant_id
 AND o.request_id = r.request_id
 AND o.receipt_sequence = r.receipt_sequence
WHERE r.request_id = $1 AND r.tenant_id = $2 AND r.validation_state = $3
ORDER BY r.receipt_sequence DESC
LIMIT 1`,
		requestID,
		tenantID,
		ReceiptValidationStateMismatchRefunded,
	)
	receipt, err := scanReceiptRow(row, pgx.ErrNoRows)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("audit: get refunded receipt: %w", err)
	}
	return receipt, nil
}

func maxReceiptSequencePGX(ctx context.Context, queryer pgxReceiptQueryer, requestID string, tenantID int64) (int32, error) {
	if queryer == nil {
		return 0, ErrReceiptStorageRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return 0, err
	}
	var maxSequence int32
	if err := queryer.QueryRow(ctx, `
SELECT COALESCE(MAX(receipt_sequence), 0)
FROM user_cost_receipts
WHERE request_id = $1 AND tenant_id = $2`,
		requestID,
		tenantID,
	).Scan(&maxSequence); err != nil {
		return 0, fmt.Errorf("audit: get max receipt sequence: %w", err)
	}
	return maxSequence, nil
}

// PGXReceiptSource 用 pgxpool 读取 receipt 派生输入，供 gateway main 直接接线。
type PGXReceiptSource struct {
	pool *pgxpool.Pool
}

func NewPGXReceiptSource(pool *pgxpool.Pool) (*PGXReceiptSource, error) {
	if pool == nil {
		return nil, errors.New("audit: pgx pool required")
	}
	return &PGXReceiptSource{pool: pool}, nil
}

func (s *PGXReceiptSource) LookupReceiptInputs(ctx context.Context, requestID string, tenantID int64) (ReceiptInputs, error) {
	if s == nil || s.pool == nil {
		return ReceiptInputs{}, ErrReceiptSourceRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return ReceiptInputs{}, err
	}
	var (
		inputs         ReceiptInputs
		costUSD        string
		snapshot       sql.NullString
		claimID        int64
		userID         int64
		usageRecordID  sql.NullInt64
		createdAt      sql.NullTime
		modelNullable  sql.NullString
		inputTokens    sql.NullInt64
		outputTokens   sql.NullInt64
		cacheReadToken sql.NullInt64
		ownerSource    sql.NullString
	)
	err := s.pool.QueryRow(ctx, receiptInputsSQL, requestID, tenantID).Scan(
		&inputs.TenantID,
		&claimID,
		&userID,
		&modelNullable,
		&inputTokens,
		&outputTokens,
		&cacheReadToken,
		&costUSD,
		&snapshot,
		&usageRecordID,
		&createdAt,
		&ownerSource,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ReceiptInputs{}, ErrReceiptInputsNotFound
		}
		return ReceiptInputs{}, fmt.Errorf("audit: lookup receipt inputs: %w", err)
	}
	if !usageRecordID.Valid {
		return ReceiptInputs{}, ErrReceiptUnavailable
	}
	costMicros, err := usdDecimalStringToMicros(costUSD)
	if err != nil {
		return ReceiptInputs{}, err
	}
	inputs.ClaimID = claimID
	inputs.UserID = userID
	inputs.OwnerSource = receiptOwnerSourceFromSettlementSource(ownerSource.String)
	inputs.Model = modelNullable.String
	inputs.InputTokens = inputTokens.Int64
	inputs.OutputTokens = outputTokens.Int64
	inputs.CachedTokens = cacheReadToken.Int64
	inputs.CostUSDMicros = costMicros
	inputs.RateTableSnapshotID = rateTableSnapshotID(snapshot.String, usageRecordID.Int64)
	if createdAt.Valid {
		inputs.CreatedAt = createdAt.Time
	}
	if err := validateReceiptInputs(inputs); err != nil {
		return ReceiptInputs{}, err
	}
	return inputs, nil
}
