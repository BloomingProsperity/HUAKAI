// HUAKAI · iKun

package paymenthttp

import (
	"context"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

// 用户支付门户 (wave-5): 用户自助充值开单 / 订单详情轮询 / 可充配置 / 退款申请。
// 安全立场:
//   - 一切租户/用户身份只来自 session, 永不取自请求体 (防越权改单)。
//   - 订单详情与退款申请都做归属校验: 只能看 / 操作自己的单 (跨用户读返回 404, 不泄露存在性)。
//   - 退款申请只建一条 pending 记录待 admin 审批, 绝不调用任何入账/退款资金路径
//     (用户不能自助退钱 — money 决策权只在 admin)。

// 默认可充金额范围 ($1.00 ~ $5,000.00, 以分计)。门户配置可覆盖。
const (
	defaultPortalMinTopupCents int64 = 100
	defaultPortalMaxTopupCents int64 = 500_000
)

// 门户默认启用的支付指引渠道 (manual 扫码 / taobao 闲鱼下单, 均人工确认入账)。
var defaultPortalProviders = []payment.ProviderKind{payment.ProviderManual, payment.ProviderTaobao}

// PortalConfig 用户支付门户运行时配置 (可充金额范围 + 启用的支付指引渠道)。
// 零值字段回落到安全默认; 由 cmd/gateway 在 UserDeps 注入。
type PortalConfig struct {
	MinTopupCents     int64
	MaxTopupCents     int64
	PresetAmountCents []int64
	EnabledProviders  []payment.ProviderKind
	ManualInstruction string
	TaobaoInstruction string
}

func (c PortalConfig) minCents() int64 {
	if c.MinTopupCents > 0 {
		return c.MinTopupCents
	}
	return defaultPortalMinTopupCents
}

func (c PortalConfig) maxCents() int64 {
	if c.MaxTopupCents > 0 {
		return c.MaxTopupCents
	}
	return defaultPortalMaxTopupCents
}

func (c PortalConfig) presetAmounts() []int64 {
	minCents := c.minCents()
	maxCents := c.maxCents()
	out := make([]int64, 0, len(c.PresetAmountCents))
	for _, amount := range c.PresetAmountCents {
		if amount < minCents || amount > maxCents {
			continue
		}
		out = append(out, amount)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (c PortalConfig) providers() []payment.ProviderKind {
	if len(c.EnabledProviders) > 0 {
		return c.EnabledProviders
	}
	return defaultPortalProviders
}

// providerEnabled 报告 provider 是否在门户启用集合内 (归一化大小写)。
func (c PortalConfig) providerEnabled(kind payment.ProviderKind) bool {
	want := payment.ProviderKind(normalizeProviderName(string(kind)))
	for _, p := range c.providers() {
		if payment.ProviderKind(normalizeProviderName(string(p))) == want {
			return true
		}
	}
	return false
}

// instructionFor 返回某渠道的人工支付指引文案 (manual/taobao 默认有兜底文案)。
func (c PortalConfig) instructionFor(kind payment.ProviderKind) string {
	switch payment.ProviderKind(normalizeProviderName(string(kind))) {
	case payment.ProviderTaobao:
		if strings.TrimSpace(c.TaobaoInstruction) != "" {
			return c.TaobaoInstruction
		}
		return "Place a Taobao/Xianyu order for the listed amount, then wait for an operator to confirm your top-up."
	case payment.ProviderManual:
		if strings.TrimSpace(c.ManualInstruction) != "" {
			return c.ManualInstruction
		}
		return "Transfer the listed amount via the manual channel, then wait for an operator to confirm your top-up."
	default:
		return "Complete payment for the listed amount, then wait for an operator to confirm your top-up."
	}
}

// RefundRequestStatus 退款申请状态机: 用户建 pending, admin 后续 approve/reject。
// 资金不在本状态机里动 — approve 仍需 admin 走 RefundOrder 资金路径 (roadmap)。
type RefundRequestStatus string

const (
	RefundRequestPending  RefundRequestStatus = "pending"
	RefundRequestApproved RefundRequestStatus = "approved"
	RefundRequestRejected RefundRequestStatus = "rejected"
)

// RefundRequest 一条用户发起的退款申请 (待 admin 审批; 不含任何资金事实)。
type RefundRequest struct {
	ID        int64               `json:"id"`
	TenantID  int64               `json:"tenant_id"`
	UserID    int64               `json:"user_id"`
	OrderID   int64               `json:"order_id"`
	Status    RefundRequestStatus `json:"status"`
	Reason    string              `json:"reason,omitempty"`
	CreatedAt time.Time           `json:"created_at"`
	DecidedAt *time.Time          `json:"decided_at,omitempty"`
	DecidedBy int64               `json:"decided_by,omitempty"`
}

// RefundRequestInput 建退款申请入参 (身份字段全部来自 session)。
type RefundRequestInput struct {
	TenantID int64
	UserID   int64
	OrderID  int64
	Reason   string
	Now      time.Time
}

// RefundRequestRecorder 持久化退款申请 (最小状态机)。默认内存实现; cmd/gateway 可换 PG。
// Create 只写一条 pending 记录, 实现方绝不触碰资金账本。
type RefundRequestRecorder interface {
	CreateRefundRequest(ctx context.Context, in RefundRequestInput) (RefundRequest, error)
	ListPendingRefundRequests(ctx context.Context, tenantID int64) ([]RefundRequest, error)
	ApproveRefundRequest(ctx context.Context, tenantID, requestID, adminActorID int64) (RefundRequest, error)
	RejectRefundRequest(ctx context.Context, tenantID, requestID int64, reason string, adminActorID int64) (RefundRequest, error)
}

// memoryRefundRequestRecorder 进程内退款申请记录 (MVP 兜底)。
// 一张订单同一租户至多一条申请, 重复申请返回既有记录 (幂等, 不重复建)。
type memoryRefundRequestRecorder struct {
	mu     sync.Mutex
	nextID int64
	byKey  map[refundRequestKey]RefundRequest
	byID   map[int64]RefundRequest
	refund refundRequestMoneyService
}

type refundRequestKey struct {
	tenantID int64
	orderID  int64
}

// NewMemoryRefundRequestRecorder 构造进程内退款申请记录器 (MVP / 测试默认)。
func NewMemoryRefundRequestRecorder() RefundRequestRecorder {
	return NewMemoryRefundRequestRecorderWithRefunds(nil)
}

// NewMemoryRefundRequestRecorderWithRefunds 构造可执行 admin approve 的内存记录器。
func NewMemoryRefundRequestRecorderWithRefunds(refund refundRequestMoneyService) RefundRequestRecorder {
	return &memoryRefundRequestRecorder{
		byKey:  map[refundRequestKey]RefundRequest{},
		byID:   map[int64]RefundRequest{},
		refund: refund,
	}
}

func (m *memoryRefundRequestRecorder) CreateRefundRequest(_ context.Context, in RefundRequestInput) (RefundRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := refundRequestKey{tenantID: in.TenantID, orderID: in.OrderID}
	if existing, ok := m.byKey[key]; ok {
		return existing, nil
	}
	m.nextID++
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rec := RefundRequest{
		ID:        m.nextID,
		TenantID:  in.TenantID,
		UserID:    in.UserID,
		OrderID:   in.OrderID,
		Status:    RefundRequestPending,
		Reason:    strings.TrimSpace(in.Reason),
		CreatedAt: now.UTC(),
	}
	m.byKey[key] = rec
	m.byID[rec.ID] = rec
	return rec, nil
}

// ---- DTO ----

type portalCreateTopupRequest struct {
	AmountCents  int64  `json:"amount_cents"`
	Provider     string `json:"provider"`
	TermsVersion string `json:"terms_version,omitempty"`
}

type portalProviderConfigView struct {
	Provider    string `json:"provider"`
	Instruction string `json:"instruction"`
}

type portalConfigView struct {
	MinTopupCents     int64                      `json:"min_topup_cents"`
	MaxTopupCents     int64                      `json:"max_topup_cents"`
	PresetAmountCents []int64                    `json:"preset_amount_cents"`
	CurrencyCode      string                     `json:"currency_code"`
	Providers         []portalProviderConfigView `json:"providers"`
}

type refundRequestView struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
	OrderID   int64  `json:"order_id"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	CreatedAt string `json:"created_at"`
	DecidedAt string `json:"decided_at,omitempty"`
	DecidedBy int64  `json:"decided_by,omitempty"`
}

func toRefundRequestView(rr RefundRequest) refundRequestView {
	view := refundRequestView{
		ID:        rr.ID,
		TenantID:  rr.TenantID,
		UserID:    rr.UserID,
		OrderID:   rr.OrderID,
		Status:    string(rr.Status),
		Reason:    rr.Reason,
		CreatedAt: rr.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if rr.DecidedAt != nil {
		view.DecidedAt = rr.DecidedAt.UTC().Format(time.RFC3339Nano)
	}
	if rr.DecidedBy > 0 {
		view.DecidedBy = rr.DecidedBy
	}
	return view
}

type portalRefundRequestBody struct {
	Reason string `json:"reason,omitempty"`
}

// ---- handlers ----

// newPortalConfigHandler 返回门户可充配置 (金额范围 + 启用的支付指引渠道)。
// 仅暴露安全的运行时配置, 无身份依赖 (但仍在 session 保护组下)。
func newPortalConfigHandler(d UserDeps) http.HandlerFunc {
	cfg := d.Portal
	return func(w http.ResponseWriter, r *http.Request) {
		providers := cfg.providers()
		views := make([]portalProviderConfigView, 0, len(providers))
		for _, p := range providers {
			views = append(views, portalProviderConfigView{
				Provider:    normalizeProviderName(string(p)),
				Instruction: cfg.instructionFor(p),
			})
		}
		sort.Slice(views, func(i, j int) bool { return views[i].Provider < views[j].Provider })
		writeJSON(w, http.StatusOK, map[string]any{"config": portalConfigView{
			MinTopupCents:     cfg.minCents(),
			MaxTopupCents:     cfg.maxCents(),
			PresetAmountCents: cfg.presetAmounts(),
			CurrencyCode:      "USD",
			Providers:         views,
		}})
	}
}

// newPortalCreateTopupHandler 用户自助创建一张充值 (topup) 订单。
// 身份/租户取自 session; out_trade_no 由服务端生成 (tenant-routable); order_kind 强制 topup;
// 金额必须落在门户配置区间内 (服务端裁决, 非客户端报价)。返回订单视图 + 该渠道人工支付指引。
func newPortalCreateTopupHandler(d UserDeps) http.HandlerFunc {
	cfg := d.Portal
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "payment dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		var req portalCreateTopupRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		providerKind := payment.ProviderKind(normalizeProviderName(req.Provider))
		if providerKind == "" {
			writeJSONError(w, http.StatusBadRequest, "payment_provider_required", "provider is required")
			return
		}
		if !cfg.providerEnabled(providerKind) {
			writeJSONError(w, http.StatusBadRequest, "payment_provider_unavailable", "payment provider is not enabled for self-service top-up")
			return
		}
		if req.AmountCents < cfg.minCents() || req.AmountCents > cfg.maxCents() {
			writeJSONError(w, http.StatusBadRequest, "topup_amount_out_of_range", "amount_cents must be within the configured top-up range")
			return
		}
		outTradeNo, err := ExternalTradeNoForTenant(ident.TenantID)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "external_trade_no_failed", "failed to generate top-up order id")
			return
		}
		termsVersion := strings.TrimSpace(req.TermsVersion)
		var acceptedAt *time.Time
		var acceptedBy int64
		var acceptedIP string
		if termsVersion != "" {
			now := time.Now().UTC()
			acceptedAt = &now
			acceptedBy = ident.UserID
			acceptedIP = portalComplianceClientIP(r, d)
		}
		res, err := d.Service.CreateOrder(r.Context(), payment.CreateOrderInput{
			TenantID:               ident.TenantID,
			UserID:                 ident.UserID,
			AmountCents:            req.AmountCents,
			OutTradeNo:             outTradeNo,
			ProviderKind:           providerKind,
			OrderKind:              payment.OrderKindTopup,
			ActorKind:              payment.ActorKindUser,
			ActorID:                ident.UserID,
			RequestID:              requestID(r),
			ComplianceTermsVersion: termsVersion,
			ComplianceAcceptedAt:   acceptedAt,
			ComplianceAcceptedBy:   acceptedBy,
			ComplianceAcceptedIP:   acceptedIP,
		})
		if err != nil {
			writePaymentError(w, err)
			return
		}
		status := http.StatusCreated
		if res.Idempotent {
			status = http.StatusOK
		}
		writeJSON(w, status, map[string]any{
			"order":      toOrderView(res.Order),
			"idempotent": res.Idempotent,
			"payment_instruction": portalProviderConfigView{
				Provider:    normalizeProviderName(string(providerKind)),
				Instruction: cfg.instructionFor(providerKind),
			},
		})
	}
}

func portalComplianceClientIP(r *http.Request, d UserDeps) string {
	ip := strings.TrimSpace(d.ClientIPResolver.ClientIP(r))
	if ip == "" {
		return ""
	}
	if _, err := netip.ParseAddr(ip); err != nil {
		return ""
	}
	return ip
}

// newPortalGetOrderHandler 单订单详情/状态 (供前端轮询)。
// 归属校验: 先按 tenant+id 读单, 再校验 order.UserID == session.UserID;
// 跨用户访问返回 404 (不泄露他人订单存在性)。删除该归属校验 → 跨用户能读到别人订单。
func newPortalGetOrderHandler(d UserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "payment dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		order, err := d.Service.GetOrder(r.Context(), ident.TenantID, id)
		if err != nil {
			writePaymentError(w, err)
			return
		}
		// 归属校验: 只能看自己的订单。不属于本人 → 当作 not found (避免存在性泄露)。
		if order.UserID != ident.UserID {
			writeJSONError(w, http.StatusNotFound, "order_not_found", "payment order not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"order": toOrderView(order)})
	}
}

// newPortalRefundRequestHandler 用户发起退款申请 → 建一条 pending 记录待 admin 审批。
// money 立场: 绝不调用 RefundOrder / 任何入账路径; 仅记录申请意图。
// 归属校验: 只能为自己的订单申请; 跨用户订单返回 404。
func newPortalRefundRequestHandler(d UserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "payment dependency unset")
			return
		}
		recorder := d.RefundRequests
		if recorder == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "refund_requests_unavailable", "refund request intake is not configured")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req portalRefundRequestBody
		// requestBody is optional (OpenAPI required:false); only decode when a body is present
		if r.ContentLength != 0 && r.Body != http.NoBody {
			if !decodeJSON(w, r, &req) {
				return
			}
		}
		order, err := d.Service.GetOrder(r.Context(), ident.TenantID, id)
		if err != nil {
			writePaymentError(w, err)
			return
		}
		if order.UserID != ident.UserID {
			writeJSONError(w, http.StatusNotFound, "order_not_found", "payment order not found")
			return
		}
		// 只对已入账完成的充值单可申请退款; 其它状态不可申请 (用户能申请, 但前置门槛在此)。
		if order.OrderKind != payment.OrderKindTopup || order.Status != payment.StatusCompleted {
			writeJSONError(w, http.StatusConflict, "order_not_refund_requestable", "only a completed top-up order can be refund-requested")
			return
		}
		rr, err := recorder.CreateRefundRequest(r.Context(), RefundRequestInput{
			TenantID: ident.TenantID,
			UserID:   ident.UserID,
			OrderID:  id,
			Reason:   req.Reason,
			Now:      time.Now().UTC(),
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "refund_request_failed", "failed to record refund request")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"refund_request": toRefundRequestView(rr)})
	}
}
