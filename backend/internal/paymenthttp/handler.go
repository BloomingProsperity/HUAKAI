// HUAKAI · iKun

// Package paymenthttp 暴露支付子系统 (Slice P1) 的 admin / user HTTP 端点。
// handler 不进冻结包 gatewayhttp; 由 cmd/gateway/routes.go 挂载。
package paymenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// AdminAuth 解析入站 admin 凭据。
type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// Service 是 handler 依赖的支付能力子集 (由 *payment.Service 实现)。
type Service interface {
	CreateOrder(context.Context, payment.CreateOrderInput) (payment.CreateOrderResult, error)
	AdminConfirmPaid(context.Context, payment.AdminConfirmPaidInput) (payment.FulfillResult, error)
	GetOrder(context.Context, int64, int64) (payment.Order, error)
	ListAuditEvents(context.Context, int64, int64) ([]payment.AuditEvent, error)
	GetBalance(context.Context, int64, int64) (payment.Balance, error)
	ListOrders(context.Context, int64, int64, int) ([]payment.Order, error)
}

// AdminDeps 管理员路由依赖。
type AdminDeps struct {
	Auth    AdminAuth
	Service Service
}

// UserDeps 用户路由依赖。
type UserDeps struct {
	Service Service
}

type createOrderRequest struct {
	TenantID     int64  `json:"tenant_id"`
	UserID       int64  `json:"user_id"`
	AmountCents  int64  `json:"amount_cents"`
	CurrencyCode string `json:"currency_code,omitempty"`
	OutTradeNo   string `json:"out_trade_no,omitempty"`
	ProviderKind string `json:"provider_kind,omitempty"`
	// OrderKind 省略=充值 (topup); subscription 时 SubscriptionPlanID 必填 (service 层校验)。
	OrderKind          string `json:"order_kind,omitempty"`
	SubscriptionPlanID *int64 `json:"subscription_plan_id,omitempty"`
}

type confirmRequest struct {
	TenantID      int64  `json:"tenant_id"`
	ConfirmReason string `json:"confirm_reason,omitempty"`
}

// orderView 是面向用户的订单 DTO — 仅公开字段, snake_case, 不暴露任何内部/管理字段。
type orderView struct {
	ID           int64  `json:"id"`
	OutTradeNo   string `json:"out_trade_no"`
	UserID       int64  `json:"user_id"`
	AmountCents  int64  `json:"amount_cents"`
	CurrencyCode string `json:"currency_code"`
	Status       string `json:"status"`
	ProviderKind string `json:"provider_kind"`
	// OrderKind 区分充值/购订阅; SubscriptionPlanID 仅订阅单非空 (用户可见自己买的是哪种)。
	OrderKind          string     `json:"order_kind"`
	SubscriptionPlanID *int64     `json:"subscription_plan_id,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	PaidAt             *time.Time `json:"paid_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
}

// adminOrderView 面向管理员 — 含管理字段, 但仍不暴露纯内部的 request_fingerprint。
type adminOrderView struct {
	orderView
	RechargingAt       *time.Time `json:"recharging_at,omitempty"`
	FailedAt           *time.Time `json:"failed_at,omitempty"`
	CreatedByAdminID   int64      `json:"created_by_admin_id,omitempty"`
	ConfirmedByAdminID int64      `json:"confirmed_by_admin_id,omitempty"`
	ConfirmReason      string     `json:"confirm_reason,omitempty"`
	ProviderOrderRef   string     `json:"provider_order_ref,omitempty"`
	FailureCode        string     `json:"failure_code,omitempty"`
	FailureMessage     string     `json:"failure_message,omitempty"`
}

type creditView struct {
	ID             int64     `json:"id"`
	AmountCents    int64     `json:"amount_cents"`
	CurrencyCode   string    `json:"currency_code"`
	ReasonClass    string    `json:"reason_class"`
	BillingEventID int64     `json:"billing_event_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type balanceView struct {
	TenantID    int64 `json:"tenant_id"`
	UserID      int64 `json:"user_id"`
	AmountCents int64 `json:"amount_cents"`
}

// subscriptionGrantView 订阅单履约后的订阅授予摘要 DTO (零入账, 不含 credit/balance)。
type subscriptionGrantView struct {
	UserSubscriptionID  int64     `json:"user_subscription_id"`
	PlanID              int64     `json:"plan_id"`
	ResultKind          string    `json:"result_kind"`
	NewExpiresAt        time.Time `json:"new_expires_at"`
	AppliedValidityDays int       `json:"applied_validity_days"`
}

func toSubscriptionGrantView(g *payment.SubscriptionGrant) subscriptionGrantView {
	return subscriptionGrantView{
		UserSubscriptionID:  g.UserSubscriptionID,
		PlanID:              g.PlanID,
		ResultKind:          g.ResultKind,
		NewExpiresAt:        g.NewExpiresAt,
		AppliedValidityDays: g.AppliedValidityDays,
	}
}

type auditEventView struct {
	EventType   string    `json:"event_type"`
	ActorKind   string    `json:"actor_kind"`
	ActorID     int64     `json:"actor_id,omitempty"`
	ReasonClass string    `json:"reason_class,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
}

func toOrderView(o payment.Order) orderView {
	return orderView{
		ID: o.ID, OutTradeNo: o.OutTradeNo, UserID: o.UserID, AmountCents: o.AmountCents,
		CurrencyCode: o.CurrencyCode, Status: string(o.Status), ProviderKind: string(o.ProviderKind),
		OrderKind: o.OrderKind, SubscriptionPlanID: o.SubscriptionPlanID,
		CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt, ExpiresAt: o.ExpiresAt, PaidAt: o.PaidAt, CompletedAt: o.CompletedAt,
	}
}

func toUserOrderViews(orders []payment.Order) []orderView {
	out := make([]orderView, 0, len(orders))
	for _, o := range orders {
		out = append(out, toOrderView(o))
	}
	return out
}

func toAdminOrderView(o payment.Order) adminOrderView {
	return adminOrderView{
		orderView: toOrderView(o), RechargingAt: o.RechargingAt, FailedAt: o.FailedAt,
		CreatedByAdminID: o.CreatedByAdminID, ConfirmedByAdminID: o.ConfirmedByAdminID, ConfirmReason: o.ConfirmReason,
		ProviderOrderRef: o.ProviderOrderRef, FailureCode: o.FailureCode, FailureMessage: o.FailureMessage,
	}
}

func toAdminOrderViews(orders []payment.Order) []adminOrderView {
	out := make([]adminOrderView, 0, len(orders))
	for _, o := range orders {
		out = append(out, toAdminOrderView(o))
	}
	return out
}

func toCreditView(c payment.CreditRecord) creditView {
	return creditView{
		ID: c.ID, AmountCents: c.AmountCents, CurrencyCode: c.CurrencyCode,
		ReasonClass: c.ReasonClass, BillingEventID: c.BillingEventID, CreatedAt: c.CreatedAt,
	}
}

func toAuditEventViews(events []payment.AuditEvent) []auditEventView {
	out := make([]auditEventView, 0, len(events))
	for _, ev := range events {
		out = append(out, auditEventView{
			EventType: ev.EventType, ActorKind: ev.ActorKind, ActorID: ev.ActorID,
			ReasonClass: ev.ReasonClass, OccurredAt: ev.OccurredAt,
		})
	}
	return out
}

// MountPaymentAdminRoutes 挂载管理员支付端点 (建单 / 确认+履约 / 查单 / 列单)。
func MountPaymentAdminRoutes(r chi.Router, d AdminDeps) {
	r.Post("/", newAdminCreateOrderHandler(d))
	r.Get("/", newAdminListOrdersHandler(d))
	r.Get("/{id}", newAdminGetOrderHandler(d))
	r.Post("/{id}/confirm", newAdminConfirmHandler(d))
}

// MountPaymentUserRoutes 挂载用户支付端点 (自己的订单 / 余额)。
func MountPaymentUserRoutes(r chi.Router, d UserDeps) {
	r.Get("/orders", newUserListOrdersHandler(d))
	r.Get("/balance", newUserBalanceHandler(d))
}

func newAdminCreateOrderHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		var req createOrderRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		res, err := d.Service.CreateOrder(r.Context(), payment.CreateOrderInput{
			TenantID:           req.TenantID,
			UserID:             req.UserID,
			AmountCents:        req.AmountCents,
			CurrencyCode:       req.CurrencyCode,
			OutTradeNo:         req.OutTradeNo,
			ProviderKind:       payment.ProviderKind(req.ProviderKind),
			OrderKind:          req.OrderKind,
			SubscriptionPlanID: req.SubscriptionPlanID,
			ActorAdminID:       ident.TokenID,
			RequestID:          requestID(r),
		})
		if err != nil {
			writePaymentError(w, err)
			return
		}
		status := http.StatusCreated
		if res.Idempotent {
			status = http.StatusOK
		}
		writeJSON(w, status, map[string]any{"order": toAdminOrderView(res.Order), "idempotent": res.Idempotent})
	}
}

func newAdminConfirmHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req confirmRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		res, err := d.Service.AdminConfirmPaid(r.Context(), payment.AdminConfirmPaidInput{
			TenantID:      req.TenantID,
			OrderID:       id,
			ActorAdminID:  ident.TokenID,
			ConfirmReason: req.ConfirmReason,
			RequestID:     requestID(r),
		})
		if err != nil {
			writePaymentError(w, err)
			return
		}
		resp := map[string]any{
			"order":      toAdminOrderView(res.Order),
			"idempotent": res.Idempotent,
		}
		// 订阅单: 表订阅授予, 零入账 → 不渲染 credit/balance (避免误导出零值入账)。
		// 充值单: 渲染入账 credit + 派生余额。
		if res.Subscription != nil {
			resp["subscription"] = toSubscriptionGrantView(res.Subscription)
		} else {
			resp["credit"] = toCreditView(res.Credit)
			resp["balance_cents"] = res.BalanceCents
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func newAdminGetOrderHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		order, err := d.Service.GetOrder(r.Context(), tenantID, id)
		if err != nil {
			writePaymentError(w, err)
			return
		}
		events, err := d.Service.ListAuditEvents(r.Context(), tenantID, id)
		if err != nil {
			writePaymentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"order": toAdminOrderView(order), "audit_events": toAuditEventViews(events)})
	}
}

func newAdminListOrdersHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		userID, ok := parsePositiveQuery(w, r, "user_id")
		if !ok {
			return
		}
		orders, err := d.Service.ListOrders(r.Context(), tenantID, userID, parseLimit(r))
		if err != nil {
			writePaymentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"orders": toAdminOrderViews(orders)})
	}
}

func newUserListOrdersHandler(d UserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "payment dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		orders, err := d.Service.ListOrders(r.Context(), ident.TenantID, ident.UserID, parseLimit(r))
		if err != nil {
			writePaymentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"orders": toUserOrderViews(orders)})
	}
}

func newUserBalanceHandler(d UserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "payment dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		bal, err := d.Service.GetBalance(r.Context(), ident.TenantID, ident.UserID)
		if err != nil {
			writePaymentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"balance": balanceView{TenantID: bal.TenantID, UserID: bal.UserID, AmountCents: bal.AmountCents}})
	}
}

func resolveAdmin(w http.ResponseWriter, r *http.Request, d AdminDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "payment admin dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_order_id", "order id must be a positive int64")
		return 0, false
	}
	return id, true
}

func parsePositiveQuery(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		writeJSONError(w, http.StatusBadRequest, name+"_required", name+" query parameter must be positive")
		return 0, false
	}
	return n, true
}

func parseLimit(r *http.Request) int {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 50
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 || n > 200 {
		return 50
	}
	return n
}

func requestID(r *http.Request) string {
	return r.Header.Get("X-Request-Id")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_payment_request", "request body is not valid JSON")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writePaymentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, payment.ErrInvalidAmount):
		writeJSONError(w, http.StatusBadRequest, "invalid_amount", "amount must be positive")
	case errors.Is(err, payment.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_payment_request", "payment request is invalid")
	case errors.Is(err, payment.ErrProviderUnknown):
		writeJSONError(w, http.StatusBadRequest, "unknown_provider", "provider kind is not supported")
	case errors.Is(err, payment.ErrUnsupportedCurrency):
		writeJSONError(w, http.StatusBadRequest, "unsupported_currency", "currency not supported (USD only)")
	case errors.Is(err, payment.ErrOrderNotFound):
		writeJSONError(w, http.StatusNotFound, "order_not_found", "payment order not found")
	case errors.Is(err, payment.ErrIdempotencyConflict):
		writeJSONError(w, http.StatusConflict, "out_trade_no_conflict", "out_trade_no reused with different order fields")
	case errors.Is(err, payment.ErrOrderNotConfirmable):
		writeJSONError(w, http.StatusConflict, "order_not_confirmable", "order is not in a confirmable state")
	case errors.Is(err, payment.ErrOrderNotFulfillable):
		writeJSONError(w, http.StatusConflict, "order_not_fulfillable", "order is not in a fulfillable state")
	case errors.Is(err, payment.ErrSubscriptionOrderRequiresPG):
		writeJSONError(w, http.StatusServiceUnavailable, "subscription_order_requires_pg", "subscription order fulfillment requires postgres store")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "payment_backend_error", "payment service unavailable")
	}
}
