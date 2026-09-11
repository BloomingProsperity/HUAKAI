package usageanalyticshttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	dboverview "github.com/BloomingProsperity/HUAKAI/internal/db/usageoverview"
)

// TenantOverviewAuth 从请求派生部署者或租户管理员身份。鉴权失败一律拒，防跨租户读。
type TenantOverviewAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// TenantOverviewQuerier 只读按租户聚合 totals 与日趋势。不并入平台总览 Querier，避免所有 stub 被强迫实现新方法。
type TenantOverviewQuerier interface {
	AggregateTenantUsageOverviewTotals(context.Context, dboverview.AggregateTenantUsageOverviewTotalsParams) (dboverview.AggregateTenantUsageOverviewTotalsRow, error)
	AggregateTenantUsageOverviewTrendByDay(context.Context, dboverview.AggregateTenantUsageOverviewTrendByDayParams) ([]dboverview.AggregateTenantUsageOverviewTrendByDayRow, error)
}

type tenantOverviewResponse struct {
	TenantID int64                `json:"tenant_id"`
	Window   string               `json:"window"`
	Totals   overviewTotals       `json:"totals"`
	Trend    []overviewTrendPoint `json:"trend"`
}

// NewTenantOverviewHandler 提供 GET /admin/v1/usage/overview。
// 租户管理员只用认证上下文本租户；部署者必须显式 tenant_id，省略不得回落全平台。
// Token 分项沿用现有结算列（输入/输出/提示缓存写读/图像），不把 HTTP 快照缓存命中折进提示缓存。
func NewTenantOverviewHandler(auth TenantOverviewAuth, q TenantOverviewQuerier) http.HandlerFunc {
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
		query, ok := parseOverviewQuery(w, r.URL, time.Now().UTC())
		if !ok {
			return
		}
		if q == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "analytics dependency unset")
			return
		}
		value, hit, err := GetOrLoad(tenantOverviewSnapshotCacheKey(tenantID, query), overviewSnapshotTTL, func() (any, error) {
			return loadTenantOverviewResponse(r.Context(), q, tenantID, query)
		})
		if err != nil {
			w.Header().Set(snapshotCacheHeader, "miss")
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		response, ok := value.(tenantOverviewResponse)
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

func loadTenantOverviewResponse(ctx context.Context, q TenantOverviewQuerier, tenantID int64, query overviewQuery) (tenantOverviewResponse, error) {
	totalsRow, err := q.AggregateTenantUsageOverviewTotals(ctx, dboverview.AggregateTenantUsageOverviewTotalsParams{
		TenantID:     tenantID,
		SettledSince: query.settledSince,
	})
	if err != nil {
		return tenantOverviewResponse{}, err
	}
	trendRows, err := q.AggregateTenantUsageOverviewTrendByDay(ctx, dboverview.AggregateTenantUsageOverviewTrendByDayParams{
		TenantID:     tenantID,
		SettledSince: query.settledSince,
	})
	if err != nil {
		return tenantOverviewResponse{}, err
	}
	totals, err := overviewTotalsFromRow(tenantOverviewTotalsToPlatform(totalsRow))
	if err != nil {
		return tenantOverviewResponse{}, err
	}
	trend, err := overviewTrendFromRows(tenantOverviewTrendToPlatform(trendRows))
	if err != nil {
		return tenantOverviewResponse{}, err
	}
	return tenantOverviewResponse{
		TenantID: tenantID,
		Window:   query.windowLabel,
		Totals:   totals,
		Trend:    trend,
	}, nil
}

func tenantOverviewSnapshotCacheKey(tenantID int64, query overviewQuery) string {
	return "admin_tenant_usage_overview:v1|tenant=" + strconv.FormatInt(tenantID, 10) + "|window=" + query.windowLabel
}

func tenantOverviewTenantFromQuery(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if raw == "" && ident.Role == admin.RoleTenantOperator {
		return tenantOverviewTenantFromValue(w, ident, ident.ScopeTenantID)
	}
	if raw == "" {
		writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id query parameter must be positive")
		return 0, false
	}
	tenantID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id must be a positive int64")
		return 0, false
	}
	return tenantOverviewTenantFromValue(w, ident, tenantID)
}

func tenantOverviewTenantFromValue(w http.ResponseWriter, ident admin.AdminIdentity, tenantID int64) (int64, bool) {
	if tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id must be positive")
		return 0, false
	}
	if err := ident.CanIssueForTenant(tenantID); err != nil {
		writeTenantOverviewAdminError(w, err)
		return 0, false
	}
	return tenantID, true
}

func writeTenantOverviewAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminBackend):
		writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
	case errors.Is(err, admin.ErrAdminForbidden):
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
	default:
		writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
	}
}

func tenantOverviewTotalsToPlatform(row dboverview.AggregateTenantUsageOverviewTotalsRow) dbbilling.AggregateUsageOverviewTotalsRow {
	return dbbilling.AggregateUsageOverviewTotalsRow{
		RequestCount:             row.RequestCount,
		TotalCost:                row.TotalCost,
		TotalTokens:              row.TotalTokens,
		TotalTokensInput:         row.TotalTokensInput,
		TotalTokensOutput:        row.TotalTokensOutput,
		TotalCacheCreationTokens: row.TotalCacheCreationTokens,
		TotalCacheReadTokens:     row.TotalCacheReadTokens,
		TotalImageOutputTokens:   row.TotalImageOutputTokens,
		TotalInputCost:           row.TotalInputCost,
		TotalOutputCost:          row.TotalOutputCost,
		TotalCacheCreationCost:   row.TotalCacheCreationCost,
		TotalCacheReadCost:       row.TotalCacheReadCost,
		TotalImageOutputCost:     row.TotalImageOutputCost,
		ActiveUsers:              row.ActiveUsers,
		ActiveApiKeys:            row.ActiveApiKeys,
		SuccessCount:             row.SuccessCount,
	}
}

func tenantOverviewTrendToPlatform(rows []dboverview.AggregateTenantUsageOverviewTrendByDayRow) []dbbilling.AggregateUsageOverviewTrendByDayRow {
	out := make([]dbbilling.AggregateUsageOverviewTrendByDayRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, dbbilling.AggregateUsageOverviewTrendByDayRow{
			Day:          row.Day,
			RequestCount: row.RequestCount,
			TotalCost:    row.TotalCost,
		})
	}
	return out
}
