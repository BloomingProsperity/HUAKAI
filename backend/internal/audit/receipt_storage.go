package audit

import (
	"context"
	"database/sql"
	"encoding/json"
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

const (
	ReceiptOwnerSourceSettle       = "settle"
	ReceiptOwnerSourceCacheHit     = "cache_hit"
	ReceiptOwnerSourceBackfillJoin = "backfill_join"
)

const receiptSelectColumns = `r.request_id, r.tenant_id,
       COALESCE(o.user_id, 0)::bigint AS user_id,
       COALESCE(o.claim_id, 0)::bigint AS claim_id,
       COALESCE(o.owner_source, '') AS owner_source,
       r.model, r.input_tokens, r.output_tokens, r.cached_tokens,
       r.cost_usd_micros, r.rate_table_snapshot_id, r.signer_fingerprint, r.signed_hash,
       r.created_at, r.validation_state, r.verdict, r.adjustment_refs, r.receipt_sequence`

// ReceiptOwner records the user ownership row that must be written atomically
// beside every cost receipt snapshot.
type ReceiptOwner struct {
	UserID      int64
	ClaimID     int64
	OwnerSource string
}

type CostReceiptReader interface {
	GetReceiptForUser(ctx context.Context, requestID string, tenantID, userID int64) (*CostReceipt, error)
	GetReceiptForAdmin(ctx context.Context, requestID string, tenantID int64) (*CostReceipt, error)
	GetReceipt(ctx context.Context, requestID string, tenantID int64) (*CostReceipt, error)
	GetReceiptBySequence(ctx context.Context, requestID string, tenantID int64, sequence int32) (*CostReceipt, error)
}

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

type receiptTx interface {
	receiptDB
	Commit() error
	Rollback() error
}

type receiptTxBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (receiptTx, error)
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

func (s sqlReceiptDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (receiptTx, error) {
	tx, err := s.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return sqlReceiptTx{tx: tx}, nil
}

type sqlReceiptTx struct {
	tx *sql.Tx
}

func (s sqlReceiptTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.tx.ExecContext(ctx, query, args...)
}

func (s sqlReceiptTx) QueryRowContext(ctx context.Context, query string, args ...any) receiptRow {
	return s.tx.QueryRowContext(ctx, query, args...)
}

func (s sqlReceiptTx) Commit() error {
	return s.tx.Commit()
}

func (s sqlReceiptTx) Rollback() error {
	return s.tx.Rollback()
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

// AppendReceipt 在同一事务里写入 receipt snapshot 和 owner sidecar。
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
	owner := receiptOwnerFromReceipt(receipt)
	if err := validateReceiptOwner(owner); err != nil {
		return err
	}
	tx, err := beginReceiptTx(ctx, db)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := insertReceiptRow(ctx, tx, receipt); err != nil {
		return err
	}
	if err := insertReceiptOwnerRow(ctx, tx, receipt, owner); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("audit: commit receipt append: %w", err)
	}
	committed = true
	return nil
}

func beginReceiptTx(ctx context.Context, db receiptDB) (receiptTx, error) {
	beginner, ok := db.(receiptTxBeginner)
	if !ok {
		return nil, errors.New("audit: receipt storage transaction required")
	}
	tx, err := beginner.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("audit: begin receipt append: %w", err)
	}
	return tx, nil
}

func insertReceiptRow(ctx context.Context, db receiptDB, receipt *CostReceipt) error {
	adjustmentRefs, err := json.Marshal(normalizedAdjustmentRefs(receipt.AdjustmentRefs))
	if err != nil {
		return fmt.Errorf("audit: marshal receipt adjustment refs: %w", err)
	}
	_, err = db.ExecContext(ctx, `
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

func insertReceiptOwnerRow(ctx context.Context, db receiptDB, receipt *CostReceipt, owner ReceiptOwner) error {
	_, err := db.ExecContext(ctx, `
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
func (rs *ReceiptStorage) GetReceiptForUser(ctx context.Context, requestID string, tenantID, userID int64) (*CostReceipt, error) {
	if rs == nil {
		return nil, ErrReceiptStorageRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, ErrReceiptNotFound
	}
	db, err := rs.backend()
	if err != nil {
		return nil, err
	}
	row := db.QueryRowContext(ctx, `
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
	receipt, err := scanReceiptRow(row, sql.ErrNoRows)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("audit: get receipt for user: %w", err)
	}
	return receipt, nil
}

// GetReceiptForAdmin 按 tenant + request_id 读取已签名 snapshot，不做 user owner 过滤。
func (rs *ReceiptStorage) GetReceiptForAdmin(ctx context.Context, requestID string, tenantID int64) (*CostReceipt, error) {
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
	receipt, err := scanReceiptRow(row, sql.ErrNoRows)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("audit: get receipt for admin: %w", err)
	}
	return receipt, nil
}

// GetReceipt preserves the legacy admin-only storage semantics until callers split.
func (rs *ReceiptStorage) GetReceipt(ctx context.Context, requestID string, tenantID int64) (*CostReceipt, error) {
	return rs.GetReceiptForAdmin(ctx, requestID, tenantID)
}

// GetReceiptBySequence 按 tenant + request_id + receipt_sequence 读取指定 snapshot。
func (rs *ReceiptStorage) GetReceiptBySequence(ctx context.Context, requestID string, tenantID int64, sequence int32) (*CostReceipt, error) {
	if rs == nil {
		return nil, ErrReceiptStorageRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return nil, err
	}
	if sequence < 0 {
		return nil, fmt.Errorf("%w: receipt_sequence must be non-negative", ErrReceiptInvalidDerivedData)
	}
	db, err := rs.backend()
	if err != nil {
		return nil, err
	}
	row := db.QueryRowContext(ctx, `
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
	receipt, err := scanReceiptRow(row, sql.ErrNoRows)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("audit: get receipt by sequence: %w", err)
	}
	return receipt, nil
}

func (rs *ReceiptStorage) GetByRefundIdempotency(ctx context.Context, requestID string, tenantID int64, idempotencyKey string) (*CostReceipt, error) {
	if rs == nil {
		return nil, ErrReceiptStorageRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return nil, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return nil, fmt.Errorf("%w: refund idempotency key missing", ErrReceiptInvalidDerivedData)
	}
	db, err := rs.backend()
	if err != nil {
		return nil, err
	}
	needle, err := json.Marshal([]string{idempotencyKey})
	if err != nil {
		return nil, fmt.Errorf("audit: marshal refund idempotency lookup: %w", err)
	}
	row := db.QueryRowContext(ctx, `
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
	receipt, err := scanReceiptRow(row, sql.ErrNoRows)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("audit: get refund receipt by idempotency: %w", err)
	}
	return receipt, nil
}

// GetRefundedReceipt 按 tenant + request_id 读取任意序号的已退款 snapshot。
func (rs *ReceiptStorage) GetRefundedReceipt(ctx context.Context, requestID string, tenantID int64) (*CostReceipt, error) {
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
	receipt, err := scanReceiptRow(row, sql.ErrNoRows)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("audit: get refunded receipt: %w", err)
	}
	return receipt, nil
}

func (rs *ReceiptStorage) MaxReceiptSequence(ctx context.Context, requestID string, tenantID int64) (int32, error) {
	if rs == nil {
		return 0, ErrReceiptStorageRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return 0, err
	}
	db, err := rs.backend()
	if err != nil {
		return 0, err
	}
	var maxSequence int32
	if err := db.QueryRowContext(ctx, `
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

func receiptOwnerFromReceipt(receipt *CostReceipt) ReceiptOwner {
	if receipt == nil {
		return ReceiptOwner{}
	}
	return ReceiptOwner{
		UserID:      receipt.UserID,
		ClaimID:     receipt.ClaimID,
		OwnerSource: strings.TrimSpace(receipt.OwnerSource),
	}
}

func validateReceiptOwner(owner ReceiptOwner) error {
	switch {
	case owner.UserID <= 0:
		return fmt.Errorf("%w: receipt owner user_id required", ErrReceiptInvalidDerivedData)
	case owner.ClaimID <= 0:
		return fmt.Errorf("%w: receipt owner claim_id required", ErrReceiptInvalidDerivedData)
	case !validReceiptOwnerSource(owner.OwnerSource):
		return fmt.Errorf("%w: receipt owner_source unsupported", ErrReceiptInvalidDerivedData)
	}
	return nil
}

func validReceiptOwnerSource(ownerSource string) bool {
	switch strings.TrimSpace(ownerSource) {
	case ReceiptOwnerSourceSettle, ReceiptOwnerSourceCacheHit, ReceiptOwnerSourceBackfillJoin:
		return true
	default:
		return false
	}
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

func scanReceiptRow(row receiptRow, noRows error) (*CostReceipt, error) {
	var (
		rawRequestID string
		receipt      CostReceipt
		adjustments  []byte
	)
	if err := row.Scan(
		&rawRequestID,
		&receipt.TenantID,
		&receipt.UserID,
		&receipt.ClaimID,
		&receipt.OwnerSource,
		&receipt.Model,
		&receipt.InputTokens,
		&receipt.OutputTokens,
		&receipt.CachedTokens,
		&receipt.CostUSDMicros,
		&receipt.RateTableSnapshotID,
		&receipt.SignerFingerprint,
		&receipt.SignedHash,
		&receipt.CreatedAt,
		&receipt.ValidationState,
		&receipt.Verdict,
		&adjustments,
		&receipt.ReceiptSequence,
	); err != nil {
		if errors.Is(err, noRows) {
			return nil, ErrReceiptNotFound
		}
		return nil, err
	}
	receipt.RequestID = rawRequestID
	receipt.ValidationState = NormalizeReceiptValidationState(receipt.ValidationState)
	receipt.Verdict = NormalizeReceiptVerdict(receipt.Verdict)
	receipt.AdjustmentRefs = decodeReceiptAdjustmentRefs(adjustments)
	return &receipt, nil
}

func decodeReceiptAdjustmentRefs(raw []byte) []string {
	var refs []string
	if len(raw) > 0 && json.Unmarshal(raw, &refs) == nil {
		return normalizedAdjustmentRefs(refs)
	}
	return []string{}
}
