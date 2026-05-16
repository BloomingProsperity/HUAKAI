package voucher

import (
	"errors"
	"time"
)

type VoucherStatus string

const (
	StatusActive    VoucherStatus = "active"
	StatusExpired   VoucherStatus = "expired"
	StatusExhausted VoucherStatus = "exhausted"
	StatusRevoked   VoucherStatus = "revoked"
)

type BatchStatus string

const (
	BatchStatusActive    BatchStatus = "active"
	BatchStatusCompleted BatchStatus = "completed"
	BatchStatusFailed    BatchStatus = "failed"
	BatchStatusRevoked   BatchStatus = "revoked"
)

var (
	ErrInvalidInput         = errors.New("voucher: invalid input")
	ErrVoucherNotFound      = errors.New("voucher: not found")
	ErrVoucherDuplicate     = errors.New("voucher: duplicate code")
	ErrVoucherInactive      = errors.New("voucher: inactive")
	ErrVoucherNotYetValid   = errors.New("voucher: not yet valid")
	ErrVoucherExpired       = errors.New("voucher: expired")
	ErrVoucherExhausted     = errors.New("voucher: exhausted")
	ErrVoucherRevoked       = errors.New("voucher: revoked")
	ErrVoucherWrongUser     = errors.New("voucher: wrong user")
	ErrAlreadyRedeemed      = errors.New("voucher: already redeemed by user")
	ErrIdempotencyConflict  = errors.New("voucher: idempotency conflict")
	ErrBurstLimited         = errors.New("voucher: burst limited")
	ErrStoreNotConfigured   = errors.New("voucher: store not configured")
	ErrAuditCodeLeakBlocked = errors.New("voucher: audit payload contains raw code field")
)

type Voucher struct {
	ID               int64         `json:"id"`
	TenantID         int64         `json:"tenant_id"`
	BatchID          *int64        `json:"batch_id,omitempty"`
	CodeFingerprint  string        `json:"code_fingerprint"`
	CodeHash         []byte        `json:"-"`
	AmountCents      int64         `json:"amount_cents"`
	CurrencyCode     string        `json:"currency_code"`
	ValidFrom        time.Time     `json:"valid_from"`
	ValidUntil       time.Time     `json:"valid_until"`
	MaxRedemptions   int           `json:"max_redemptions"`
	RedeemedCount    int           `json:"redeemed_count"`
	SingleUsePerUser bool          `json:"single_use_per_user"`
	EligibleUserID   *int64        `json:"eligible_user_id,omitempty"`
	Status           VoucherStatus `json:"status"`
	CreatedByAdminID int64         `json:"created_by_admin_id,omitempty"`
	RevokedByAdminID int64         `json:"revoked_by_admin_id,omitempty"`
	RevokedReason    string        `json:"revoked_reason,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	RevokedAt        *time.Time    `json:"revoked_at,omitempty"`
}

type Redemption struct {
	ID               int64     `json:"id"`
	TenantID         int64     `json:"tenant_id"`
	VoucherID        int64     `json:"voucher_id"`
	UserID           int64     `json:"user_id"`
	IdempotencyKey   string    `json:"idempotency_key,omitempty"`
	AmountCents      int64     `json:"amount_cents"`
	CurrencyCode     string    `json:"currency_code"`
	SingleUsePerUser bool      `json:"single_use_per_user"`
	SourceIPHash     string    `json:"source_ip_hash,omitempty"`
	RequestID        string    `json:"request_id,omitempty"`
	BillingEventID   int64     `json:"billing_event_id,omitempty"`
	RedeemedAt       time.Time `json:"redeemed_at"`
	CodeFingerprint  string    `json:"code_fingerprint,omitempty"`
}

type Batch struct {
	ID               int64       `json:"id"`
	TenantID         int64       `json:"tenant_id"`
	CreatedByAdminID int64       `json:"created_by_admin_id,omitempty"`
	RequestedCount   int         `json:"requested_count"`
	CreatedCount     int         `json:"created_count"`
	AmountCents      int64       `json:"amount_cents"`
	CurrencyCode     string      `json:"currency_code"`
	ValidFrom        time.Time   `json:"valid_from"`
	ValidUntil       time.Time   `json:"valid_until"`
	MaxRedemptions   int         `json:"max_redemptions"`
	SingleUsePerUser bool        `json:"single_use_per_user"`
	Status           BatchStatus `json:"status"`
	CreatedAt        time.Time   `json:"created_at"`
}

type BillingEvent struct {
	ID           int64     `json:"id"`
	TenantID     int64     `json:"tenant_id"`
	EventType    string    `json:"event_type"`
	RedemptionID int64     `json:"redemption_id"`
	VoucherID    int64     `json:"voucher_id"`
	UserID       int64     `json:"user_id"`
	AmountCents  int64     `json:"amount_cents"`
	Fingerprint  string    `json:"fingerprint"`
	OccurredAt   time.Time `json:"occurred_at"`
}

type CreateInput struct {
	TenantID         int64
	AdminID          int64
	Code             string
	AmountCents      int64
	CurrencyCode     string
	ValidFrom        time.Time
	ValidUntil       time.Time
	MaxRedemptions   int
	SingleUsePerUser bool
	EligibleUserID   *int64
	Now              time.Time
}

type CreateResult struct {
	Voucher Voucher `json:"voucher"`
	Code    string  `json:"code,omitempty"`
}

type BatchCreateInput struct {
	TenantID         int64
	AdminID          int64
	Count            int
	AmountCents      int64
	CurrencyCode     string
	ValidFrom        time.Time
	ValidUntil       time.Time
	MaxRedemptions   int
	SingleUsePerUser bool
	EligibleUserID   *int64
	Now              time.Time
}

type BatchCreateResult struct {
	Batch    Batch         `json:"batch"`
	Vouchers []Voucher     `json:"vouchers"`
	Codes    []CreatedCode `json:"codes"`
}

type CreatedCode struct {
	VoucherID       int64  `json:"voucher_id"`
	Code            string `json:"code"`
	CodeFingerprint string `json:"code_fingerprint"`
}

type RedeemInput struct {
	TenantID       int64
	UserID         int64
	Code           string
	IdempotencyKey string
	SourceIP       string
	RequestID      string
	Now            time.Time
}

type RedeemResult struct {
	Voucher      Voucher    `json:"voucher"`
	Redemption   Redemption `json:"redemption"`
	BalanceCents int64      `json:"balance_cents"`
	Idempotent   bool       `json:"idempotent"`
}

type RevokeInput struct {
	TenantID int64
	ID       int64
	AdminID  int64
	Reason   string
	Now      time.Time
}

type ListInput struct {
	TenantID int64
	Limit    int
}

type GetBatchResult struct {
	Batch    Batch     `json:"batch"`
	Vouchers []Voucher `json:"vouchers"`
}
