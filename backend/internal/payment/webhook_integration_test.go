// HUAKAI · iKun
//go:build integration_pg

// 支付 P2a 自动回调入账真 PG 判别测试。每个 W 守一个 webhook 攻击面缺陷, fixture 设计成 mutation 即变红:
//   W1 合法签名回调入账恰一次 (验签→复用 P1 入账, system 归属)
//   W2 伪造签名零入账 (跳过验签 → 伪造入账 = 红)
//   W3 重放幂等 (同回调两次 → 仅一次入账)
//   W4 金额篡改拒 (验签合法但金额≠单 → 零入账)
//   W5 跨租户隔离 (同 out_trade_no, 签 B → 仅 B 入账, A 不动)
//   W6 幽灵订单拒 (合法签名但订单不存在 → 零入账)

package payment

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

const webhookTestSecret = "p2a-webhook-secret-fixture"

// signedTestCallback 构造一条 test-provider 回调体 + 其合法 HMAC 签名。
func signedTestCallback(t *testing.T, env testCallbackEnvelope) ([]byte, string) {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal callback: %v", err)
	}
	return raw, SignTestCallback(webhookTestSecret, raw)
}

// W1: 合法签名回调 → 订单 completed, 余额=金额, 恰 1 credit / 1 payment_credited, paid_confirmed 归属 system。
func TestPaymentWebhook_ValidSignedCallbackCreditsOnce(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProviderSecret(webhookTestSecret))

	outNo := "p2a-w1-" + f.suffix
	const amount = int64(1500)
	created, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: f.tenantA, UserID: f.userA, AmountCents: amount, OutTradeNo: outNo, ProviderKind: ProviderTest,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	raw, sig := signedTestCallback(t, testCallbackEnvelope{
		TenantID: f.tenantA, OutTradeNo: outNo, PaidAmountCents: amount, CurrencyCode: "USD", ProviderRef: "evt-w1",
	})
	res, err := svc.ConfirmPaidByCallback(ctx, ProviderTest, raw, sig)
	if err != nil {
		t.Fatalf("confirm by callback: %v", err)
	}
	if res.Order.Status != StatusCompleted || res.BalanceCents != amount || res.Idempotent {
		t.Fatalf("result status=%q balance=%d idempotent=%v, want completed/%d/false", res.Order.Status, res.BalanceCents, res.Idempotent, amount)
	}

	if bal, _ := svc.GetBalance(ctx, f.tenantA, f.userA); bal.AmountCents != amount {
		t.Fatalf("balance = %d, want %d", bal.AmountCents, amount)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND payment_order_id=$2`, f.tenantA, created.Order.ID); n != 1 {
		t.Fatalf("credit count = %d, want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_credited'`, f.tenantA); n != 1 {
		t.Fatalf("payment_credited count = %d, want 1", n)
	}
	// 归属判别: 回调确认必须记为 system (mutation: ConfirmPaid 审计若退回硬编码 admin → 此处变红)。
	if n := f.countInt(`SELECT count(*) FROM payment_audit_events WHERE tenant_id=$1 AND payment_order_id=$2 AND event_type='paid_confirmed' AND actor_kind='system'`, f.tenantA, created.Order.ID); n != 1 {
		t.Fatalf("system paid_confirmed audit count = %d, want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_audit_events WHERE tenant_id=$1 AND payment_order_id=$2 AND event_type='credited'`, f.tenantA, created.Order.ID); n != 1 {
		t.Fatalf("credited audit count = %d, want 1", n)
	}
}

// W2: 伪造签名 → ErrCallbackUnverified, 零 credit, 订单仍 pending。
// 自证: 与 W1 同 fixture 但签名错, 结果余额必须为 0 (而非 W1 的 amount)。
func TestPaymentWebhook_ForgedSignatureRejectedNoCredit(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProviderSecret(webhookTestSecret))

	outNo := "p2a-w2-" + f.suffix
	const amount = int64(1500)
	created, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: f.tenantA, UserID: f.userA, AmountCents: amount, OutTradeNo: outNo, ProviderKind: ProviderTest,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	raw, _ := signedTestCallback(t, testCallbackEnvelope{
		TenantID: f.tenantA, OutTradeNo: outNo, PaidAmountCents: amount, CurrencyCode: "USD",
	})
	// 用错误密钥算出的签名 = 伪造 (内容合法但无有效密钥)。
	forged := SignTestCallback("attacker-guessed-secret", raw)
	_, err = svc.ConfirmPaidByCallback(ctx, ProviderTest, raw, forged)
	if !errors.Is(err, ErrCallbackUnverified) {
		t.Fatalf("err = %v, want ErrCallbackUnverified", err)
	}
	if bal, _ := svc.GetBalance(ctx, f.tenantA, f.userA); bal.AmountCents != 0 {
		t.Fatalf("balance = %d, want 0 (forged callback must not credit)", bal.AmountCents)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1`, f.tenantA); n != 0 {
		t.Fatalf("credit count = %d, want 0", n)
	}
	if o, _ := svc.GetOrder(ctx, f.tenantA, created.Order.ID); o.Status != StatusPending {
		t.Fatalf("order status = %q, want pending (forged callback must not advance order)", o.Status)
	}
}

// W3: 同一合法回调发两次 → 仅 1 credit, 余额增一次, 第二次幂等。
func TestPaymentWebhook_ReplayedCallbackIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProviderSecret(webhookTestSecret))

	outNo := "p2a-w3-" + f.suffix
	const amount = int64(2200)
	created, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: f.tenantA, UserID: f.userA, AmountCents: amount, OutTradeNo: outNo, ProviderKind: ProviderTest,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	raw, sig := signedTestCallback(t, testCallbackEnvelope{
		TenantID: f.tenantA, OutTradeNo: outNo, PaidAmountCents: amount, CurrencyCode: "USD",
	})

	res1, err := svc.ConfirmPaidByCallback(ctx, ProviderTest, raw, sig)
	if err != nil || res1.Idempotent {
		t.Fatalf("first callback: err=%v idempotent=%v", err, res1.Idempotent)
	}
	res2, err := svc.ConfirmPaidByCallback(ctx, ProviderTest, raw, sig)
	if err != nil {
		t.Fatalf("replay callback: %v", err)
	}
	// 自证: 重放后的余额必须等于单次金额 (而非翻倍)。
	if !res2.Idempotent || res2.BalanceCents != amount {
		t.Fatalf("replay doubled credit: idempotent=%v balance=%d, want true/%d", res2.Idempotent, res2.BalanceCents, amount)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND payment_order_id=$2`, f.tenantA, created.Order.ID); n != 1 {
		t.Fatalf("credit count = %d, want 1 (replay must not double-credit)", n)
	}
}

// W4: 验签合法但金额≠本地单 → ErrCallbackRejected, 零 credit, 订单仍 pending。
func TestPaymentWebhook_AmountMismatchRejected(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProviderSecret(webhookTestSecret))

	outNo := "p2a-w4-" + f.suffix
	const orderAmount = int64(5000)
	created, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: f.tenantA, UserID: f.userA, AmountCents: orderAmount, OutTradeNo: outNo, ProviderKind: ProviderTest,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	// 回调声称只付了 4900 (篡改); 签名对篡改后的体仍合法 → 必须靠金额一致性拦截。
	raw, sig := signedTestCallback(t, testCallbackEnvelope{
		TenantID: f.tenantA, OutTradeNo: outNo, PaidAmountCents: 4900, CurrencyCode: "USD",
	})
	_, err = svc.ConfirmPaidByCallback(ctx, ProviderTest, raw, sig)
	if !errors.Is(err, ErrCallbackRejected) {
		t.Fatalf("err = %v, want ErrCallbackRejected", err)
	}
	if bal, _ := svc.GetBalance(ctx, f.tenantA, f.userA); bal.AmountCents != 0 {
		t.Fatalf("balance = %d, want 0 (amount-mismatch callback must not credit)", bal.AmountCents)
	}
	if o, _ := svc.GetOrder(ctx, f.tenantA, created.Order.ID); o.Status != StatusPending {
		t.Fatalf("order status = %q, want pending", o.Status)
	}
}

// W5: tenant A/B 同 out_trade_no 各建单, 回调签给 B → 仅 B 入账, A 不动。
func TestPaymentWebhook_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProviderSecret(webhookTestSecret))

	outNo := "p2a-w5-shared-" + f.suffix
	const amount = int64(3300)
	if _, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: f.tenantA, UserID: f.userA, AmountCents: amount, OutTradeNo: outNo, ProviderKind: ProviderTest}); err != nil {
		t.Fatalf("create order A: %v", err)
	}
	if _, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: f.tenantB, UserID: f.userB, AmountCents: amount, OutTradeNo: outNo, ProviderKind: ProviderTest}); err != nil {
		t.Fatalf("create order B: %v", err)
	}

	// 回调 tenant 字段指向 B (已验签的可信 tenant)。
	raw, sig := signedTestCallback(t, testCallbackEnvelope{
		TenantID: f.tenantB, OutTradeNo: outNo, PaidAmountCents: amount, CurrencyCode: "USD",
	})
	res, err := svc.ConfirmPaidByCallback(ctx, ProviderTest, raw, sig)
	if err != nil {
		t.Fatalf("confirm B callback: %v", err)
	}
	if res.Order.TenantID != f.tenantB || res.Order.Status != StatusCompleted {
		t.Fatalf("callback resolved tenant=%d status=%q, want tenant B completed", res.Order.TenantID, res.Order.Status)
	}
	// 自证: B 增, A 必须为 0 (mutation: lookup 漏 tenant 谓词 → A 被误入账 → 红)。
	if balB, _ := svc.GetBalance(ctx, f.tenantB, f.userB); balB.AmountCents != amount {
		t.Fatalf("tenant B balance = %d, want %d", balB.AmountCents, amount)
	}
	if balA, _ := svc.GetBalance(ctx, f.tenantA, f.userA); balA.AmountCents != 0 {
		t.Fatalf("tenant A balance = %d, want 0 (B's callback must not credit A)", balA.AmountCents)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2 AND status='pending'`, f.tenantA, outNo); n != 1 {
		t.Fatalf("tenant A pending order count = %d, want 1 (A must stay pending)", n)
	}
}

// W6: 合法签名但 out_trade_no 不存在 → ErrOrderNotFound, 零 credit。
func TestPaymentWebhook_UnknownOrderRejected(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProviderSecret(webhookTestSecret))

	raw, sig := signedTestCallback(t, testCallbackEnvelope{
		TenantID: f.tenantA, OutTradeNo: "p2a-w6-ghost-" + f.suffix, PaidAmountCents: 1000, CurrencyCode: "USD",
	})
	_, err := svc.ConfirmPaidByCallback(ctx, ProviderTest, raw, sig)
	if !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("err = %v, want ErrOrderNotFound", err)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1`, f.tenantA); n != 0 {
		t.Fatalf("credit count = %d, want 0 (ghost order must not credit)", n)
	}
}
