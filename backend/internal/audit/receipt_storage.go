package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrReceiptStorageRequired = errors.New("audit: receipt storage required")
	ErrReceiptDuplicate       = errors.New("audit: receipt already exists")
	ErrReceiptNotFound        = errors.New("audit: receipt not found")
)

type receiptRow interface {
	Scan(dest ...any) error
}

type receiptQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) receiptRow
}

type receiptExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type receiptDB interface {
	receiptQueryer
	receiptExecer
}

type sqlReceiptDB struct {
	db *sql.DB
}

func (s sqlReceiptDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, query, args...)
}

func (s sqlReceiptDB) QueryRowContext(ctx context.Context, query string, args ...any) receiptRow {
	return s.db.QueryRowContext(ctx, query, args...)
}

// ReceiptStorage 写入和读取 user_cost_receipts 物理 snapshot。
type ReceiptStorage struct {
	db   *sql.DB
	exec receiptDB
}

// NewReceiptStorage 用 database/sql 构造 user_cost_receipts append-only writer。
func NewReceiptStorage(db *sql.DB) (*ReceiptStorage, error) {
	if db == nil {
		return nil, errors.New("audit: sql db required")
	}
	return &ReceiptStorage{db: db, exec: sqlReceiptDB{db: db}}, nil
}

// AppendReceipt 只 INSERT 新 receipt，重复 request_id 交给唯一约束拒绝。
func (rs *ReceiptStorage) AppendReceipt(ctx context.Context, receipt *CostReceipt) error {
	if rs == nil {
		return ErrReceiptStorageRequired
	}
	db, err := rs.backend()
	if err != nil {
		return err
	}
	if err := validateReceiptForStorage(receipt); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO user_cost_receipts (
    tenant_id, request_id, model, input_tokens, output_tokens, cached_tokens,
    cost_usd_micros, rate_table_snapshot_id, signer_fingerprint, signed_hash,
    created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		receipt.TenantID,
		receipt.RequestID,
		receipt.Model,
		receipt.InputTokens,
		receipt.OutputTokens,
		receipt.CachedTokens,
		receipt.CostUSDMicros,
		receipt.RateTableSnapshotID,
		append([]byte(nil), receipt.SignerFingerprint...),
		append([]byte(nil), receipt.SignedHash...),
		receipt.CreatedAt.UTC(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: request_id %s", ErrReceiptDuplicate, receipt.RequestID)
		}
		return fmt.Errorf("audit: append receipt: %w", err)
	}
	return nil
}

// GetReceipt 按 tenant + request_id 读取已签名 snapshot。
func (rs *ReceiptStorage) GetReceipt(ctx context.Context, requestID string, tenantID int64) (*CostReceipt, error) {
	if rs == nil {
		return nil, ErrReceiptStorageRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return nil, err
	}
	db, err := rs.backend()
	if err != nil {
		return nil, err
	}
	row := db.QueryRowContext(ctx, `
SELECT request_id, tenant_id, model, input_tokens, output_tokens, cached_tokens,
       cost_usd_micros, rate_table_snapshot_id, signer_fingerprint, signed_hash,
       created_at
FROM user_cost_receipts
WHERE request_id = $1 AND tenant_id = $2`,
		requestID,
		tenantID,
	)
	var (
		rawRequestID string
		receipt      CostReceipt
	)
	if err := row.Scan(
		&rawRequestID,
		&receipt.TenantID,
		&receipt.Model,
		&receipt.InputTokens,
		&receipt.OutputTokens,
		&receipt.CachedTokens,
		&receipt.CostUSDMicros,
		&receipt.RateTableSnapshotID,
		&receipt.SignerFingerprint,
		&receipt.SignedHash,
		&receipt.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrReceiptNotFound
		}
		return nil, fmt.Errorf("audit: get receipt: %w", err)
	}
	receipt.RequestID = rawRequestID
	return &receipt, nil
}

func (rs *ReceiptStorage) backend() (receiptDB, error) {
	if rs.exec != nil {
		return rs.exec, nil
	}
	if rs.db != nil {
		return sqlReceiptDB{db: rs.db}, nil
	}
	return nil, errors.New("audit: sql db required")
}

func validateReceiptForStorage(receipt *CostReceipt) error {
	if err := validateReceiptForSigning(receipt); err != nil {
		return err
	}
	if len(receipt.SignerFingerprint) == 0 {
		return fmt.Errorf("%w: signer_fingerprint missing", ErrReceiptInvalidDerivedData)
	}
	if len(receipt.SignedHash) == 0 {
		return fmt.Errorf("%w: signed_hash missing", ErrReceiptInvalidDerivedData)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	if errors.Is(err, ErrReceiptDuplicate) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate")
}
