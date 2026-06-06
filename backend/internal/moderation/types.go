package moderation

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

type Decision string

const (
	DecisionPass          Decision = "pass"
	DecisionBlockKeyword  Decision = "block_keyword"
	DecisionBlockHash     Decision = "block_hash"
	DecisionBlockExternal Decision = "block_external"
	DecisionBlockBackend  Decision = "block_backend"
	DecisionFeeCharged    Decision = "fee_charged"
)

type ScreenRequest struct {
	TenantID      int64
	APIKeyID      int64
	UserID        int64
	RequestID     string
	PayloadHash   string
	Body          []byte
	ImageDataURLs []string
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

type HashRule struct {
	ID         int64
	TenantID   int64
	HashHex    string
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
	External         ExternalModerationConfig
	UpdatedBy        string
	UpdatedAt        time.Time
}

type ExternalModerationConfig struct {
	Enabled      bool
	BaseURL      string
	APIKeys      []string
	Model        string
	Thresholds   map[string]float64
	TimeoutMS    int
	RetryCount   int
	ImageEnabled bool
}

type ExternalModerationResult struct {
	Blocked    bool
	ReasonCode string
	Category   string
	Score      float64
	Threshold  float64
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
		External:         DefaultExternalModerationConfig(),
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

type CreateHashRequest struct {
	TenantID   int64
	HashHex    string
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

type ExternalModerator interface {
	ScreenExternal(context.Context, ScreenRequest, ExternalModerationConfig) (ExternalModerationResult, error)
}

type Screener interface {
	Screen(context.Context, ScreenRequest) (ScreenResult, error)
}
