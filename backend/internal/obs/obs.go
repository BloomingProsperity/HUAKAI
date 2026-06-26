// Package obs 实现 F-OBS-001 可观测性层(Usage Record 查询、
// 审计事件查询、DLQ 重放)。
//
// 与 internal/billing 紧密耦合,用于 Tx2 原子结算。
// 已发布规格见 docs/specs/observability-billing.md。
// 本包在 repository.go 中包含一个由 sqlc 支撑的读侧 Reader。
package obs

import "context"

// Repository 暴露针对 usage_records、billing_ledger_claims、审计事件表
// 以及 usage_record_dlq 的运营者查询操作。
type Repository interface {
	QueryUsageRecords(ctx context.Context, filter UsageFilter) (*UsagePage, error)
	QueryClaims(ctx context.Context, filter ClaimFilter) (*ClaimPage, error)
	QueryAuditEvents(ctx context.Context, filter AuditFilter) (*AuditPage, error)
	ReplayDLQ(ctx context.Context, dlqID int64, actorID string) error
}

// UsageFilter、ClaimFilter、AuditFilter 对应 admin API 的查询参数。
type UsageFilter struct {
	TenantID                  int64
	From, To                  string // RFC3339
	APIKeyID                  int64
	ProviderAccountID         int64
	Model                     string
	PendingReconciliationOnly bool
	Cursor                    string
	Limit                     int
}

type ClaimFilter struct {
	TenantID int64
	Status   string
	From     string
	Cursor   string
	Limit    int
}

type AuditFilter struct {
	TenantID   int64
	EventClass string // pool_routing | rate_limit | oauth_refresh(事件类别)
	ActorID    string
	From       string
	Cursor     string
	Limit      int
}

// PageMeta 参见 docs/openapi/openapi.yaml。
type PageMeta struct {
	Cursor     string `json:"cursor,omitempty"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// UsagePage / ClaimPage / AuditPage 是分页结果信封。
type UsagePage struct {
	Items []byte // 原始 JSON 数组;带类型的形态由 openapi.yaml 生成
	Page  PageMeta
}

type ClaimPage struct {
	Items []byte
	Page  PageMeta
}

type AuditPage struct {
	Items []byte
	Page  PageMeta
}
