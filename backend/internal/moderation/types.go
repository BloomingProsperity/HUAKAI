package moderation

import (
	"context"
	"time"
)

type Decision string

const (
	DecisionPass          Decision = "pass"
	DecisionBlockKeyword  Decision = "block_keyword"
	DecisionBlockHash     Decision = "block_hash"
	DecisionBlockExternal Decision = "block_external"
	DecisionBlockBackend  Decision = "block_backend"
)

type ScreenRequest struct {
	TenantID      int64
	APIKeyID      int64
	UserID        int64
	RequestID     string
	PayloadHash   string
	Body          []byte
	ImageDataURLs []string
	// TailRole 是请求体最后一条消息的角色(gatewayhttp 按客户端协议解析;
	// 空 = 未知/不可解析,行为不变)。Agent 工具循环每轮重发整段对话,
	// 尾消息非 user 时:拦截判定照旧(防绕过),但跳过 auto-ban 重复计数
	// 与 clean 审计噪音(DM-16)。
	TailRole string
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
}

type ModerationLog struct {
	ID               int64
	TenantID         int64
	APIKeyID         int64
	UserID           int64
	RequestID        string
	PayloadHash      string
	Decision         Decision
	ReasonCode       string
	MatchedKeywordID *int64
	MatchedHashID    *int64
	OccurredAt       time.Time
}

type BannedAPIKey struct {
	ID              int64
	TenantID        int64
	UserID          int64
	Name            string
	KeyPrefix       string
	Status          string
	ViolationCount  int64
	LastViolationAt time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type UnbanAPIKeyRequest struct {
	TenantID int64
	APIKeyID int64
	ActorID  string
	Reason   string
}

type UnbanAPIKeyResult struct {
	APIKeyID   int64
	TenantID   int64
	Status     string
	AuditLogID int64
	UpdatedAt  time.Time
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

const BulkImportMaxItems = 1000

type BulkCreateKeywordItem struct {
	Keyword    string
	ReasonCode string
	Enabled    bool
}

type BulkCreateHashItem struct {
	HashHex    string
	ReasonCode string
	Enabled    bool
}

type BulkCreateKeywordsRequest struct {
	TenantID  int64
	Items     []BulkCreateKeywordItem
	UpdatedBy string
}

type BulkCreateHashesRequest struct {
	TenantID  int64
	Items     []BulkCreateHashItem
	UpdatedBy string
}

type BulkItemError struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

type BulkCreateResult struct {
	Accepted         int             `json:"accepted"`
	SkippedDuplicate int             `json:"skipped_duplicate"`
	Errors           []BulkItemError `json:"errors"`
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
