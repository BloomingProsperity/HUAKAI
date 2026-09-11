package usageanalyticshttp

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dboverview "github.com/BloomingProsperity/HUAKAI/internal/db/usageoverview"
)

// TenantHourlyQuerier 只读同一租户、同一窗口的合计与 UTC 小时序列。
// 不并入平台总览 Querier，避免所有 stub 被强迫实现新方法。
type TenantHourlyQuerier interface {
	AggregateTenantUsageOverviewTotals(context.Context, dboverview.AggregateTenantUsageOverviewTotalsParams) (dboverview.AggregateTenantUsageOverviewTotalsRow, error)
	AggregateTenantUsageHourlyTrend(context.Context, dboverview.AggregateTenantUsageHourlyTrendParams) ([]dboverview.AggregateTenantUsageHourlyTrendRow, error)
}

type hourlyTrendPoint struct {
	Hour                string `json:"hour"`
	TokensInput         int64  `json:"tokens_input"`
	TokensOutput        int64  `json:"tokens_output"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	Cost                string `json:"cost"`
}

type hourlyTrendResponse struct {
	TenantID int64              `json:"tenant_id"`
	Window   string             `json:"window"`
	Totals   overviewTotals     `json:"totals"`
	Hours    []hourlyTrendPoint `json:"hours"`
}

// NewTenantHourlyHandler 提供 GET /admin/v1/usage/hourly。
// 小时桶在服务端按 UTC 整点聚合；每个点并列输入/输出/提示缓存写读 Token 与同窗费用。
// 租户管理员锁定认证租户；部署者必须显式 tenant_id，省略或空白不得回落全平台。
// 不接受 timezone 参数，避免静默改窗；不补零点；不把 HTTP 快照命中折进提示缓存 Token。
func NewTenantHourlyHandler(auth TenantOverviewAuth, q TenantHourlyQuerier) http.HandlerFunc {
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
			writeJSONError(w, http.StatusBadRequest, "timezone_not_supported", "hourly buckets are UTC only")
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
		value, hit, err := GetOrLoad(tenantHourlySnapshotCacheKey(tenantID, query), overviewSnapshotTTL, func() (any, error) {
			return loadTenantHourlyResponse(r.Context(), q, tenantID, query)
		})
		if err != nil {
			w.Header().Set(snapshotCacheHeader, "miss")
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		response, ok := value.(hourlyTrendResponse)
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

func loadTenantHourlyResponse(ctx context.Context, q TenantHourlyQuerier, tenantID int64, query overviewQuery) (hourlyTrendResponse, error) {
	totalsRow, err := q.AggregateTenantUsageOverviewTotals(ctx, dboverview.AggregateTenantUsageOverviewTotalsParams{
		TenantID:     tenantID,
		SettledSince: query.settledSince,
	})
	if err != nil {
		return hourlyTrendResponse{}, err
	}
	hourRows, err := q.AggregateTenantUsageHourlyTrend(ctx, dboverview.AggregateTenantUsageHourlyTrendParams{
		TenantID:     tenantID,
		SettledSince: query.settledSince,
	})
	if err != nil {
		return hourlyTrendResponse{}, err
	}
	totals, err := overviewTotalsFromRow(tenantOverviewTotalsToPlatform(totalsRow))
	if err != nil {
		return hourlyTrendResponse{}, err
	}
	hours, err := hourlyTrendFromRows(hourRows)
	if err != nil {
		return hourlyTrendResponse{}, err
	}
	return hourlyTrendResponse{
		TenantID: tenantID,
		Window:   query.windowLabel,
		Totals:   totals,
		Hours:    hours,
	}, nil
}

func hourlyTrendFromRows(rows []dboverview.AggregateTenantUsageHourlyTrendRow) ([]hourlyTrendPoint, error) {
	hours := make([]hourlyTrendPoint, 0, len(rows))
	for _, row := range rows {
		cost, err := fixedMoneyText(row.TotalCost)
		if err != nil {
			return nil, err
		}
		hours = append(hours, hourlyTrendPoint{
			Hour:                formatHour(row.Hour),
			TokensInput:         row.TokensInput,
			TokensOutput:        row.TokensOutput,
			CacheCreationTokens: row.CacheCreationTokens,
			CacheReadTokens:     row.CacheReadTokens,
			Cost:                cost,
		})
	}
	return hours, nil
}

func tenantHourlySnapshotCacheKey(tenantID int64, query overviewQuery) string {
	return "admin_tenant_usage_hourly:v1|tenant=" + strconv.FormatInt(tenantID, 10) + "|window=" + query.windowLabel
}

func formatHour(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Truncate(time.Hour).Format(time.RFC3339)
}
