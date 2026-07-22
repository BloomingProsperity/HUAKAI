// 基于数据库的账号来源，把池组维度的 sqlc 查询转换为选号快照。
//
// LoadRate 计算为 in_flight_count / cap_concurrency。容量为零的容量行
// 被当作 load=1.0(由上游 gate 排除,而非悄悄触发除零)。

package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// DBAccountSource 基于 ListEligibleAccountsByPoolGroup sqlc 查询实现
// AccountSource。
type DBAccountSource struct {
	q *dbbilling.Queries
}

// NewDBAccountSource 从 sqlc 查询句柄构造适配器。
func NewDBAccountSource(q *dbbilling.Queries) *DBAccountSource {
	return &DBAccountSource{q: q}
}

// ListAccounts 实现 AccountSource。
func (s *DBAccountSource) ListAccounts(ctx context.Context, req SelectionRequest) ([]*AccountSnapshot, error) {
	if s == nil || s.q == nil {
		return nil, fmt.Errorf("pool: DBAccountSource not configured")
	}
	// 账号白名单和额度事实描述的是最终发往上游的模型名，不能拿客户端公开别名
	// 过滤；公开别名仍由 RequestedModel 承担 sticky 与租户路由策略语义。
	providerModelID := strings.TrimSpace(req.ProviderModelID)
	if providerModelID == "" {
		providerModelID = req.RequestedModel
	}
	required := req.CapabilityFlags
	if required == nil {
		required = []string{}
	}
	rows, err := s.q.ListEligibleAccountsByPoolGroup(ctx, dbbilling.ListEligibleAccountsByPoolGroupParams{
		TenantID:                req.TenantID,
		PoolGroupID:             req.PoolGroupID,
		RequestedModel:          providerModelID,
		RequestedProtocolFamily: req.ProtocolFamily,
		RequiredCapabilities:    required,
	})
	if err != nil {
		return nil, fmt.Errorf("pool: list eligible accounts: %w", err)
	}
	out := make([]*AccountSnapshot, 0, len(rows))
	for _, r := range rows {
		modelRateLimits, err := decodeModelRateLimits(r.ModelRateLimits)
		if err != nil {
			return nil, err
		}
		// 生产快照不投影 provider_accounts.cap_quota_* 历史列；保留状态与启用缺链
		// 见 docs/architecture/deprecated-schema.md。
		snap := &AccountSnapshot{
			ID:                r.ID,
			TenantID:          r.TenantID,
			ProtocolFamily:    r.UpstreamProtocol,
			Priority:          int(r.Priority),
			Weight:            r.StaticWeight,
			UpstreamCostRatio: r.UpstreamCostRatio,
			MaxConcurrency:    int(r.CapConcurrency),
			LoadRate:          loadRate(r.InFlightCount, r.CapConcurrency),
			// MaxWaiting 供给 selector 的 WaitPlan 回落路径
			//(selector.go fallbackWaitPlan)。cap_queue_fallback 是
			// 每账号在回落队列长度上的上限。
			MaxWaiting: int(r.CapQueueFallback),
			// WaitTimeoutMS 保持 0 —— 当设置了 policy 时,selector 会用
			// RoutingPolicy.FallbackTimeoutMS 覆盖它。
			// 每账号的超时 override 是 Phase E 的精化项。
			HealthState:                 r.HealthState,
			ModelRateLimits:             modelRateLimits,
			WindowCostLimitCents:        r.WindowCostLimitCents,
			MaxSessions:                 int(r.MaxSessions),
			DisableCooling:              r.DisableCooling,
			RPMLimit:                    r.RpmLimit,
			TPMLimit:                    r.TpmLimit,
			SuccessEWMA:                 float64Value(r.SuccessEwma),
			ErrorEWMA:                   float64Value(r.ErrorEwma),
			ResponseLatencyMSEWMA:       float64Value(r.ResponseLatencyMsEwma),
			RoutingSignalSampleCount:    int64Value(r.RoutingSignalSampleCount),
			UpstreamQuotaState:          r.UpstreamQuotaState,
			UpstreamQuotaRemainingKnown: r.UpstreamQuotaRemainingKnown,
			UpstreamQuotaRemaining:      r.UpstreamQuotaRemainingPercent,
		}
		if r.LastDispatchAt.Valid {
			snap.LastUsedAt = r.LastDispatchAt.Time
		}
		if r.HealthStateUntil.Valid {
			snap.HealthStateUntil = r.HealthStateUntil.Time
		}
		if r.RoutingSignalObservedAt.Valid {
			snap.RoutingSignalObservedAt = r.RoutingSignalObservedAt.Time.UTC()
		}
		if r.UpstreamQuotaResetsAt.Valid {
			snap.UpstreamQuotaResetsAt = r.UpstreamQuotaResetsAt.Time.UTC()
		}
		if r.UpstreamQuotaObservedAt.Valid {
			snap.UpstreamQuotaObservedAt = r.UpstreamQuotaObservedAt.Time.UTC()
		}
		out = append(out, snap)
	}
	return out, nil
}

func float64Value(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

type modelRateLimitJSON struct {
	RateLimitResetAt string `json:"rate_limit_reset_at"`
	Reason           string `json:"reason"`
}

func decodeModelRateLimits(raw []byte) (map[string]ModelRateLimit, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var payload map[string]modelRateLimitJSON
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("pool: decode model rate limits: %w", err)
	}
	if len(payload) == 0 {
		return nil, nil
	}
	out := make(map[string]ModelRateLimit, len(payload))
	for key, entry := range payload {
		key = strings.TrimSpace(key)
		resetRaw := strings.TrimSpace(entry.RateLimitResetAt)
		if key == "" || resetRaw == "" {
			continue
		}
		resetAt, err := time.Parse(time.RFC3339Nano, resetRaw)
		if err != nil {
			continue
		}
		out[key] = ModelRateLimit{
			RateLimitResetAt: resetAt,
			Reason:           entry.Reason,
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// loadRate 把 in-flight 计数映射为 [0,1] 的负载比例。容量为零的账号
// 被报告为 load=1,从而让上游 gate 把它们排除,而不是触发除零。
func loadRate(inFlight, cap int32) float64 {
	if cap <= 0 {
		return 1.0
	}
	if inFlight <= 0 {
		return 0.0
	}
	if inFlight >= cap {
		return 1.0
	}
	return float64(inFlight) / float64(cap)
}

var _ AccountSource = (*DBAccountSource)(nil)
