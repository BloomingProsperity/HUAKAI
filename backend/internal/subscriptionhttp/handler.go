// HUAKAI · iKun

// Package subscriptionhttp 暴露订阅子系统 (Slice P3a) 的 admin / user HTTP 端点。
// handler 由 cmd/gateway/routes.go 挂载。
package subscriptionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// AdminAuth 解析入站 admin 凭据。
type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// Service 是 handler 依赖的订阅能力子集 (由 *subscription.Service 实现)。
type Service interface {
	CreatePlan(context.Context, subscription.CreatePlanInput) (subscription.Plan, error)
	GetPlan(context.Context, int64, int64) (subscription.Plan, error)
	ListPlans(context.Context, int64, bool) ([]subscription.Plan, error)
	UpdatePlan(context.Context, subscription.UpdatePlanInput) (subscription.Plan, error)
	DisablePlan(context.Context, int64, int64) error
	AssignSubscription(context.Context, subscription.AssignSubscriptionInput) (subscription.AssignResult, error)
	BulkAssign(context.Context, subscription.BulkAssignInput) (subscription.BulkAssignResult, error)
	CancelSubscription(context.Context, int64, int64, int64, string) (subscription.UserSubscription, error)
	ExtendSubscription(context.Context, subscription.ExtendSubscriptionInput) (subscription.UserSubscription, error)
	ResetQuota(context.Context, subscription.ResetQuotaInput) (subscription.UserSubscription, error)
	RevokeSubscription(context.Context, subscription.RevokeSubscriptionInput) (subscription.UserSubscription, error)
	SetAutoRenew(context.Context, int64, int64, bool) (subscription.UserSubscription, error)
	GetSubscription(context.Context, int64, int64) (subscription.UserSubscription, error)
	ListUserSubscriptions(context.Context, int64, int64) ([]subscription.UserSubscription, error)
	ListAuditEvents(context.Context, int64, int64) ([]subscription.AuditEvent, error)
}

// VoucherService 是订阅券创建端点依赖的券能力子集 (由 *voucher.Service 实现)。
// 仅暴露 Create — 订阅券的列券/吊销/兑换走既有 voucher / gatewayhttp 路由, 不在此重复。
type VoucherService interface {
	Create(context.Context, voucher.CreateInput) (voucher.CreateResult, error)
}

// AdminDeps 管理员路由依赖。
type AdminDeps struct {
	Auth    AdminAuth
	Service Service
	// VoucherService 仅订阅券创建端点用; 其余订阅 admin 端点不依赖它 (可为 nil, 该端点回 503)。
	VoucherService VoucherService
}

// UserDeps 用户路由依赖。
type UserDeps struct {
	Service Service
	// Payment 用于订阅自助购买 (POST /purchase): 复用支付建单, 造一张 subscription 类型订单,
	// 待 confirm/webhook 履约后才真正 grant 订阅。nil 时该端点回 503, 不影响只读端点。
	Payment PaymentOrderService
	// TradeNoGen 生成 tenant-routable 外部交易号 (默认 paymenthttp.ExternalTradeNoForTenant);
	// 注入点便于测试。nil 时 purchase 端点回 503。
	TradeNoGen func(tenantID int64) (string, error)
}

// ---- 请求体 ----

type createPlanRequest struct {
	TenantID      int64   `json:"tenant_id"`
	Name          string  `json:"name"`
	Description   string  `json:"description,omitempty"`
	PriceCents    int64   `json:"price_cents,omitempty"`
	CurrencyCode  string  `json:"currency_code,omitempty"`
	ValidityDays  int     `json:"validity_days"`
	GrantedGroup  string  `json:"granted_group,omitempty"`
	DailyCapUSD   *string `json:"daily_cap_usd,omitempty"`
	WeeklyCapUSD  *string `json:"weekly_cap_usd,omitempty"`
	MonthlyCapUSD *string `json:"monthly_cap_usd,omitempty"`
	// 指针: 区分"省略"与"显式 false"。省略时默认 true (对齐 migration DEFAULT true,
	// 否则 admin 建的套餐零值 false 会被 /v1/users/me/subscriptions/plans 的 for_sale 过滤隐藏)。
	ForSale   *bool `json:"for_sale,omitempty"`
	SortOrder int   `json:"sort_order,omitempty"`
}

type assignRequest struct {
	TenantID int64 `json:"tenant_id"`
	UserID   int64 `json:"user_id"`
	PlanID   int64 `json:"plan_id"`
}

type tenantBodyRequest struct {
	TenantID int64 `json:"tenant_id"`
}

// createSubscriptionVoucherRequest 建一张订阅券 (grant_kind=subscription) 的请求体。
// 字段命名对齐 gatewayhttp 余额券建券请求 + plan_id; grant_kind 由端点强制 subscription, 不由客户端传。
type createSubscriptionVoucherRequest struct {
	TenantID     int64     `json:"tenant_id"`
	PlanID       int64     `json:"plan_id"`
	Code         string    `json:"code,omitempty"`
	AmountCents  int64     `json:"amount_cents"` // 名义价 (信息性, 兑换时不入余额)
	CurrencyCode string    `json:"currency_code,omitempty"`
	ValidFrom    time.Time `json:"valid_from"`
	ValidUntil   time.Time `json:"valid_until"`
	// 券码本身的可兑换窗口 (与套餐授予的 validity_days 无关)。
	MaxRedemptions   int    `json:"max_redemptions,omitempty"`
	SingleUsePerUser *bool  `json:"single_use_per_user,omitempty"`
	EligibleUserID   *int64 `json:"eligible_user_id,omitempty"`
}

// ---- DTO (snake_case, 不暴露内部字段) ----

type planView struct {
	ID            int64     `json:"id"`
	TenantID      int64     `json:"tenant_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	PriceCents    int64     `json:"price_cents"`
	CurrencyCode  string    `json:"currency_code"`
	ValidityDays  int       `json:"validity_days"`
	GrantedGroup  string    `json:"granted_group,omitempty"`
	DailyCapUSD   *string   `json:"daily_cap_usd,omitempty"`
	WeeklyCapUSD  *string   `json:"weekly_cap_usd,omitempty"`
	MonthlyCapUSD *string   `json:"monthly_cap_usd,omitempty"`
	ForSale       bool      `json:"for_sale"`
	Enabled       bool      `json:"enabled"`
	SortOrder     int       `json:"sort_order"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type subscriptionView struct {
	ID            int64      `json:"id"`
	PlanID        int64      `json:"plan_id"`
	GrantedGroup  string     `json:"granted_group,omitempty"`
	DailyCapUSD   *string    `json:"daily_cap_usd,omitempty"`
	WeeklyCapUSD  *string    `json:"weekly_cap_usd,omitempty"`
	MonthlyCapUSD *string    `json:"monthly_cap_usd,omitempty"`
	Status        string     `json:"status"`
	StartsAt      time.Time  `json:"starts_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CancelledAt   *time.Time `json:"cancelled_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type adminSubscriptionView struct {
	subscriptionView
	UserID            int64  `json:"user_id"`
	Source            string `json:"source"`
	AssignedByAdminID int64  `json:"assigned_by_admin_id,omitempty"`
	PrevUserGroup     string `json:"prev_user_group,omitempty"`
}

type auditEventView struct {
	EventType   string    `json:"event_type"`
	ActorKind   string    `json:"actor_kind"`
	ActorID     int64     `json:"actor_id,omitempty"`
	ReasonClass string    `json:"reason_class,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
}

func capStr(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
}

func toPlanView(p subscription.Plan) planView {
	return planView{
		ID: p.ID, TenantID: p.TenantID, Name: p.Name, Description: p.Description,
		PriceCents: p.PriceCents, CurrencyCode: p.CurrencyCode, ValidityDays: p.ValidityDays,
		GrantedGroup: p.GrantedGroup, DailyCapUSD: capStr(p.DailyCapUSD), WeeklyCapUSD: capStr(p.WeeklyCapUSD),
		MonthlyCapUSD: capStr(p.MonthlyCapUSD), ForSale: p.ForSale, Enabled: p.Enabled, SortOrder: p.SortOrder,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

func toPlanViews(plans []subscription.Plan) []planView {
	out := make([]planView, 0, len(plans))
	for _, p := range plans {
		out = append(out, toPlanView(p))
	}
	return out
}

func toSubscriptionView(s subscription.UserSubscription) subscriptionView {
	return subscriptionView{
		ID: s.ID, PlanID: s.PlanID, GrantedGroup: s.GrantedGroup,
		DailyCapUSD: capStr(s.DailyCapUSD), WeeklyCapUSD: capStr(s.WeeklyCapUSD), MonthlyCapUSD: capStr(s.MonthlyCapUSD),
		Status: string(s.Status), StartsAt: s.StartsAt, ExpiresAt: s.ExpiresAt, CancelledAt: s.CancelledAt, CreatedAt: s.CreatedAt,
	}
}

func toUserSubscriptionViews(subs []subscription.UserSubscription) []subscriptionView {
	out := make([]subscriptionView, 0, len(subs))
	for _, s := range subs {
		out = append(out, toSubscriptionView(s))
	}
	return out
}

func toAdminSubscriptionView(s subscription.UserSubscription) adminSubscriptionView {
	return adminSubscriptionView{
		subscriptionView:  toSubscriptionView(s),
		UserID:            s.UserID,
		Source:            string(s.Source),
		AssignedByAdminID: s.AssignedByAdminID,
		PrevUserGroup:     s.PrevUserGroup,
	}
}

func toAdminSubscriptionViews(subs []subscription.UserSubscription) []adminSubscriptionView {
	out := make([]adminSubscriptionView, 0, len(subs))
	for _, s := range subs {
		out = append(out, toAdminSubscriptionView(s))
	}
	return out
}

func toAuditEventViews(events []subscription.AuditEvent) []auditEventView {
	out := make([]auditEventView, 0, len(events))
	for _, ev := range events {
		out = append(out, auditEventView{
			EventType: ev.EventType, ActorKind: ev.ActorKind, ActorID: ev.ActorID,
			ReasonClass: ev.ReasonClass, OccurredAt: ev.OccurredAt,
		})
	}
	return out
}

// ---- 路由挂载 ----

// MountSubscriptionAdminRoutes 挂载管理员订阅端点 (套餐 CRUD + 分配/取消/查询)。
func MountSubscriptionAdminRoutes(r chi.Router, d AdminDeps) {
	r.Post("/plans", newAdminCreatePlanHandler(d))
	r.Get("/plans", newAdminListPlansHandler(d))
	r.Get("/plans/{id}", newAdminGetPlanHandler(d))
	r.Put("/plans/{id}", newAdminUpdatePlanHandler(d))
	r.Post("/plans/{id}", newAdminUpdatePlanHandler(d))
	r.Post("/plans/{id}/disable", newAdminDisablePlanHandler(d))
	r.Post("/assignments", newAdminAssignHandler(d))
	r.Post("/assignments/bulk", newAdminBulkAssignHandler(d))
	r.Get("/assignments", newAdminListAssignmentsHandler(d))
	r.Get("/assignments/{id}", newAdminGetAssignmentHandler(d))
	r.Post("/assignments/{id}/cancel", newAdminCancelHandler(d))
	r.Post("/assignments/{id}/extend", newAdminExtendHandler(d))
	r.Post("/assignments/{id}/reset-quota", newAdminResetQuotaHandler(d))
	r.Post("/assignments/{id}/revoke", newAdminRevokeHandler(d))
	r.Post("/vouchers", newAdminCreateSubscriptionVoucherHandler(d))
}

// MountSubscriptionUserRoutes 挂载用户订阅端点 (当前订阅 / 可购套餐 / 自助购买)。
func MountSubscriptionUserRoutes(r chi.Router, d UserDeps) {
	r.Get("/", newUserListSubscriptionsHandler(d))
	r.Get("/me", newUserCurrentSubscriptionHandler(d))
	r.Get("/plans", newUserListPlansHandler(d))
	r.Post("/cancel-renew", newUserCancelRenewHandler(d))
	r.Post("/purchase", newUserPurchaseHandler(d))
}

// ---- admin handlers ----

func newAdminCreatePlanHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		var req createPlanRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		daily, ok := parseCap(w, req.DailyCapUSD, "daily_cap_usd")
		if !ok {
			return
		}
		weekly, ok := parseCap(w, req.WeeklyCapUSD, "weekly_cap_usd")
		if !ok {
			return
		}
		monthly, ok := parseCap(w, req.MonthlyCapUSD, "monthly_cap_usd")
		if !ok {
			return
		}
		forSale := true // 省略=默认上架 (对齐 migration DEFAULT true)
		if req.ForSale != nil {
			forSale = *req.ForSale
		}
		plan, err := d.Service.CreatePlan(r.Context(), subscription.CreatePlanInput{
			TenantID:      req.TenantID,
			Name:          req.Name,
			Description:   req.Description,
			PriceCents:    req.PriceCents,
			CurrencyCode:  req.CurrencyCode,
			ValidityDays:  req.ValidityDays,
			GrantedGroup:  req.GrantedGroup,
			DailyCapUSD:   daily,
			WeeklyCapUSD:  weekly,
			MonthlyCapUSD: monthly,
			ForSale:       forSale,
			SortOrder:     req.SortOrder,
		})
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"plan": toPlanView(plan)})
	}
}

func newAdminListPlansHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		onlyForSale := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("for_sale")), "true")
		plans, err := d.Service.ListPlans(r.Context(), tenantID, onlyForSale)
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plans": toPlanViews(plans)})
	}
}

func newAdminGetPlanHandler(d AdminDeps) http.HandlerFunc {
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
		plan, err := d.Service.GetPlan(r.Context(), tenantID, id)
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plan": toPlanView(plan)})
	}
}

func newAdminDisablePlanHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req tenantBodyRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if err := d.Service.DisablePlan(r.Context(), req.TenantID, id); err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"disabled": true})
	}
}

func newAdminAssignHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		var req assignRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		res, err := d.Service.AssignSubscription(r.Context(), subscription.AssignSubscriptionInput{
			TenantID:     req.TenantID,
			UserID:       req.UserID,
			PlanID:       req.PlanID,
			ActorAdminID: ident.TokenID,
			RequestID:    requestID(r),
		})
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		status := http.StatusCreated
		if res.Idempotent {
			status = http.StatusOK
		}
		writeJSON(w, status, map[string]any{
			"subscription": toAdminSubscriptionView(res.Subscription),
			"idempotent":   res.Idempotent,
		})
	}
}

func newAdminListAssignmentsHandler(d AdminDeps) http.HandlerFunc {
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
		subs, err := d.Service.ListUserSubscriptions(r.Context(), tenantID, userID)
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subscriptions": toAdminSubscriptionViews(subs)})
	}
}

func newAdminGetAssignmentHandler(d AdminDeps) http.HandlerFunc {
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
		sub, err := d.Service.GetSubscription(r.Context(), tenantID, id)
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		events, err := d.Service.ListAuditEvents(r.Context(), tenantID, id)
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"subscription": toAdminSubscriptionView(sub),
			"audit_events": toAuditEventViews(events),
		})
	}
}

func newAdminCancelHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req tenantBodyRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		sub, err := d.Service.CancelSubscription(r.Context(), req.TenantID, id, ident.TokenID, requestID(r))
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subscription": toAdminSubscriptionView(sub)})
	}
}

// newAdminCreateSubscriptionVoucherHandler 建一张订阅券 (grant_kind 由端点强制 subscription)。
// 复用 voucher.Service 落库 (含 P3b-5b 的一致性校验); 先经订阅 Service.GetPlan 确认套餐存在,
// 给清晰 404 而非建券撞 FK 回模糊后端错。
func newAdminCreateSubscriptionVoucherHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		if d.VoucherService == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "voucher dependency unset")
			return
		}
		var req createSubscriptionVoucherRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.PlanID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_plan", "plan_id must be positive")
			return
		}
		// 先确认套餐存在 (清晰 404, 而非建券撞 voucher_subscription_plan_fk 回模糊错)。
		if _, err := d.Service.GetPlan(r.Context(), req.TenantID, req.PlanID); err != nil {
			writeSubscriptionError(w, err)
			return
		}
		planID := req.PlanID
		result, err := d.VoucherService.Create(r.Context(), voucher.CreateInput{
			TenantID: req.TenantID, AdminID: ident.TokenID, Code: req.Code,
			AmountCents: req.AmountCents, CurrencyCode: req.CurrencyCode,
			ValidFrom: req.ValidFrom, ValidUntil: req.ValidUntil,
			MaxRedemptions: req.MaxRedemptions, SingleUsePerUser: boolDefault(req.SingleUsePerUser, true),
			EligibleUserID:     req.EligibleUserID,
			GrantKind:          voucher.GrantKindSubscription,
			SubscriptionPlanID: &planID,
		})
		if err != nil {
			writeVoucherError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"voucher": result.Voucher, "code": result.Code})
	}
}

// ---- user handlers ----

func newUserListSubscriptionsHandler(d UserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d)
		if !ok {
			return
		}
		subs, err := d.Service.ListUserSubscriptions(r.Context(), ident.TenantID, ident.UserID)
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subscriptions": toUserSubscriptionViews(subs)})
	}
}

func newUserListPlansHandler(d UserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d)
		if !ok {
			return
		}
		plans, err := d.Service.ListPlans(r.Context(), ident.TenantID, true) // 用户只看在售启用的
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plans": toPlanViews(plans)})
	}
}

// ---- helpers ----

func resolveAdmin(w http.ResponseWriter, r *http.Request, d AdminDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "subscription admin dependency unset")
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

func resolveSession(w http.ResponseWriter, r *http.Request, d UserDeps) (sessionauth.SessionIdentity, bool) {
	if d.Service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "subscription dependency unset")
		return sessionauth.SessionIdentity{}, false
	}
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func parseCap(w http.ResponseWriter, raw *string, field string) (*decimal.Decimal, bool) {
	if raw == nil {
		return nil, true
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return nil, true
	}
	d, err := decimal.NewFromString(s)
	if err != nil || d.IsNegative() {
		writeJSONError(w, http.StatusBadRequest, "invalid_"+field, field+" must be a non-negative decimal string")
		return nil, false
	}
	return &d, true
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_id", "id must be a positive int64")
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

func boolDefault(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func requestID(r *http.Request) string {
	return r.Header.Get("X-Request-Id")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_subscription_request", "request body is not valid JSON")
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

func writeSubscriptionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscription.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_subscription_request", "subscription request is invalid")
	case errors.Is(err, subscription.ErrPlanInvalid):
		writeJSONError(w, http.StatusBadRequest, "invalid_plan", "plan fields are invalid")
	case errors.Is(err, subscription.ErrPlanNotFound):
		writeJSONError(w, http.StatusNotFound, "plan_not_found", "subscription plan not found")
	case errors.Is(err, subscription.ErrPlanDisabled):
		writeJSONError(w, http.StatusConflict, "plan_disabled", "subscription plan is disabled")
	case errors.Is(err, subscription.ErrSubscriptionNotFound):
		writeJSONError(w, http.StatusNotFound, "subscription_not_found", "subscription not found")
	case errors.Is(err, subscription.ErrSubscriptionNotActive):
		writeJSONError(w, http.StatusConflict, "subscription_not_active", "subscription is not active")
	case errors.Is(err, subscription.ErrQuotaInstallFailed):
		writeJSONError(w, http.StatusServiceUnavailable, "quota_install_failed", "failed to install subscription quota")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "subscription_backend_error", "subscription service unavailable")
	}
}

// writeVoucherError 把订阅券创建路径的 voucher 错误映射为 HTTP 响应。
func writeVoucherError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, voucher.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_voucher_request", "voucher request is invalid")
	case errors.Is(err, voucher.ErrVoucherDuplicate):
		writeJSONError(w, http.StatusConflict, "voucher_duplicate", "voucher code already exists")
	case errors.Is(err, voucher.ErrSubscriptionVoucherUnsupported):
		writeJSONError(w, http.StatusServiceUnavailable, "subscription_voucher_unsupported", "subscription voucher requires postgres store")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "voucher_backend_error", "voucher service unavailable")
	}
}
