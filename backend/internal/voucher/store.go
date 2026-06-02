package voucher

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Store interface {
	CreateVoucher(context.Context, createVoucherRecord) (Voucher, error)
	CreateBatch(context.Context, createBatchRecord, []createVoucherRecord) (Batch, []Voucher, error)
	ListVouchers(context.Context, ListInput) ([]Voucher, error)
	GetBatch(context.Context, int64, int64) (GetBatchResult, error)
	RevokeVoucher(context.Context, RevokeInput) (Voucher, error)
	Redeem(context.Context, redeemRecord) (RedeemResult, error)
	BillingEvents(context.Context, int64, int64) ([]BillingEvent, error)
}

type createVoucherRecord struct {
	TenantID         int64
	BatchID          *int64
	AdminID          int64
	CodeHash         []byte
	CodeFingerprint  string
	AmountCents      int64
	CurrencyCode     string
	ValidFrom        time.Time
	ValidUntil       time.Time
	MaxRedemptions   int
	SingleUsePerUser bool
	EligibleUserID   *int64
	// GrantKind 空=balance (批量路径不设, 由 grantKindOrDefault 兜底); subscription 时
	// SubscriptionPlanID 必非空 (service 层校验 + DB voucher_subscription_kind_check)。
	GrantKind          string
	SubscriptionPlanID *int64
	Now                time.Time
}

type createBatchRecord struct {
	TenantID         int64
	AdminID          int64
	RequestedCount   int
	AmountCents      int64
	CurrencyCode     string
	ValidFrom        time.Time
	ValidUntil       time.Time
	MaxRedemptions   int
	SingleUsePerUser bool
	EligibleUserID   *int64
	Now              time.Time
}

type redeemRecord struct {
	TenantID        int64
	UserID          int64
	CodeHash        []byte
	CodeFingerprint string
	IdempotencyKey  string
	SourceIPHash    string
	RequestID       string
	Now             time.Time
}

func stringKey(parts ...any) string {
	var b strings.Builder
	for i, part := range parts {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(fmt.Sprint(part))
	}
	return b.String()
}
