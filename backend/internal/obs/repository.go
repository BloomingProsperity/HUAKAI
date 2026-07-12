// Package obs 是 HUAKAI ledger 的只读审计/观测面。
// 遵循 docs/specs/_invariants/cross-module-boundaries.md。
// 本包不写入任何数据 —— 每个方法都是 SELECT。
// 由于底层 SQL 从不 select 凭证列，因此保证凭证永远不会出现在返回的行中。
//
// Reader 为 admin 端点渲染 usage、claim、audit 与 billing-event 列表。
// 集成测试会跑通 SQL 支撑的实现。

package obs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// Reader 是对外的读取 API。所有方法都按租户隔离 ——
// tenantID 参数在服务端通过 SQL WHERE 强制生效；本接口无法进行
// 跨租户读取。
type Reader interface {
	ListUsage(ctx context.Context, tenantID int64, page Page) ([]UsageRow, error)
	GetClaim(ctx context.Context, tenantID, claimID int64) (ClaimRow, error)
	ListBillingEvents(ctx context.Context, tenantID int64, eventTypeFilter string, page Page) ([]BillingEventRow, error)
	CountClaimsByStatus(ctx context.Context, tenantID int64) (map[string]int64, error)
}

// Page 是标准分页参数。Limit 被钳制到 [1, 500]；
// offset 必须为非负数。
type Page struct {
	Limit  int32
	Offset int32
}

// ErrNotFound 在 (tenantID, claimID) 元组没有对应行时由 GetClaim 返回 ——
// 要么该记录不存在，要么它属于另一个租户。
// 我们把两种情况都归并为 ErrNotFound，使得跨租户探测与
// “不存在”无法区分。
var ErrNotFound = errors.New("obs: not found")

// UsageRow 是 usage_records 中的一行，其中承载凭证和密钥的列被刻意
// 排除在外。时间戳被暴露出来以便按时间顺序进行审计排序。
type UsageRow struct {
	ID                     int64
	TenantID               int64
	ClaimID                int64
	APIKeyID               int64
	UserID                 int64
	ProviderAccountID      int64
	AttemptSeq             int32
	TokensInput            int32
	TokensOutput           int32
	CacheCreationTokens    int32
	CacheReadTokens        int32
	ActualCost             decimal.Decimal
	EndClass               string
	UsageSource            string
	PendingReconciliation  bool
	StreamState            int16
	DeliveredTokenCount    int64
	StreamTerminatedReason string
	RequestedModel         string
	UpstreamModel          string // DB 中为 null 时取空字符串
	Stream                 bool
	RequestedAt            time.Time  // gateway 收到请求的时间
	SettledAt              *time.Time // Tx2 提交的时间；nil 表示仍在处理中
}

// ClaimRow 是一行 billing_ledger_claims 的审计模式视图。
// ReservedAt 是必填的（schema 中为 NOT NULL）；当 claim 仍处于
// 'reserving' 状态时 SettledAt 为 nil。
type ClaimRow struct {
	ID                   int64
	TenantID             int64
	APIKeyID             int64
	UserID               int64
	LogicalRequestID     string
	EndpointFamily       string
	RequestedModel       string
	PoolingGroupID       int64 // 为 null 时取 0
	BillingPolicyVersion string
	RequestClass         string
	ProviderAccountID    int64 // 为 null 时取 0（acquire 之前）
	AttemptSeq           int32
	PredictedCost        decimal.Decimal
	ActualCost           decimal.Decimal // status='reserving' 时为零
	CurrencyCode         string
	Status               string
	AbortedReason        string // 未中止时为空
	RequestFingerprint   string
	ReservedAt           time.Time
	SettledAt            *time.Time
}

// BillingEventRow 是审计级别的事件视图。OccurredAt 是审计列表的
// 标准时间顺序键。
type BillingEventRow struct {
	ID                     int64
	TenantID               int64
	ClaimID                int64
	EventType              string
	ActualCost             decimal.Decimal
	ActualCostSigned       decimal.Decimal
	EndClass               string
	UsageSource            string
	StreamState            int16
	DeliveredTokenCount    int64
	StreamTerminatedReason string
	Fingerprint            string
	OccurredAt             time.Time
}

// PgxReader 是由 sqlc.Queries 支撑的 Reader。请通过 NewPgxReader 构造。
type PgxReader struct {
	q *dbbilling.Queries
}

// NewPgxReader 包装一个 sqlc.Queries 句柄。传入由 pgxpool.Pool 派生的
// *dbbilling.Queries；连接池的生命周期由调用方管理。
func NewPgxReader(q *dbbilling.Queries) *PgxReader {
	return &PgxReader{q: q}
}

// ListUsage 实现 Reader。
func (r *PgxReader) ListUsage(ctx context.Context, tenantID int64, page Page) ([]UsageRow, error) {
	limit, offset := normalizePage(page)
	rows, err := r.q.ListUsageByTenant(ctx, dbbilling.ListUsageByTenantParams{
		TenantID:   tenantID,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("obs: list usage: %w", err)
	}
	out := make([]UsageRow, 0, len(rows))
	for _, row := range rows {
		u := UsageRow{
			ID:                    row.ID,
			TenantID:              row.TenantID,
			ClaimID:               row.ClaimID,
			APIKeyID:              row.APIKeyID,
			UserID:                row.UserID,
			AttemptSeq:            row.AttemptSeq,
			TokensInput:           row.TokensInput,
			TokensOutput:          row.TokensOutput,
			CacheCreationTokens:   row.CacheCreationTokens,
			CacheReadTokens:       row.CacheReadTokens,
			ActualCost:            row.ActualCost,
			EndClass:              row.EndClass,
			UsageSource:           row.UsageSource,
			PendingReconciliation: row.PendingReconciliation,
			StreamState:           row.StreamState,
			DeliveredTokenCount:   row.DeliveredTokenCount,
			RequestedModel:        row.RequestedModel,
			Stream:                row.Stream,
		}
		// provider_account_id migration 0043 起可空 (L2 缓存命中行无上游账号);
		// 沿用本仓 obs 既有约定 0 表示 null。
		if row.ProviderAccountID != nil {
			u.ProviderAccountID = *row.ProviderAccountID
		}
		if row.StreamTerminatedReason != nil {
			u.StreamTerminatedReason = *row.StreamTerminatedReason
		}
		if row.UpstreamModel != nil {
			u.UpstreamModel = *row.UpstreamModel
		}
		if row.RequestedAt.Valid {
			u.RequestedAt = row.RequestedAt.Time
		}
		if row.SettledAt.Valid {
			t := row.SettledAt.Time
			u.SettledAt = &t
		}
		out = append(out, u)
	}
	return out, nil
}

// GetClaim 实现 Reader。
func (r *PgxReader) GetClaim(ctx context.Context, tenantID, claimID int64) (ClaimRow, error) {
	row, err := r.q.GetClaimByID(ctx, dbbilling.GetClaimByIDParams{ID: claimID, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimRow{}, ErrNotFound
	}
	if err != nil {
		return ClaimRow{}, fmt.Errorf("obs: get claim: %w", err)
	}
	c := ClaimRow{
		ID:                   row.ID,
		TenantID:             row.TenantID,
		APIKeyID:             row.APIKeyID,
		UserID:               row.UserID,
		LogicalRequestID:     row.LogicalRequestID,
		EndpointFamily:       row.EndpointFamily,
		RequestedModel:       row.RequestedModel,
		BillingPolicyVersion: row.BillingPolicyVersion,
		RequestClass:         row.RequestClass,
		AttemptSeq:           row.AttemptSeq,
		PredictedCost:        row.PredictedCost,
		CurrencyCode:         row.CurrencyCode,
		Status:               row.Status,
		RequestFingerprint:   row.RequestFingerprint,
	}
	if row.PoolingGroupID != nil {
		c.PoolingGroupID = *row.PoolingGroupID
	}
	if row.ProviderAccountID != nil {
		c.ProviderAccountID = *row.ProviderAccountID
	}
	if row.ActualCost.Valid {
		c.ActualCost = row.ActualCost.Decimal
	}
	if row.AbortedReason != nil {
		c.AbortedReason = *row.AbortedReason
	}
	if row.ReservedAt.Valid {
		c.ReservedAt = row.ReservedAt.Time
	}
	if row.SettledAt.Valid {
		t := row.SettledAt.Time
		c.SettledAt = &t
	}
	return c, nil
}

// ListBillingEvents 实现 Reader。eventTypeFilter="" 表示不过滤。
func (r *PgxReader) ListBillingEvents(ctx context.Context, tenantID int64, eventTypeFilter string, page Page) ([]BillingEventRow, error) {
	limit, offset := normalizePage(page)
	rows, err := r.q.ListBillingEventsByTenant(ctx, dbbilling.ListBillingEventsByTenantParams{
		TenantID:        tenantID,
		EventTypeFilter: eventTypeFilter,
		PageLimit:       limit,
		PageOffset:      offset,
	})
	if err != nil {
		return nil, fmt.Errorf("obs: list billing_events: %w", err)
	}
	out := make([]BillingEventRow, 0, len(rows))
	for _, row := range rows {
		e := BillingEventRow{
			ID:                  row.ID,
			TenantID:            row.TenantID,
			ClaimID:             nullableInt64Value(row.ClaimID),
			EventType:           row.EventType,
			ActualCost:          row.ActualCost,
			ActualCostSigned:    row.ActualCostSigned,
			Fingerprint:         row.Fingerprint,
			StreamState:         row.StreamState,
			DeliveredTokenCount: row.DeliveredTokenCount,
		}
		if row.StreamTerminatedReason != nil {
			e.StreamTerminatedReason = *row.StreamTerminatedReason
		}
		if row.EndClass != nil {
			e.EndClass = *row.EndClass
		}
		if row.UsageSource != nil {
			e.UsageSource = *row.UsageSource
		}
		if row.OccurredAt.Valid {
			e.OccurredAt = row.OccurredAt.Time
		}
		out = append(out, e)
	}
	return out, nil
}

// CountClaimsByStatus 实现 Reader 接口。
func (r *PgxReader) CountClaimsByStatus(ctx context.Context, tenantID int64) (map[string]int64, error) {
	rows, err := r.q.CountClaimsByStatus(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("obs: count claims by status: %w", err)
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Status] = r.ClaimCount
	}
	return out, nil
}

func normalizePage(p Page) (int32, int32) {
	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := p.Offset
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func nullableInt64Value(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

var _ Reader = (*PgxReader)(nil)
