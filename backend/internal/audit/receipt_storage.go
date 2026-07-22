package audit

import (
	"context"
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

// ReceiptOwner 记录用户归属行，必须与每条消费回执在同一事务内写入。
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

func validateReceiptForStorage(receipt *CostReceipt) error {
	if err := validateReceiptForSigning(receipt); err != nil {
		return err
	}
	hasFingerprint := len(receipt.SignerFingerprint) > 0
	hasSignature := len(receipt.SignedHash) > 0
	if hasFingerprint != hasSignature {
		return fmt.Errorf("%w: receipt signature fields must be both present or both empty", ErrReceiptInvalidDerivedData)
	}
	return nil
}

func receiptBytea(value []byte) []byte {
	if len(value) == 0 {
		return []byte{}
	}
	return append([]byte(nil), value...)
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
