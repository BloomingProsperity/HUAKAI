package moderation

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

type Decision string

const (
	DecisionPass         Decision = "pass"
	DecisionBlockKeyword Decision = "block_keyword"
	DecisionBlockHash    Decision = "block_hash"
	DecisionBlockBackend Decision = "block_backend"
	DecisionFeeCharged   Decision = "fee_charged"
)

type ScreenRequest struct {
	TenantID    int64
	APIKeyID    int64
	UserID      int64
	RequestID   string
	PayloadHash string
	Body        []byte
}

type ScreenResult struct {
	Decision         Decision
	ReasonCode       string
	MatchedKeywordID *int64
	MatchedHashID    *int64
}

type KeywordRule struct {
	ID         int64
	TenantID   int64
	Keyword    string
	ReasonCode string
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type HashMatch struct {
	Matched    bool
	ID         int64
	ReasonCode string
}

type ModerationConfig struct {
	TenantID         int64
	Enabled          bool
	FailClosed       bool
	SampleRatePct    int32
	BanThreshold     int32
	BanWindowSeconds int32
	ViolationFeeUSD  decimal.Decimal
	UpdatedBy        string
	UpdatedAt        time.Time
}

func DefaultConfig(tenantID int64) ModerationConfig {
	return ModerationConfig{
		TenantID:         tenantID,
		Enabled:          false,
		FailClosed:       true,
		SampleRatePct:    100,
		BanThreshold:     3,
		BanWindowSeconds: 3600,
		ViolationFeeUSD:  decimal.Zero,
	}
}

type ModerationEvent struct {
	TenantID         int64
	APIKeyID         int64
	UserID           int64
	RequestID        string
	PayloadHash      string
	Decision         Decision
	ReasonCode       string
	MatchedKeywordID *int64
	MatchedHashID    *int64
	ViolationFeeUSD  decimal.Decimal
	BillingEventID   *int64
}

type CreateKeywordRequest struct {
	TenantID   int64
	Keyword    string
	ReasonCode string
	Enabled    bool
	UpdatedBy  string
}

type ConfigStore interface {
	GetConfig(context.Context, int64) (ModerationConfig, error)
}

type KeywordStore interface {
	ListEnabled(context.Context, int64) ([]KeywordRule, error)
}

type HashStore interface {
	Contains(context.Context, int64, string) (HashMatch, error)
}

type AuditLogger interface {
	Log(context.Context, ModerationEvent, ModerationConfig) error
}

type Screener interface {
	Screen(context.Context, ScreenRequest) (ScreenResult, error)
}
