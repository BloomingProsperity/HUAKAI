// HUAKAI · iKun

package payment

import "context"

// PaymentIntent provider 为订单创建支付意图后的安全返回 — 只含可公开的引用与快照, 无密钥/敏感 payload。
type PaymentIntent struct {
	OrderRef string
	Snapshot map[string]any
}

// Provider 抽象支付渠道行为, 不耦合任何真实 SDK 类型。
// P1 仅 manual / test; 真实 Stripe/支付宝/微信/epay provider 留 Owner-gated 后续切片
// (引入真实密钥 / webhook 验签 / 退款撤销语义 / SDK 供应链风险)。
type Provider interface {
	Kind() ProviderKind
	CreateIntent(ctx context.Context, order Order) (PaymentIntent, error)
}

// manualProvider 生产可用但只能管理员手动确认; 不接触任何真实商户密钥。
type manualProvider struct{}

// NewManualProvider 返回手动确认 provider。
func NewManualProvider() Provider { return manualProvider{} }

func (manualProvider) Kind() ProviderKind { return ProviderManual }

func (manualProvider) CreateIntent(_ context.Context, _ Order) (PaymentIntent, error) {
	// manual 无外部跳转, 等管理员手动确认支付。
	return PaymentIntent{}, nil
}

// testProvider 仅测试 / 本地配置启用; 任何生产配置默认关闭。
type testProvider struct{}

// NewTestProvider 返回测试 provider。
func NewTestProvider() Provider { return testProvider{} }

func (testProvider) Kind() ProviderKind { return ProviderTest }

func (testProvider) CreateIntent(_ context.Context, order Order) (PaymentIntent, error) {
	return PaymentIntent{OrderRef: "test-ref-" + order.OutTradeNo}, nil
}

// providerRegistry 按 kind 解析 provider。
type providerRegistry map[ProviderKind]Provider

func newProviderRegistry(providers ...Provider) providerRegistry {
	reg := make(providerRegistry, len(providers))
	for _, p := range providers {
		if p != nil {
			reg[p.Kind()] = p
		}
	}
	return reg
}

func (r providerRegistry) resolve(kind ProviderKind) (Provider, error) {
	if kind == "" {
		kind = ProviderManual
	}
	p, ok := r[kind]
	if !ok {
		return nil, ErrProviderUnknown
	}
	return p, nil
}
