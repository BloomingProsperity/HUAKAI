package payment

import (
	"context"
	"testing"
)

// 守: 淘宝/闲鱼 provider 的 CreateIntent 返回运营配置的 checkout 链接 + 订单号 + manual 确认模式
// (供前端渲染二维码/跳转, 用户备注订单号, 管理员手动确认)。
// Mutation: CreateIntent 返回空 Snapshot -> checkout_url 缺失 -> 红。
func TestTaobaoProviderCreateIntentReturnsCheckoutRedirect(t *testing.T) {
	const url = "https://item.taobao.com/item.htm?id=123456"
	p := NewTaobaoProvider(url)
	if p.Kind() != ProviderTaobao {
		t.Fatalf("kind=%s want taobao", p.Kind())
	}
	intent, err := p.CreateIntent(context.Background(), Order{OutTradeNo: "OT-789", AmountCents: 1000, CurrencyCode: "CNY"})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	if intent.OrderRef != "OT-789" {
		t.Fatalf("OrderRef=%q want OT-789", intent.OrderRef)
	}
	if got := intent.Snapshot["checkout_url"]; got != url {
		t.Fatalf("checkout_url=%v want %q", got, url)
	}
	if got := intent.Snapshot["out_trade_no"]; got != "OT-789" {
		t.Fatalf("out_trade_no=%v want OT-789", got)
	}
	if got := intent.Snapshot["confirm_mode"]; got != "manual" {
		t.Fatalf("confirm_mode=%v want manual", got)
	}
}

// 守: 淘宝/闲鱼无程序回调 → provider 绝不实现 CallbackVerifier, 只能管理员手动确认。
// Mutation: 给 taobaoProvider 加 VerifyCallback -> 该断言红(防误把 manual 渠道当自动入账)。
func TestTaobaoProviderHasNoCallbackVerifier(t *testing.T) {
	var p Provider = NewTaobaoProvider("https://x")
	if _, ok := p.(CallbackVerifier); ok {
		t.Fatal("taobao provider must NOT implement CallbackVerifier (manual confirm only, no programmatic webhook)")
	}
}

// 守: WithTaobaoProvider Option 把 taobao 注册进 service provider registry(配置开关启用时)。
// Mutation: WithTaobaoProvider 改 no-op -> resolve(ProviderTaobao) 报错 -> 红。
func TestWithTaobaoProviderRegistersTaobao(t *testing.T) {
	s := NewService(nil, WithTaobaoProvider("https://item.taobao.com/x"))
	p, err := s.providers.resolve(ProviderTaobao)
	if err != nil {
		t.Fatalf("taobao not registered: %v", err)
	}
	if p.Kind() != ProviderTaobao {
		t.Fatalf("resolved kind=%s want taobao", p.Kind())
	}
}
