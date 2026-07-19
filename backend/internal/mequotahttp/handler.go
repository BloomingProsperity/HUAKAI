package mequotahttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

type AuthResolver interface {
	Resolve(context.Context, *http.Request) (auth.Identity, error)
}

type Store interface {
	ListCurrentWindowsForScopeMetrics(context.Context, int64, quota.ScopeKind, string, time.Time, []quota.Metric) ([]quota.CurrentWindowRead, error)
}

type Deps struct {
	Auth  AuthResolver
	Store Store
}

// SessionResolver 把已校验的 /v1/me 会话上下文适配为与 API-key 作用域的
// 自助 handler 所用相同的 AuthResolver 形状。
type SessionResolver struct{}

func (SessionResolver) Resolve(ctx context.Context, _ *http.Request) (auth.Identity, error) {
	ident, ok := auth.SessionFromContext(ctx)
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		return auth.Identity{}, auth.ErrUnauthorized
	}
	return auth.Identity{TenantID: ident.TenantID, UserID: ident.UserID}, nil
}

type listResponse struct {
	Items []windowView `json:"items"`
}

type windowView struct {
	ModelSelector string    `json:"model_selector"`
	Metric        string    `json:"metric"`
	WindowKind    string    `json:"window_kind"`
	Cap           string    `json:"cap"`
	Consumed      string    `json:"consumed"`
	Remaining     string    `json:"remaining"`
	Overage       string    `json:"overage"`
	RequestCount  int64     `json:"request_count"`
	WindowStart   time.Time `json:"window_start"`
	WindowEnd     time.Time `json:"window_end"`
}

func NewHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Store == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "me quota dependency unset")
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
		if err != nil || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
			return
		}
		now := time.Now().UTC()
		rows, err := d.Store.ListCurrentWindowsForScopeMetrics(
			r.Context(),
			ident.TenantID,
			quota.ScopeUser,
			strconv.FormatInt(ident.UserID, 10),
			now,
			// 仅限窗口形态的 metric;并发是基于槽位的(无窗口行)。
			[]quota.Metric{quota.MetricRequests, quota.MetricCostUSD, quota.MetricTokensEstimated},
		)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "quota_status_unavailable", "quota status unavailable")
			return
		}
		out := listResponse{Items: make([]windowView, 0, len(rows))}
		for _, row := range rows {
			out.Items = append(out.Items, toWindowView(row))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func toWindowView(w quota.CurrentWindowRead) windowView {
	consumed := w.SettledValue.Add(w.ReservedValue)
	remaining := w.LimitValue.Sub(consumed)
	if remaining.IsNegative() {
		remaining = decimal.Zero
	}
	return windowView{
		ModelSelector: w.ModelSelector,
		Metric:        string(w.Metric),
		WindowKind:    string(w.Window.Kind),
		Cap:           w.LimitValue.String(),
		Consumed:      consumed.String(),
		Remaining:     remaining.String(),
		Overage:       w.OverageValue.String(),
		RequestCount:  w.RequestCount,
		WindowStart:   w.Window.Start,
		WindowEnd:     w.Window.End,
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}
