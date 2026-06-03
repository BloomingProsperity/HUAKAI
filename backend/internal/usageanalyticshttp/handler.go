// Package usageanalyticshttp serves aggregated usage analytics over already-
// settled usage_records. SELECT-only (CMB-7); never reads or logs credentials
// (CMB-5). The self-serve surface is locked to the authenticated API key's
// (tenant_id, api_key_id) — the key scope is taken from the resolved identity,
// never from the query string, so cross-key reads are structurally impossible.
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

// maxAnalyticsWindow caps a self-serve query window so an unbounded full-history
// scan cannot be requested by accident (design risk #4 mitigation).
const maxAnalyticsWindow = 31 * 24 * time.Hour

type AuthResolver interface {
	Resolve(context.Context, *http.Request) (auth.Identity, error)
}

type AnalyticsStore interface {
	AggregateMyUsageByDay(context.Context, dbbilling.AggregateMyUsageByDayParams) ([]dbbilling.AggregateMyUsageByDayRow, error)
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

// NewTimeSeriesHandler serves GET /v1/me/analytics/time-series: a self-serve
// daily usage time-series (cost + token totals by UTC day and model) for the
// authenticated API key. Scope is locked to ident.TenantID + ident.APIKeyID.
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
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
			return
		}
		from, to, ok := parseWindow(w, r.URL)
		if !ok {
			return
		}
		// Scope is taken ONLY from the resolved identity — never the query string.
		rows, err := d.Store.AggregateMyUsageByDay(r.Context(), dbbilling.AggregateMyUsageByDayParams{
			TenantID: ident.TenantID,
			APIKeyID: ident.APIKeyID,
			FromTs:   pgtype.Timestamptz{Time: from, Valid: true},
			ToTs:     pgtype.Timestamptz{Time: to, Valid: true},
		})
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
