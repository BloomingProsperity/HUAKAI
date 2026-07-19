// HUAKAI · iKun

package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// PaymentIntent provider 为订单创建支付意图后的安全返回 — 只含可公开的引用与快照, 无密钥/敏感 payload。
type PaymentIntent struct {
	OrderRef string
	Snapshot map[string]any
}

var ErrProviderOperationNotSupported = errors.New("payment: provider operation not supported")

type ProviderOrderState struct {
	Status string
	Raw    map[string]string
}

type ProviderRefundResult struct {
	ProviderRefundID string
	Status           string
}

// Provider 抽象支付渠道行为, 不耦合任何真实 SDK 类型。
// 当前内置 manual/test/HMAC provider；它们不支持的渠道操作必须返回明确错误，
// 调用链不得把本地余额变化冒充为外部渠道已经完成退款或撤销。
type Provider interface {
	Kind() ProviderKind
	CreateIntent(ctx context.Context, order Order) (PaymentIntent, error)
	QueryOrder(ctx context.Context, order Order) (ProviderOrderState, error)
	Refund(ctx context.Context, order Order, amountCents int64) (ProviderRefundResult, error)
	Cancel(ctx context.Context, order Order) error
}

// CallbackResult 是 provider 对一条回调验签通过后, 归一化出的可信字段。
// 关键: tenant 来自已验签的回调体, 不来自 URL/query — 防越权路由 (见 ConfirmPaidByCallback)。
type CallbackResult struct {
	TenantID        int64
	OutTradeNo      string
	PaidAmountCents int64
	CurrencyCode    string
	ProviderRef     string
}

// CallbackVerifier 由支持自动回调入账的 provider 实现 (P2a: 仅 test provider)。
// manual provider 不实现 → webhook 对它返 ErrProviderNoCallback (手动路径不走回调)。
// 入参 signature 由 HTTP 层从签名头取出; 实现侧用密钥重算并常量时间比较, 通过才解析字段。
type CallbackVerifier interface {
	VerifyCallback(rawBody []byte, signature string) (CallbackResult, error)
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

func (manualProvider) QueryOrder(_ context.Context, _ Order) (ProviderOrderState, error) {
	return ProviderOrderState{}, ErrProviderOperationNotSupported
}

func (manualProvider) Refund(_ context.Context, _ Order, _ int64) (ProviderRefundResult, error) {
	return ProviderRefundResult{}, ErrProviderOperationNotSupported
}

func (manualProvider) Cancel(_ context.Context, _ Order) error {
	return ErrProviderOperationNotSupported
}

// taobaoProvider 淘宝/闲鱼 manual-redirect provider: CreateIntent 返回运营配置的淘宝/闲鱼
// 商品链接(供前端渲染二维码 / 用户点击跳转)+ 订单号(让用户在淘宝/闲鱼下单备注里填写,
// 运营据此对账)。它**不接触任何真实商户密钥, 不实现 CallbackVerifier** —— 淘宝/闲鱼没有
// 程序回调, 付款后由管理员手动确认入账(走与 manual provider 相同的确认路径)。
type taobaoProvider struct{ checkoutURL string }

// NewTaobaoProvider 返回淘宝/闲鱼 manual-redirect provider。checkoutURL 为运营配置的
// 淘宝/闲鱼商品/店铺链接。
func NewTaobaoProvider(checkoutURL string) Provider {
	return taobaoProvider{checkoutURL: strings.TrimSpace(checkoutURL)}
}

func (taobaoProvider) Kind() ProviderKind { return ProviderTaobao }

func (p taobaoProvider) CreateIntent(_ context.Context, order Order) (PaymentIntent, error) {
	return PaymentIntent{
		OrderRef: order.OutTradeNo,
		Snapshot: map[string]any{
			"marketplace":  "taobao_xianyu",
			"checkout_url": p.checkoutURL,
			"qr_content":   p.checkoutURL, // 前端据此渲染二维码
			"out_trade_no": order.OutTradeNo,
			"amount_cents": order.AmountCents,
			"currency":     order.CurrencyCode,
			"confirm_mode": "manual",
			"instructions": "扫码或点击链接前往淘宝/闲鱼下单付款, 务必在备注/留言填写订单号 " + order.OutTradeNo + ", 付款后等待管理员核对入账。",
		},
	}, nil
}

func (taobaoProvider) QueryOrder(_ context.Context, _ Order) (ProviderOrderState, error) {
	return ProviderOrderState{}, ErrProviderOperationNotSupported
}

func (taobaoProvider) Refund(_ context.Context, _ Order, _ int64) (ProviderRefundResult, error) {
	return ProviderRefundResult{}, ErrProviderOperationNotSupported
}

func (taobaoProvider) Cancel(_ context.Context, _ Order) error {
	return ErrProviderOperationNotSupported
}

// WithTaobaoProvider 启用淘宝/闲鱼 manual-redirect provider (默认关闭, 由配置开关控制)。
func WithTaobaoProvider(checkoutURL string) Option {
	return func(s *Service) {
		s.providers[ProviderTaobao] = NewTaobaoProvider(checkoutURL)
	}
}

type hmacProvider struct{}

// NewHMACProvider 返回 HTTP HMAC 桥接 provider。它不自己验签; 回调由 paymenthttp
// 先按配置 provider 名和密钥验签, 再把可信结果交给支付状态机。
func NewHMACProvider() Provider { return hmacProvider{} }

func (hmacProvider) Kind() ProviderKind { return ProviderHMAC }

func (hmacProvider) CreateIntent(_ context.Context, order Order) (PaymentIntent, error) {
	return PaymentIntent{OrderRef: "hmac-ref-" + order.OutTradeNo}, nil
}

func (hmacProvider) QueryOrder(_ context.Context, _ Order) (ProviderOrderState, error) {
	return ProviderOrderState{}, ErrProviderOperationNotSupported
}

func (hmacProvider) Refund(_ context.Context, _ Order, _ int64) (ProviderRefundResult, error) {
	return ProviderRefundResult{}, ErrProviderOperationNotSupported
}

func (hmacProvider) Cancel(_ context.Context, _ Order) error {
	return ErrProviderOperationNotSupported
}

// defaultTestProviderSecret 是 NewTestProvider() 缺省 HMAC 密钥; 仅测试/本地, 永不用于真实渠道。
const defaultTestProviderSecret = "huakai-test-provider-secret"

// testProvider 仅测试 / 本地配置启用; 任何生产配置默认关闭。secret 用于回调验签。
type testProvider struct{ secret []byte }

// NewTestProvider 返回带缺省密钥的测试 provider。
func NewTestProvider() Provider { return testProvider{secret: []byte(defaultTestProviderSecret)} }

// NewTestProviderWithSecret 返回带指定 HMAC 密钥的测试 provider (回调链路测试用)。
func NewTestProviderWithSecret(secret string) Provider { return testProvider{secret: []byte(secret)} }

func (testProvider) Kind() ProviderKind { return ProviderTest }

func (testProvider) CreateIntent(_ context.Context, order Order) (PaymentIntent, error) {
	return PaymentIntent{OrderRef: "test-ref-" + order.OutTradeNo}, nil
}

func (testProvider) QueryOrder(_ context.Context, _ Order) (ProviderOrderState, error) {
	return ProviderOrderState{}, ErrProviderOperationNotSupported
}

func (testProvider) Refund(_ context.Context, _ Order, _ int64) (ProviderRefundResult, error) {
	return ProviderRefundResult{}, ErrProviderOperationNotSupported
}

func (testProvider) Cancel(_ context.Context, _ Order) error {
	return ErrProviderOperationNotSupported
}

// 编译期断言: test provider 必须满足回调验签契约。
var _ CallbackVerifier = testProvider{}

// testCallbackEnvelope 是 test provider 回调体的签名载荷 (我们自控格式; 真渠道用各自格式留 P-RealMoney)。
type testCallbackEnvelope struct {
	TenantID        int64  `json:"tenant_id"`
	OutTradeNo      string `json:"out_trade_no"`
	PaidAmountCents int64  `json:"paid_amount_cents"`
	CurrencyCode    string `json:"currency_code,omitempty"`
	ProviderRef     string `json:"provider_ref,omitempty"`
	EventID         string `json:"event_id,omitempty"`
	Timestamp       int64  `json:"ts,omitempty"` // 签进体内防篡改; P2a 不强制时效窗 (重放靠 P1 幂等), 留 P-RealMoney 加固
}

// VerifyCallback 用密钥对 raw body 重算 HMAC-SHA256 并常量时间比较, 通过才解析归一化字段。
// 任何失败 (无密钥 / 签名不匹配 / body 非法 / 关键字段缺) 一律 ErrCallbackUnverified — 不区分原因, 不泄露给伪造者。
func (p testProvider) VerifyCallback(rawBody []byte, signature string) (CallbackResult, error) {
	if len(p.secret) == 0 {
		return CallbackResult{}, ErrCallbackUnverified
	}
	expected := computeTestSignature(p.secret, rawBody)
	provided := strings.ToLower(strings.TrimSpace(signature))
	// hmac.Equal 常量时间比较, 防签名比对的时序侧信道; 长度不等安全返回 false。
	if !hmac.Equal([]byte(provided), []byte(expected)) {
		return CallbackResult{}, ErrCallbackUnverified
	}
	var env testCallbackEnvelope
	if err := json.Unmarshal(rawBody, &env); err != nil {
		return CallbackResult{}, ErrCallbackUnverified
	}
	if env.TenantID <= 0 || strings.TrimSpace(env.OutTradeNo) == "" || env.PaidAmountCents <= 0 {
		return CallbackResult{}, ErrCallbackUnverified
	}
	return CallbackResult{
		TenantID:        env.TenantID,
		OutTradeNo:      strings.TrimSpace(env.OutTradeNo),
		PaidAmountCents: env.PaidAmountCents,
		CurrencyCode:    strings.TrimSpace(env.CurrencyCode),
		ProviderRef:     strings.TrimSpace(env.ProviderRef),
	}, nil
}

func computeTestSignature(secret, rawBody []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(rawBody)
	return hex.EncodeToString(mac.Sum(nil))
}

// SignTestCallback 用 test provider 密钥对回调体算签名 (HMAC-SHA256 hex)。
// 仅供 test provider 链路 (测试 / 本地模拟) 使用; 真实渠道用各自 SDK 验签, 不走此函数。
func SignTestCallback(secret string, rawBody []byte) string {
	return computeTestSignature([]byte(secret), rawBody)
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
