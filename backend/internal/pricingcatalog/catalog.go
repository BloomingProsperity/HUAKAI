package pricingcatalog

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

var (
	ErrNotFound           = errors.New("pricingcatalog: not found")
	ErrBackend            = errors.New("pricingcatalog: backend error")
	ErrInvalidInput       = errors.New("pricingcatalog: invalid input")
	ErrAuditSignerMissing = errors.New("pricingcatalog: audit signer missing")
	ErrAuditTxMissing     = errors.New("pricingcatalog: audit transaction missing")
)

type GroupPricingRatio struct {
	ID          int64
	TenantID    int64
	PoolGroupID int64
	Ratio       decimal.Decimal
	RatioText   string
	PublicRatio bool
	CreatedBy   string
	UpdatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (r GroupPricingRatio) RatioString() string {
	if strings.TrimSpace(r.RatioText) != "" {
		return r.RatioText
	}
	return r.Ratio.String()
}

type UpsertRatioParams struct {
	TenantID    int64
	PoolGroupID int64
	Ratio       decimal.Decimal
	PublicRatio bool
	Actor       string
	ActorRole   string
}

type DeleteRatioParams struct {
	TenantID    int64
	PoolGroupID int64
	Actor       string
	ActorRole   string
}

type Store interface {
	GetRatio(ctx context.Context, tenantID, poolGroupID int64) (GroupPricingRatio, error)
	ListRatios(ctx context.Context, tenantID int64) ([]GroupPricingRatio, error)
	UpsertRatio(ctx context.Context, p UpsertRatioParams) (GroupPricingRatio, error)
	DeleteRatio(ctx context.Context, p DeleteRatioParams) error
}
