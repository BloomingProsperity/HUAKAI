// HUAKAI · iKun

package payment

import (
	"context"
	"strings"
	"time"
)

// Service 支付外观: 规范化输入 / 校验 / 编排订单状态机 / 解析 provider。
type Service struct {
	store       Store
	providers   providerRegistry
	now         func() time.Time
	tradeNoGen  ExternalTradeNoGenerator
	maxAttempts int
}

// Option 配置 Service。
type Option func(*Service)

// WithClock 注入时钟 (测试用)。
func WithClock(clock func() time.Time) Option {
	return func(s *Service) {
		if clock != nil {
			s.now = clock
		}
	}
}

// WithExternalTradeNoGenerator 注入用户充值开单的订单号生成器。
func WithExternalTradeNoGenerator(gen ExternalTradeNoGenerator) Option {
	return func(s *Service) {
		if gen != nil {
			s.tradeNoGen = gen
		}
	}
}

// WithTestProvider 启用 test provider (默认关闭, 仅测试 / 本地)。
func WithTestProvider() Option {
	return func(s *Service) {
		s.providers[ProviderTest] = NewTestProvider()
	}
}

// WithTestProviderSecret 启用带指定 HMAC 验签密钥的 test provider (回调链路测试用)。
// 真实渠道密钥走 Owner-gated P-RealMoney, 不经此 option。
func WithTestProviderSecret(secret string) Option {
	return func(s *Service) {
		s.providers[ProviderTest] = NewTestProviderWithSecret(secret)
	}
}

// NewService 构造支付 service。默认只注册 manual provider。
func NewService(store Store, opts ...Option) *Service {
	s := &Service{
		store:       store,
		providers:   newProviderRegistry(NewManualProvider(), NewHMACProvider()),
		now:         func() time.Time { return time.Now().UTC() },
		tradeNoGen:  randomExternalTradeNo,
		maxAttempts: 3,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// CreateOrder 建一张 pending 订单 (幂等 out_trade_no)。
func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (CreateOrderResult, error) {
	if in.TenantID <= 0 || in.UserID <= 0 {
		return CreateOrderResult{}, ErrInvalidInput
	}
	// amount 必须为正且不超过账本可表示上限 (防 billing_events numeric(20,8) 溢出后卡单)。
	if in.AmountCents <= 0 {
		return CreateOrderResult{}, ErrInvalidAmount
	}
	if in.AmountCents > maxAmountCents {
		return CreateOrderResult{}, ErrInvalidAmount
	}
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return CreateOrderResult{}, err
	}
	kind := providerKindOrDefault(in.ProviderKind)
	provider, err := s.providers.resolve(kind)
	if err != nil {
		return CreateOrderResult{}, err
	}
	// out_trade_no 必须由 caller 提供且稳定 — 不再 server 端每次生成, 否则重试会建出第二张可入账订单 (双账)。
	outTradeNo := strings.TrimSpace(in.OutTradeNo)
	if outTradeNo == "" {
		return CreateOrderResult{}, ErrInvalidInput
	}
	if err := validateOutTradeNo(outTradeNo); err != nil {
		return CreateOrderResult{}, err
	}

	// order_kind: 缺省充值; 订阅单必须带有效套餐指针。
	orderKind := orderKindOrDefault(in.OrderKind)
	if orderKind != OrderKindTopup && orderKind != OrderKindSubscription {
		return CreateOrderResult{}, ErrInvalidInput
	}
	if orderKind == OrderKindSubscription && (in.SubscriptionPlanID == nil || *in.SubscriptionPlanID <= 0) {
		return CreateOrderResult{}, ErrInvalidInput
	}

	now := s.now()
	ttl := in.ExpiresIn
	if ttl <= 0 {
		ttl = defaultOrderTTL
	}
	expiresAt := now.Add(ttl)

	intent, err := provider.CreateIntent(ctx, Order{
		TenantID: in.TenantID, UserID: in.UserID, OutTradeNo: outTradeNo,
		AmountCents: in.AmountCents, CurrencyCode: currency, ProviderKind: kind,
	})
	if err != nil {
		return CreateOrderResult{}, err
	}

	order, replay, err := s.store.CreateOrder(ctx, createOrderRecord{
		TenantID:           in.TenantID,
		UserID:             in.UserID,
		OutTradeNo:         outTradeNo,
		AmountCents:        in.AmountCents,
		CurrencyCode:       currency,
		ProviderKind:       kind,
		ProviderOrderRef:   intent.OrderRef,
		RequestFingerprint: strings.TrimSpace(in.RequestFingerprint),
		CreatedByAdminID:   in.ActorAdminID,
		CreatedActorKind:   createOrderActorKind(in),
		CreatedActorID:     createOrderActorID(in),
		RequestID:          in.RequestID,
		ExpiresAt:          &expiresAt,
		OrderKind:          orderKind,
		SubscriptionPlanID: in.SubscriptionPlanID,
		Now:                now,
	})
	if err != nil {
		return CreateOrderResult{}, err
	}
	return CreateOrderResult{Order: order, Idempotent: replay}, nil
}

func createOrderActorKind(in CreateOrderInput) string {
	if in.ActorKind != "" {
		return in.ActorKind
	}
	if in.ActorAdminID > 0 {
		return ActorKindAdmin
	}
	return ActorKindSystem
}

func createOrderActorID(in CreateOrderInput) int64 {
	if in.ActorID > 0 {
		return in.ActorID
	}
	if in.ActorAdminID > 0 {
		return in.ActorAdminID
	}
	return 0
}

// AdminConfirmPaid 管理员手动确认支付并触发履约 (CAS pending->paid, 然后 Fulfill)。
func (s *Service) AdminConfirmPaid(ctx context.Context, in AdminConfirmPaidInput) (FulfillResult, error) {
	if in.TenantID <= 0 || in.OrderID <= 0 {
		return FulfillResult{}, ErrInvalidInput
	}
	if _, err := s.store.ConfirmPaid(ctx, confirmRecord{
		TenantID:      in.TenantID,
		OrderID:       in.OrderID,
		AdminID:       in.ActorAdminID,
		ActorKind:     ActorKindAdmin, // P1 手动确认显式归属 admin (区别于 P2a 回调的 system)。
		ConfirmReason: in.ConfirmReason,
		RequestID:     in.RequestID,
		Now:           s.now(),
	}); err != nil {
		return FulfillResult{}, err
	}
	return s.Fulfill(ctx, FulfillInput{
		TenantID:  in.TenantID,
		OrderID:   in.OrderID,
		ActorKind: ActorKindAdmin,
		ActorID:   in.ActorAdminID,
		RequestID: in.RequestID,
	})
}

// Fulfill 两段式履约: phase1 推进 recharging (持久) → phase2 入账 + completed。
// 对已完成订单幂等返回, 不重复入账。
func (s *Service) Fulfill(ctx context.Context, in FulfillInput) (FulfillResult, error) {
	if in.TenantID <= 0 || in.OrderID <= 0 {
		return FulfillResult{}, ErrInvalidInput
	}
	if _, _, err := s.store.BeginFulfill(ctx, fulfillRecord{
		TenantID:  in.TenantID,
		OrderID:   in.OrderID,
		ActorKind: in.ActorKind,
		ActorID:   in.ActorID,
		RequestID: in.RequestID,
		Now:       s.now(),
	}); err != nil {
		return FulfillResult{}, err
	}
	return s.store.CompleteFulfill(ctx, fulfillRecord{
		TenantID:  in.TenantID,
		OrderID:   in.OrderID,
		ActorKind: in.ActorKind,
		ActorID:   in.ActorID,
		RequestID: in.RequestID,
		Now:       s.now(),
	})
}

// GetOrder 读订单。
func (s *Service) GetOrder(ctx context.Context, tenantID, orderID int64) (Order, error) {
	if tenantID <= 0 || orderID <= 0 {
		return Order{}, ErrInvalidInput
	}
	return s.store.GetOrder(ctx, tenantID, orderID)
}

// GetBalance 用户支付来源余额 (payment_credits 派生 SUM)。
func (s *Service) GetBalance(ctx context.Context, tenantID, userID int64) (Balance, error) {
	if tenantID <= 0 || userID <= 0 {
		return Balance{}, ErrInvalidInput
	}
	cents, err := s.store.UserBalanceCents(ctx, tenantID, userID)
	if err != nil {
		return Balance{}, err
	}
	return Balance{TenantID: tenantID, UserID: userID, AmountCents: cents}, nil
}

// ListOrders 列某用户订单。
func (s *Service) ListOrders(ctx context.Context, tenantID, userID int64, limit int) ([]Order, error) {
	if tenantID <= 0 || userID <= 0 {
		return nil, ErrInvalidInput
	}
	return s.store.ListOrdersByUser(ctx, tenantID, userID, limit)
}

// ListAuditEvents 列某订单操作审计轨迹。
func (s *Service) ListAuditEvents(ctx context.Context, tenantID, orderID int64) ([]AuditEvent, error) {
	if tenantID <= 0 || orderID <= 0 {
		return nil, ErrInvalidInput
	}
	return s.store.ListAuditEvents(ctx, tenantID, orderID)
}

// normalizeCurrency: P1 账本 (billing_events.actual_cost / 派生余额 SUM) 仅 USD, 无币种维度。
// 非 USD 会被当 USD 入账 → 余额语义被污染, 故 P1 一律拒绝; 多币种留 P2+ (账本加币种维度后)。
func normalizeCurrency(c string) (string, error) {
	c = strings.ToUpper(strings.TrimSpace(c))
	if c == "" {
		return "USD", nil
	}
	if c != "USD" {
		return "", ErrUnsupportedCurrency
	}
	return c, nil
}
