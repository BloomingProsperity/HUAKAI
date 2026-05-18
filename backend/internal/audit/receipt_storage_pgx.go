package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

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
	return appendReceiptPGX(ctx, rs.pool, receipt)
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
		append([]byte(nil), receipt.SignerFingerprint...),
		append([]byte(nil), receipt.SignedHash...),
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
	return nil
}

// GetReceipt 按 tenant + request_id 读取已签名 snapshot。
func (rs *PGXReceiptStorage) GetReceipt(ctx context.Context, requestID string, tenantID int64) (*CostReceipt, error) {
	if rs == nil || rs.pool == nil {
		return nil, ErrReceiptStorageRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return nil, err
	}
	row := rs.pool.QueryRow(ctx, `
SELECT request_id, tenant_id, model, input_tokens, output_tokens, cached_tokens,
       cost_usd_micros, rate_table_snapshot_id, signer_fingerprint, signed_hash,
       created_at, validation_state, verdict, adjustment_refs, receipt_sequence
FROM user_cost_receipts
WHERE request_id = $1 AND tenant_id = $2
ORDER BY receipt_sequence DESC
LIMIT 1`,
		requestID,
		tenantID,
	)
	receipt, err := scanReceiptRow(row, pgx.ErrNoRows)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("audit: get receipt: %w", err)
	}
	return receipt, nil
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
SELECT request_id, tenant_id, model, input_tokens, output_tokens, cached_tokens,
       cost_usd_micros, rate_table_snapshot_id, signer_fingerprint, signed_hash,
       created_at, validation_state, verdict, adjustment_refs, receipt_sequence
FROM user_cost_receipts
WHERE request_id = $1 AND tenant_id = $2 AND receipt_sequence = $3
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
		usageRecordID  sql.NullInt64
		createdAt      sql.NullTime
		modelNullable  sql.NullString
		inputTokens    sql.NullInt64
		outputTokens   sql.NullInt64
		cacheReadToken sql.NullInt64
	)
	err := s.pool.QueryRow(ctx, receiptInputsSQL, requestID, tenantID).Scan(
		&inputs.TenantID,
		&claimID,
		&modelNullable,
		&inputTokens,
		&outputTokens,
		&cacheReadToken,
		&costUSD,
		&snapshot,
		&usageRecordID,
		&createdAt,
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
