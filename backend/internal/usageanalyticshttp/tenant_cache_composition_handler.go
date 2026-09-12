package usageanalyticshttp

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	dboverview "github.com/BloomingProsperity/HUAKAI/internal/db/usageoverview"
)

// TenantCacheCompositionQuerier 只读同一租户、同一窗口的合计与缓存构成。
// 不并入平台总览 Querier，避免所有 stub 被强迫实现新方法。
type TenantCacheCompositionQuerier interface {
	AggregateTenantUsageOverviewTotals(context.Context, dboverview.AggregateTenantUsageOverviewTotalsParams) (dboverview.AggregateTenantUsageOverviewTotalsRow, error)
	AggregateTenantUsageCacheComposition(context.Context, dboverview.AggregateTenantUsageCacheCompositionParams) (dboverview.AggregateTenantUsageCacheCompositionRow, error)
}

type cacheComposition struct {
	PromptCacheCreationTokens int64  `json:"prompt_cache_creation_tokens"`
	PromptCacheReadTokens     int64  `json:"prompt_cache_read_tokens"`
	PromptCacheCreationCost   string `json:"prompt_cache_creation_cost"`
	PromptCacheReadCost       string `json:"prompt_cache_read_cost"`
	ResponseCacheHits         int64  `json:"response_cache_hits"`
	ResponseCacheCost         string `json:"response_cache_cost"`
	UpstreamRequests          int64  `json:"upstream_requests"`
	Requests                  int64  `json:"requests"`
	ResponseCacheHitRate      string `json:"response_cache_hit_rate"`
}

type cacheCompositionResponse struct {
	TenantID    int64            `json:"tenant_id"`
	Window      string           `json:"window"`
	Totals      overviewTotals   `json:"totals"`
	Composition cacheComposition `json:"composition"`
}

// NewTenantCacheCompositionHandler 提供 GET /admin/v1/usage/cache-composition。
// 同一窗口分栏：上游提示缓存写/读 Token 与费用，以及 L2 响应缓存命中次数与费用。
// 租户管理员锁定认证租户；部署者必须显式 tenant_id。不把 L2 草稿 Token 折进提示缓存。
func NewTenantCacheCompositionHandler(auth TenantOverviewAuth, q TenantCacheCompositionQuerier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "analytics dependency unset")
			return
		}
		ident, err := auth.Resolve(r.Context(), r)
		if err != nil {
			writeTenantOverviewAdminError(w, err)
			return
		}
		tenantID, ok := tenantOverviewTenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		if strings.TrimSpace(r.URL.Query().Get("timezone")) != "" {
			writeJSONError(w, http.StatusBadRequest, "timezone_not_supported", "composition window is UTC only")
			return
		}
		query, ok := parseOverviewQuery(w, r.URL, time.Now().UTC())
		if !ok {
			return
		}
		if q == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "analytics dependency unset")
			return
		}
		value, hit, err := GetOrLoad(tenantCacheCompositionSnapshotCacheKey(tenantID, query), overviewSnapshotTTL, func() (any, error) {
			return loadTenantCacheCompositionResponse(r.Context(), q, tenantID, query)
		})
		if err != nil {
			w.Header().Set(snapshotCacheHeader, "miss")
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		response, ok := value.(cacheCompositionResponse)
		if !ok {
			w.Header().Set(snapshotCacheHeader, "miss")
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		if hit {
			w.Header().Set(snapshotCacheHeader, "hit")
		} else {
			w.Header().Set(snapshotCacheHeader, "miss")
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func loadTenantCacheCompositionResponse(ctx context.Context, q TenantCacheCompositionQuerier, tenantID int64, query overviewQuery) (cacheCompositionResponse, error) {
	totalsRow, err := q.AggregateTenantUsageOverviewTotals(ctx, dboverview.AggregateTenantUsageOverviewTotalsParams{
		TenantID:     tenantID,
		SettledSince: query.settledSince,
	})
	if err != nil {
		return cacheCompositionResponse{}, err
	}
	compRow, err := q.AggregateTenantUsageCacheComposition(ctx, dboverview.AggregateTenantUsageCacheCompositionParams{
		TenantID:     tenantID,
		SettledSince: query.settledSince,
	})
	if err != nil {
		return cacheCompositionResponse{}, err
	}
	totals, err := overviewTotalsFromRow(tenantOverviewTotalsToPlatform(totalsRow))
	if err != nil {
		return cacheCompositionResponse{}, err
	}
	composition, err := cacheCompositionFromRow(compRow)
	if err != nil {
		return cacheCompositionResponse{}, err
	}
	return cacheCompositionResponse{
		TenantID:    tenantID,
		Window:      query.windowLabel,
		Totals:      totals,
		Composition: composition,
	}, nil
}

func cacheCompositionFromRow(row dboverview.AggregateTenantUsageCacheCompositionRow) (cacheComposition, error) {
	createCost, err := fixedMoneyText(row.PromptCacheCreationCost)
	if err != nil {
		return cacheComposition{}, err
	}
	readCost, err := fixedMoneyText(row.PromptCacheReadCost)
	if err != nil {
		return cacheComposition{}, err
	}
	hitCost, err := fixedMoneyText(row.ResponseCacheCost)
	if err != nil {
		return cacheComposition{}, err
	}
	return cacheComposition{
		PromptCacheCreationTokens: row.PromptCacheCreationTokens,
		PromptCacheReadTokens:     row.PromptCacheReadTokens,
		PromptCacheCreationCost:   createCost,
		PromptCacheReadCost:       readCost,
		ResponseCacheHits:         row.ResponseCacheHits,
		ResponseCacheCost:         hitCost,
		UpstreamRequests:          row.UpstreamRequests,
		Requests:                  row.RequestCount,
		ResponseCacheHitRate:      successRateText(row.ResponseCacheHits, row.RequestCount),
	}, nil
}

func tenantCacheCompositionSnapshotCacheKey(tenantID int64, query overviewQuery) string {
	return "admin_tenant_usage_cache_composition:v1|tenant=" + strconv.FormatInt(tenantID, 10) + "|window=" + query.windowLabel
}
