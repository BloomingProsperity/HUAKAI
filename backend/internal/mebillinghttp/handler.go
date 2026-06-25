// Package mebillinghttp 暴露【用户态】只读计费端点:用户查自己的余额与订单列表。
//
//	GET /v1/me/balance  -> 当前登录用户的可用余额
//	GET /v1/me/orders   -> 当前登录用户的订单列表(倒序,可 limit)
//
// 刻意独立成包而非塞进 gatewayhttp(后者已是超预算 god package,见
// docs/process/reviews/2026-06-24-backend-renew-codex-review.md)。底层 payment.Service 的
// GetBalance / ListOrders 已存在,本包只做用户态 HTTP 暴露 + 身份从 session context 取(绝不信
// 请求体里的 user_id),并把订单投影成【用户安全 DTO】剔除管理员内部字段(创建/确认管理员 id、
// 指纹、确认理由等)。零计费副作用,纯读。
package mebillinghttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

const (
	defaultOrderLimit = 50
	maxOrderLimit     = 200
)

// Service 是本包依赖的只读计费能力(payment.Service 已实现)。
type Service interface {
	GetBalance(ctx context.Context, tenantID, userID int64) (payment.Balance, error)
	ListOrders(ctx context.Context, tenantID, userID int64, limit int) ([]payment.Order, error)
}

type Deps struct {
	Service Service
}

// MountRoutes 挂在已带 SessionMiddleware 的 /v1/me 组下。
func MountRoutes(r chi.Router, d Deps) {
	r.Get("/balance", newBalanceHandler(d))
	r.Get("/orders", newOrdersHandler(d))
}

type balanceResponse struct {
	BalanceCents int64  `json:"balance_cents"`
	Balance      string `json:"balance"`       // 人类可读金额(分→元,带符号)
	CurrencyCode string `json:"currency_code"` // 平台基准币种
}

// orderView 是用户安全订单投影:只含用户该看的字段,剔除管理员内部归属/指纹等。
type orderView struct {
	ID                 int64   `json:"id"`
	OutTradeNo         string  `json:"out_trade_no"`
	AmountCents        int64   `json:"amount_cents"`
	CurrencyCode       string  `json:"currency_code"`
	Status             string  `json:"status"`
	ProviderKind       string  `json:"provider_kind"`
	OrderKind          string  `json:"order_kind"`
	SubscriptionPlanID *int64  `json:"subscription_plan_id,omitempty"`
	FailureCode        string  `json:"failure_code,omitempty"`
	FailureMessage     string  `json:"failure_message,omitempty"`
	PaidAt             *string `json:"paid_at,omitempty"`
	CreatedAt          string  `json:"created_at"`
}

type ordersResponse struct {
	Orders []orderView `json:"orders"`
	Count  int         `json:"count"`
}

func newBalanceHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "billing dependency unset")
			return
		}
		ident, ok := currentUser(r)
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		bal, err := d.Service.GetBalance(r.Context(), ident.TenantID, ident.UserID)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "billing_backend_error", "balance lookup failed")
			return
		}
		writeJSON(w, http.StatusOK, balanceResponse{
			BalanceCents: bal.AmountCents,
			Balance:      centsToDisplay(bal.AmountCents),
			CurrencyCode: "USD",
		})
	}
}

func newOrdersHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "billing dependency unset")
			return
		}
		ident, ok := currentUser(r)
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		limit := parseLimit(r.URL.Query().Get("limit"))
		orders, err := d.Service.ListOrders(r.Context(), ident.TenantID, ident.UserID, limit)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "billing_backend_error", "orders lookup failed")
			return
		}
		views := make([]orderView, 0, len(orders))
		for _, o := range orders {
			views = append(views, toUserView(o))
		}
		writeJSON(w, http.StatusOK, ordersResponse{Orders: views, Count: len(views)})
	}
}

// toUserView 把内部订单投影成用户安全 DTO:刻意不带 CreatedByAdminID/ConfirmedByAdminID/
// ConfirmReason/RequestFingerprint/ProviderOrderRef 等管理员内部字段。
func toUserView(o payment.Order) orderView {
	v := orderView{
		ID:                 o.ID,
		OutTradeNo:         o.OutTradeNo,
		AmountCents:        o.AmountCents,
		CurrencyCode:       o.CurrencyCode,
		Status:             string(o.Status),
		ProviderKind:       string(o.ProviderKind),
		OrderKind:          o.OrderKind,
		SubscriptionPlanID: o.SubscriptionPlanID,
		FailureCode:        o.FailureCode,
		FailureMessage:     o.FailureMessage,
		CreatedAt:          o.CreatedAt.UTC().Format(time.RFC3339),
	}
	if o.PaidAt != nil {
		s := o.PaidAt.UTC().Format(time.RFC3339)
		v.PaidAt = &s
	}
	return v
}

func currentUser(r *http.Request) (sessionauth.SessionIdentity, bool) {
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func parseLimit(raw string) int {
	if raw == "" {
		return defaultOrderLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultOrderLimit
	}
	if n > maxOrderLimit {
		return maxOrderLimit
	}
	return n
}

// centsToDisplay 把分换算成带符号的元字符串(避免浮点)。
func centsToDisplay(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := fmt.Sprintf("%d.%02d", cents/100, cents%100)
	if neg {
		return "-" + s
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
