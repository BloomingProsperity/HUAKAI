// HUAKAI · iKun

package payment

import (
	"context"
	"strings"
)

// ConfirmPaidByCallback 自动入账路径 (P2a): 验签可信回调 → 解析归一化 → 解析本地订单 →
// 一致性校验 → 复用 P1 ConfirmPaid(system) + Fulfill(system)。与管理员手动路径共用唯一入账核心,
// 不另开第二条写 payment_credits / billing_events 的路径。
//
// 安全不变量:
//   - tenant 取自已验签的回调体 (CallbackResult.TenantID), 绝不取自 URL/query/未验签 JSON — 防越权路由。
//   - 验签失败 → 立即返回, 零 DB 交互, 零入账 (伪造回调不得触达订单)。
//   - 验签通过但金额/币种/渠道与本地单不符 → 拒, 零入账 (验签≠授权入任意额)。
//   - 重放安全靠 P1 三重幂等 (out_trade_no 唯一 + ConfirmPaid CAS + 一单一 credit): 同一回调多次 → 仅入账一次。
func (s *Service) ConfirmPaidByCallback(ctx context.Context, providerKind ProviderKind, rawBody []byte, signature string) (FulfillResult, error) {
	provider, err := s.providers.resolve(providerKind)
	if err != nil {
		return FulfillResult{}, err // ErrProviderUnknown
	}
	verifier, ok := provider.(CallbackVerifier)
	if !ok {
		return FulfillResult{}, ErrProviderNoCallback // 如 manual provider: 不走回调入账
	}
	result, err := verifier.VerifyCallback(rawBody, signature)
	if err != nil {
		return FulfillResult{}, err // ErrCallbackUnverified — 零入账, 不触达订单
	}

	// 已验签的 tenant + 外部订单号定位本地订单 (tenant-scoped, 杜绝跨租户)。
	order, err := s.store.GetOrderByOutTradeNo(ctx, result.TenantID, result.OutTradeNo)
	if err != nil {
		return FulfillResult{}, err // ErrOrderNotFound
	}
	// 一致性: 回调到 X 渠道端点, 订单也必须是 X 渠道所建; 且金额/币种须与本地单一致。
	// 任一不符 = 验签通过但业务非法, 拒且零入账 (审计上靠订单停在原状态 + 无 paid_confirmed 体现; Owner 决策 A 不单建拒绝事件类型)。
	if order.ProviderKind != providerKind {
		return FulfillResult{}, ErrCallbackRejected
	}
	if order.AmountCents != result.PaidAmountCents {
		return FulfillResult{}, ErrCallbackRejected
	}
	if result.CurrencyCode != "" && !strings.EqualFold(result.CurrencyCode, order.CurrencyCode) {
		return FulfillResult{}, ErrCallbackRejected
	}

	// 复用 P1: 系统归属的确认 + 两段式履约 (幂等)。
	if _, err := s.store.ConfirmPaid(ctx, confirmRecord{
		TenantID:      order.TenantID,
		OrderID:       order.ID,
		ActorKind:     ActorKindSystem,
		ConfirmReason: "callback_confirmed:" + string(providerKind),
		RequestID:     result.ProviderRef,
		Now:           s.now(),
	}); err != nil {
		return FulfillResult{}, err
	}
	return s.Fulfill(ctx, FulfillInput{
		TenantID:  order.TenantID,
		OrderID:   order.ID,
		ActorKind: ActorKindSystem,
		RequestID: result.ProviderRef,
	})
}
