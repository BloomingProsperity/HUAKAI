// 包 usageanalyticshttp 基于已结算的 usage_records 提供聚合的用量分析。
// 仅 SELECT;绝不读取或记录任何 credential
// 。自助面被锁定到已认证 API key 的
// (tenant_id, api_key_id) —— key 范围取自解析出的身份,
// 绝不取自 query string,因此跨 key 读取在结构上不可能。
package usageanalyticshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// maxAnalyticsWindow 限制自助查询窗口,使得无界的全历史扫描
// 不会被意外地请求(设计风险缓解)。
const maxAnalyticsWindow = 31 * 24 * time.Hour

type AuthResolver interface {
	Resolve(context.Context, *http.Request) (auth.Identity, error)
}

type AnalyticsStore interface {
	AggregateMyUsageByDay(context.Context, dbbilling.AggregateMyUsageByDayParams) ([]dbbilling.AggregateMyUsageByDayRow, error)
	AggregateMyUsageByWeek(context.Context, dbbilling.AggregateMyUsageByWeekParams) ([]dbbilling.AggregateMyUsageByWeekRow, error)
	AggregateMyUsageByMonth(context.Context, dbbilling.AggregateMyUsageByMonthParams) ([]dbbilling.AggregateMyUsageByMonthRow, error)
}

type Deps struct {
	Auth  AuthResolver
	Store AnalyticsStore
}

type tokensBreakdown struct {
	Input         int64 `json:"input"`
	Output        int64 `json:"output"`
	CacheRead     int64 `json:"cache_read"`
	CacheCreation int64 `json:"cache_creation"`
}

type timeSeriesPoint struct {
	Day            string          `json:"day"`
	RequestedModel string          `json:"requested_model"`
	TotalCost      string          `json:"total_cost"`
	Tokens         tokensBreakdown `json:"tokens"`
	RequestCount   int64           `json:"request_count"`
}

type windowPeriod struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type timeSeriesResponse struct {
	Items  []timeSeriesPoint `json:"items"`
	Period windowPeriod      `json:"period"`
}

type usageGranularity string

const (
	usageGranularityDay   usageGranularity = "day"
	usageGranularityWeek  usageGranularity = "week"
	usageGranularityMonth usageGranularity = "month"
)

type usageAggregateRow struct {
	Day                      pgtype.Timestamptz
	RequestedModel           string
	TotalCost                string
	TotalTokensInput         int64
	TotalTokensOutput        int64
	TotalCacheReadTokens     int64
	TotalCacheCreationTokens int64
	RequestCount             int64
}

// NewTimeSeriesHandler 提供 GET /v1/me/analytics/time-series:面向已认证 API key 的
// 自助用量时间序列(按 UTC 天/周/月与 model 聚合的 cost + token 总量)。
// 范围锁定到 ident.TenantID + ident.APIKeyID。
func NewTimeSeriesHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Store == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "analytics dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if errors.Is(err, auth.ErrAuthMisconfigured) {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth tables unavailable")
			return
		}
		if errors.Is(err, auth.ErrAuthBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
			return
		}
		if errors.Is(err, auth.ErrForbidden) {
			writeJSONError(w, http.StatusForbidden, "forbidden", "api key policy forbids this request")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
			return
		}
		from, to, ok := parseWindow(w, r.URL)
		if !ok {
			return
		}
		granularity, ok := parseGranularity(w, r.URL)
		if !ok {
			return
		}
		// 范围仅取自解析出的身份 —— 绝不取自 query string。
		rows, err := aggregateMyUsage(r.Context(), d.Store, granularity, ident, from, to)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		items := make([]timeSeriesPoint, 0, len(rows))
		for _, row := range rows {
			items = append(items, timeSeriesPoint{
				Day:            formatDay(row.Day),
				RequestedModel: strings.TrimSpace(row.RequestedModel),
				TotalCost:      strings.TrimSpace(row.TotalCost),
				Tokens: tokensBreakdown{
					Input:         row.TotalTokensInput,
					Output:        row.TotalTokensOutput,
					CacheRead:     row.TotalCacheReadTokens,
					CacheCreation: row.TotalCacheCreationTokens,
				},
				RequestCount: row.RequestCount,
			})
		}
		writeJSON(w, http.StatusOK, timeSeriesResponse{
			Items:  items,
			Period: windowPeriod{From: from.Format(time.RFC3339), To: to.Format(time.RFC3339)},
		})
	}
}

func parseGranularity(w http.ResponseWriter, u *url.URL) (usageGranularity, bool) {
	raw := strings.TrimSpace(u.Query().Get("granularity"))
	switch raw {
	case "", string(usageGranularityDay):
		return usageGranularityDay, true
	case string(usageGranularityWeek):
		return usageGranularityWeek, true
	case string(usageGranularityMonth):
		return usageGranularityMonth, true
	default:
		writeJSONError(w, http.StatusBadRequest, "invalid_granularity", "granularity must be one of day, week, month")
		return "", false
	}
}

func aggregateMyUsage(ctx context.Context, store AnalyticsStore, granularity usageGranularity, ident auth.Identity, from, to time.Time) ([]usageAggregateRow, error) {
	fromTs := pgtype.Timestamptz{Time: from, Valid: true}
	toTs := pgtype.Timestamptz{Time: to, Valid: true}
	switch granularity {
	case usageGranularityDay:
		rows, err := store.AggregateMyUsageByDay(ctx, dbbilling.AggregateMyUsageByDayParams{
			TenantID: ident.TenantID,
			APIKeyID: ident.APIKeyID,
			FromTs:   fromTs,
			ToTs:     toTs,
		})
		if err != nil {
			return nil, err
		}
		out := make([]usageAggregateRow, 0, len(rows))
		for _, row := range rows {
			out = append(out, usageAggregateRow{
				Day:                      row.Day,
				RequestedModel:           row.RequestedModel,
				TotalCost:                row.TotalCost,
				TotalTokensInput:         row.TotalTokensInput,
				TotalTokensOutput:        row.TotalTokensOutput,
				TotalCacheReadTokens:     row.TotalCacheReadTokens,
				TotalCacheCreationTokens: row.TotalCacheCreationTokens,
				RequestCount:             row.RequestCount,
			})
		}
		return out, nil
	case usageGranularityWeek:
		rows, err := store.AggregateMyUsageByWeek(ctx, dbbilling.AggregateMyUsageByWeekParams{
			TenantID: ident.TenantID,
			APIKeyID: ident.APIKeyID,
			FromTs:   fromTs,
			ToTs:     toTs,
		})
		if err != nil {
			return nil, err
		}
		out := make([]usageAggregateRow, 0, len(rows))
		for _, row := range rows {
			out = append(out, usageAggregateRow{
				Day:                      row.Day,
				RequestedModel:           row.RequestedModel,
				TotalCost:                row.TotalCost,
				TotalTokensInput:         row.TotalTokensInput,
				TotalTokensOutput:        row.TotalTokensOutput,
				TotalCacheReadTokens:     row.TotalCacheReadTokens,
				TotalCacheCreationTokens: row.TotalCacheCreationTokens,
				RequestCount:             row.RequestCount,
			})
		}
		return out, nil
	case usageGranularityMonth:
		rows, err := store.AggregateMyUsageByMonth(ctx, dbbilling.AggregateMyUsageByMonthParams{
			TenantID: ident.TenantID,
			APIKeyID: ident.APIKeyID,
			FromTs:   fromTs,
			ToTs:     toTs,
		})
		if err != nil {
			return nil, err
		}
		out := make([]usageAggregateRow, 0, len(rows))
		for _, row := range rows {
			out = append(out, usageAggregateRow{
				Day:                      row.Day,
				RequestedModel:           row.RequestedModel,
				TotalCost:                row.TotalCost,
				TotalTokensInput:         row.TotalTokensInput,
				TotalTokensOutput:        row.TotalTokensOutput,
				TotalCacheReadTokens:     row.TotalCacheReadTokens,
				TotalCacheCreationTokens: row.TotalCacheCreationTokens,
				RequestCount:             row.RequestCount,
			})
		}
		return out, nil
	default:
		return nil, errors.New("unsupported analytics granularity")
	}
}

func parseWindow(w http.ResponseWriter, u *url.URL) (time.Time, time.Time, bool) {
	values := u.Query()
	fromRaw := strings.TrimSpace(values.Get("from"))
	toRaw := strings.TrimSpace(values.Get("to"))
	if fromRaw == "" || toRaw == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_window", "from and to (RFC3339) are required")
		return time.Time{}, time.Time{}, false
	}
	from, err := time.Parse(time.RFC3339, fromRaw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_from", "from must be RFC3339")
		return time.Time{}, time.Time{}, false
	}
	to, err := time.Parse(time.RFC3339, toRaw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_to", "to must be RFC3339")
		return time.Time{}, time.Time{}, false
	}
	from, to = from.UTC(), to.UTC()
	if !to.After(from) {
		writeJSONError(w, http.StatusBadRequest, "invalid_window", "to must be after from")
		return time.Time{}, time.Time{}, false
	}
	if to.Sub(from) > maxAnalyticsWindow {
		writeJSONError(w, http.StatusBadRequest, "window_too_large", "window must not exceed 31 days")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func formatDay(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format("2006-01-02")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
