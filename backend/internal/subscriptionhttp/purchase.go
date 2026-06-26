// HUAKAI · iKun

// 订阅自助 C5: 用户当前订阅查询 (GET /me) 与自助购买 (POST /purchase)。
// 购买复用 internal/payment 建一张 subscription 类型订单 (零余额入账),
// 待 admin confirm / provider webhook 履约后才真正 grant 订阅; 本端点只返回支付指引。
package subscriptionhttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

// PaymentOrderService 是订阅自助购买依赖的支付能力子集 (由 *payment.Service 实现)。
// 只暴露建单 — 履约 (confirm/webhook) 仍走既有支付路由, 不在此重复。
type PaymentOrderService interface {
	CreateOrder(context.Context, payment.CreateOrderInput) (payment.CreateOrderResult, error)
}

// QuotaProgressStore 暴露自助进度端点所需的只读 quota 投影。
type QuotaProgressStore interface {
	ListCurrentWindowsForScope(ctx context.Context, tenantID int64, scopeKind quota.ScopeKind, scopeID string, at time.Time) ([]quota.CurrentWindowRead, error)
}

// ---- 请求 / DTO ----

type purchaseRequest struct {
	PlanID int64 `json:"plan_id"`
}

type userChangePlanRequest struct {
	NewPlanID int64 `json:"new_plan_id"`
}

// purchaseOrderView 订阅购买订单摘要 (面向用户, snake_case, 不暴露内部/管理字段)。
type purchaseOrderView struct {
	ID                 int64  `json:"id"`
	OutTradeNo         string `json:"out_trade_no"`
	Status             string `json:"status"`
	AmountCents        int64  `json:"amount_cents"`
	CurrencyCode       string `json:"currency_code"`
	OrderKind          string `json:"order_kind"`
	SubscriptionPlanID *int64 `json:"subscription_plan_id,omitempty"`
}

// currentSubscriptionView GET /me 与 cancel-renew 响应: 当前生效订阅 + auto_renew 自助续订状态。
type currentSubscriptionView struct {
	Subscription *subscriptionView `json:"subscription"`
	AutoRenew    bool              `json:"auto_renew"`
}

type subscriptionProgressResponse struct {
	Subscription *subscriptionView          `json:"subscription"`
	Progress     []subscriptionProgressView `json:"progress"`
}

type subscriptionProgressView struct {
	WindowKind   string    `json:"window_kind"`
	Cap          string    `json:"cap"`
	Consumed     string    `json:"consumed"`
	Remaining    string    `json:"remaining"`
	Overage      string    `json:"overage"`
	RequestCount int64     `json:"request_count"`
	WindowStart  time.Time `json:"window_start"`
	WindowEnd    time.Time `json:"window_end"`

	// 读侧派生字段(纯由上面已有的 window 数据计算得出；
	// 不新增 query / schema / mutation)。所有金额都与 Cap/Consumed 保持相同的
	// USD 小数单位(numeric(20,8));参见 toSubscriptionProgressView。
	UsagePercent    float64 `json:"usage_percent"`     // consumed/cap*100；cap==0 时为 0(不做除法);超限时允许超过 100
	ResetsInSeconds int64   `json:"resets_in_seconds"` // max(0, window_end − now);窗口结束后为 0
	OverLimit       bool    `json:"over_limit"`        // consumed > cap
	OverLimitAmount string  `json:"over_limit_amount"` // max(0, consumed − cap)，与 Consumed 同一 USD 小数单位(未超时为 "0")。区别于上面持久化的 ledger Overage 字段。
}

func toPurchaseOrderView(o payment.Order) purchaseOrderView {
	return purchaseOrderView{
		ID: o.ID, OutTradeNo: o.OutTradeNo, Status: string(o.Status),
		AmountCents: o.AmountCents, CurrencyCode: o.CurrencyCode,
		OrderKind: o.OrderKind, SubscriptionPlanID: o.SubscriptionPlanID,
	}
}

// currentActiveSubscription 从用户全部订阅里挑出"当前生效"的一条:
// status=active 且 now 在 [starts_at, expires_at) 内; 多条时取 expires_at 最晚 (最长权益)。
func currentActiveSubscription(subs []subscription.UserSubscription, now time.Time) *subscription.UserSubscription {
	var best *subscription.UserSubscription
	for i := range subs {
		s := subs[i]
		if s.Status != subscription.StatusActive {
			continue
		}
		if s.StartsAt.After(now) || s.IsExpiredAt(now) {
			continue
		}
		if best == nil || s.ExpiresAt.After(best.ExpiresAt) {
			cur := s
			best = &cur
		}
	}
	return best
}

// toSubscriptionProgressView 把一个当前 quota window 投影成面向用户的进度视图。
// 派生字段(usage_percent / resets_in_seconds / over_limit / over_limit_amount)
// 纯由 window 数据加上传入的 now 时钟计算得出 —— 不新增 query、schema 或资金
// mutation。now 由 handler 一路传入，使 resets_in_seconds 在测试下具有确定性
// (此处不再现取 time.Now)。
func toSubscriptionProgressView(w quota.CurrentWindowRead, now time.Time) subscriptionProgressView {
	cap := w.LimitValue
	consumed := w.SettledValue.Add(w.ReservedValue)
	remaining := cap.Sub(consumed)
	if remaining.IsNegative() {
		remaining = decimal.Zero
	}

	// usage_percent = consumed/cap*100。对 cap==0 做防护(不做除法)→ 返回 0 而非
	// NaN/Inf。不在 100 处截断:超限窗口报告 >100，便于调用方呈现超额量。
	// DivRound 保持固定标度，使浮点结果稳定。
	usagePercent := 0.0
	if !cap.IsZero() {
		usagePercent = consumed.DivRound(cap, 8).Mul(decimal.NewFromInt(100)).InexactFloat64()
	}

	// over_limit / over_limit_amount = max(0, consumed − cap)，与 consumed 同一 USD
	// 小数单位。区别于持久化的 ledger OverageValue(累计计费超额);此处是窗口当前
	// 超出 cap 多少的实时快照。
	overLimit := consumed.GreaterThan(cap)
	overLimitAmount := consumed.Sub(cap)
	if overLimitAmount.IsNegative() {
		overLimitAmount = decimal.Zero
	}

	// resets_in_seconds = max(0, window_end − now);窗口结束后截断为 0。
	resetsInSeconds := int64(0)
	if d := w.Window.End.Sub(now); d > 0 {
		resetsInSeconds = int64(d.Seconds())
	}

	return subscriptionProgressView{
		WindowKind:      string(w.Window.Kind),
		Cap:             cap.String(),
		Consumed:        consumed.String(),
		Remaining:       remaining.String(),
		Overage:         w.OverageValue.String(),
		RequestCount:    w.RequestCount,
		WindowStart:     w.Window.Start,
		WindowEnd:       w.Window.End,
		UsagePercent:    usagePercent,
		ResetsInSeconds: resetsInSeconds,
		OverLimit:       overLimit,
		OverLimitAmount: overLimitAmount.String(),
	}
}

func subscriptionAllowsProgressWindow(s subscription.UserSubscription, kind quota.WindowKind) bool {
	switch kind {
	case quota.WindowCalendarDay:
		return s.DailyCapUSD != nil
	case quota.WindowCalendarWeek:
		return s.WeeklyCapUSD != nil
	case quota.WindowCalendarMonth:
		return s.MonthlyCapUSD != nil
	default:
		return false
	}
}

// ---- handler ----

// newUserCurrentSubscriptionHandler GET /me: 当前生效订阅 + 持久化 auto_renew。
func newUserCurrentSubscriptionHandler(d UserDeps) http.HandlerFunc {
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
		out := currentSubscriptionView{AutoRenew: false}
		if cur := currentActiveSubscription(subs, time.Now().UTC()); cur != nil {
			v := toSubscriptionView(*cur)
			out.Subscription = &v
			out.AutoRenew = cur.AutoRenew
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// newUserSubscriptionProgressHandler GET /me/progress: 按 quota window 返回当前订阅 cap 的用量。
func newUserSubscriptionProgressHandler(d UserDeps) http.HandlerFunc {
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
		out := subscriptionProgressResponse{Progress: []subscriptionProgressView{}}
		now := time.Now().UTC()
		cur := currentActiveSubscription(subs, now)
		if cur == nil {
			writeJSON(w, http.StatusOK, out)
			return
		}
		v := toSubscriptionView(*cur)
		out.Subscription = &v
		if d.Quota == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "quota_progress_unavailable", "subscription quota progress dependency unset")
			return
		}
		rows, err := d.Quota.ListCurrentWindowsForScope(
			r.Context(),
			ident.TenantID,
			quota.ScopeUser,
			strconv.FormatInt(ident.UserID, 10),
			now,
		)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "quota_progress_unavailable", "subscription quota progress unavailable")
			return
		}
		for _, row := range rows {
			if !subscriptionAllowsProgressWindow(*cur, row.Window.Kind) {
				continue
			}
			out.Progress = append(out.Progress, toSubscriptionProgressView(row, now))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// newUserCancelRenewHandler POST /cancel-renew: 只关闭自动续订, 不取消当前已生效权益。
func newUserCancelRenewHandler(d UserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d)
		if !ok {
			return
		}
		sub, err := d.Service.SetAutoRenew(r.Context(), ident.TenantID, ident.UserID, false)
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		v := toSubscriptionView(sub)
		writeJSON(w, http.StatusOK, currentSubscriptionView{Subscription: &v, AutoRenew: sub.AutoRenew})
	}
}

// newUserChangePlanHandler POST /change-plan {new_plan_id}: 自助换套餐
// 只允许升级；降级需要 admin 覆盖。
func newUserChangePlanHandler(d UserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d)
		if !ok {
			return
		}
		var req userChangePlanRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.NewPlanID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_plan", "new_plan_id must be positive")
			return
		}
		sub, err := d.Service.ChangePlan(r.Context(), subscription.ChangePlanInput{
			TenantID: ident.TenantID, UserID: ident.UserID, NewPlanID: req.NewPlanID,
			AllowDowngrade: false, RequestID: requestID(r),
		})
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subscription": toSubscriptionView(sub)})
	}
}

// newUserPurchaseHandler POST /purchase {plan_id}: 复用支付建一张 subscription 订单。
// 不在此 grant 订阅 —— 履约后 (admin confirm / provider webhook) 才授予; 返回支付指引订单。
func newUserPurchaseHandler(d UserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d)
		if !ok {
			return
		}
		if d.Payment == nil || d.TradeNoGen == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "subscription purchase dependency unset")
			return
		}
		var req purchaseRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.PlanID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_plan", "plan_id must be positive")
			return
		}
		// 先确认套餐存在且在售启用 (清晰 404/409, 而非建单时撞 service 内部校验回模糊错)。
		plan, err := d.Service.GetPlan(r.Context(), ident.TenantID, req.PlanID)
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		if !plan.Enabled || !plan.ForSale {
			writeJSONError(w, http.StatusConflict, "plan_not_for_sale", "subscription plan is not available for purchase")
			return
		}
		outTradeNo, err := d.TradeNoGen(ident.TenantID)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "external_trade_no_failed", "failed to allocate purchase order id")
			return
		}
		planID := req.PlanID
		res, err := d.Payment.CreateOrder(r.Context(), payment.CreateOrderInput{
			TenantID:           ident.TenantID,
			UserID:             ident.UserID,
			OutTradeNo:         outTradeNo,
			OrderKind:          payment.OrderKindSubscription,
			SubscriptionPlanID: &planID,
			ActorKind:          payment.ActorKindUser,
			ActorID:            ident.UserID,
			RequestID:          requestID(r),
		})
		if err != nil {
			writePaymentOrderError(w, err)
			return
		}
		status := http.StatusCreated
		if res.Idempotent {
			status = http.StatusOK
		}
		writeJSON(w, status, map[string]any{
			"order":      toPurchaseOrderView(res.Order),
			"idempotent": res.Idempotent,
			// 支付指引: 用户拿 out_trade_no 去既有支付通道完成付款, 履约后订阅才生效。
			"payment_instruction": "complete payment for out_trade_no via your configured payment provider; the subscription is granted after the order is confirmed",
		})
	}
}

// writePaymentOrderError 把订阅购买路径的 payment 建单错误映射为 HTTP 响应。
func writePaymentOrderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, payment.ErrInvalidInput), errors.Is(err, payment.ErrInvalidAmount):
		writeJSONError(w, http.StatusBadRequest, "invalid_purchase_request", "subscription purchase request is invalid")
	case errors.Is(err, payment.ErrUnsupportedCurrency):
		writeJSONError(w, http.StatusBadRequest, "unsupported_currency", "currency not supported")
	case errors.Is(err, payment.ErrIdempotencyConflict):
		writeJSONError(w, http.StatusConflict, "out_trade_no_conflict", "purchase order id conflict, please retry")
	case errors.Is(err, payment.ErrSubscriptionOrderRequiresPG):
		writeJSONError(w, http.StatusServiceUnavailable, "subscription_order_requires_pg", "subscription purchase requires postgres store")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "payment_backend_error", "payment service unavailable")
	}
}
