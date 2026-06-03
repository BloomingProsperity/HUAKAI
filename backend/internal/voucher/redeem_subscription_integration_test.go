// HUAKAI · iKun
//go:build integration_pg

package voucher

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

func openSubPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 20})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// subVoucherFixture 订阅券兑换集成夹具: 一租户一用户 + 经 Service 兑换 (顺带覆盖 audit 分支)。
type subVoucherFixture struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	svc      *Service
	audit    *MemoryAuditSink
	suffix   string
	tenantID int64
	userID   int64
}

func newSubVoucherFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *subVoucherFixture {
	t.Helper()
	audit := NewMemoryAuditSink()
	f := &subVoucherFixture{
		t: t, ctx: ctx, pool: pool, suffix: uuid.NewString(),
		audit: audit, svc: NewService(NewPostgresStore(pool), WithAuditSink(audit)),
	}
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "vsub-"+f.suffix).Scan(&f.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1,$2) RETURNING id`, f.tenantID, "u-"+f.suffix).Scan(&f.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(f.cleanup)
	return f
}

func (f *subVoucherFixture) cleanup() {
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM subscription_fulfillment_effects WHERE tenant_id=$1`,
		`DELETE FROM billing_events WHERE tenant_id=$1`,
		`DELETE FROM voucher_redemption WHERE tenant_id=$1`,
		`DELETE FROM voucher WHERE tenant_id=$1`,
		`DELETE FROM subscription_audit_events WHERE tenant_id=$1`,
		`DELETE FROM subscription_policy_links WHERE tenant_id=$1`,
		`DELETE FROM user_subscriptions WHERE tenant_id=$1`,
		`DELETE FROM subscription_plans WHERE tenant_id=$1`,
		`DELETE FROM quota_policies WHERE tenant_id=$1`,
		`DELETE FROM users WHERE tenant_id=$1`,
		`DELETE FROM tenants WHERE id=$1`,
	} {
		_, _ = f.pool.Exec(ctx, q, f.tenantID)
	}
}

func (f *subVoucherFixture) seedPlan(name, group string, validityDays int, daily *decimal.Decimal) subscription.Plan {
	f.t.Helper()
	p, err := subscription.NewService(subscription.NewPostgresStore(f.pool)).CreatePlan(f.ctx, subscription.CreatePlanInput{
		TenantID: f.tenantID, Name: name, CurrencyCode: "USD", ValidityDays: validityDays,
		GrantedGroup: group, DailyCapUSD: daily, ForSale: true,
	})
	if err != nil {
		f.t.Fatalf("seed plan %s: %v", name, err)
	}
	return p
}

// seedSubscriptionVoucher 直插一张订阅券 (grant_kind=subscription); admin 创建侧 API 是路标 (计划 §1)。
func (f *subVoucherFixture) seedSubscriptionVoucher(planID int64, code string, maxRedemptions int, now time.Time) int64 {
	f.t.Helper()
	hash, fp := CodeHash(f.tenantID, NormalizeCode(code))
	now = now.UTC()
	var id int64
	if err := f.pool.QueryRow(f.ctx, `
INSERT INTO voucher (tenant_id, code_hash, code_fingerprint, amount_cents, currency_code,
	valid_from, valid_until, max_redemptions, single_use_per_user, status, grant_kind, subscription_plan_id)
VALUES ($1,$2,$3,$4,'USD',$5,$6,$7,true,'active','subscription',$8) RETURNING id`,
		f.tenantID, hash, fp, 999, now.AddDate(0, 0, -1), now.AddDate(0, 0, 30), maxRedemptions, planID).Scan(&id); err != nil {
		f.t.Fatalf("seed subscription voucher: %v", err)
	}
	return id
}

// seedBalanceVoucher 直插一张余额券 (grant_kind 默认 'balance')。直插而非走 CreateVoucher:
// 隔离 pre-existing CreateVoucher-on-PG 的 $8 类型推断缺陷 (见 deferred), 聚焦 Redeem 余额路径本身。
func (f *subVoucherFixture) seedBalanceVoucher(code string, amountCents int64, now time.Time) int64 {
	f.t.Helper()
	hash, fp := CodeHash(f.tenantID, NormalizeCode(code))
	now = now.UTC()
	var id int64
	if err := f.pool.QueryRow(f.ctx, `
INSERT INTO voucher (tenant_id, code_hash, code_fingerprint, amount_cents, currency_code,
	valid_from, valid_until, max_redemptions, single_use_per_user, status)
VALUES ($1,$2,$3,$4,'USD',$5,$6,1,true,'active') RETURNING id`,
		f.tenantID, hash, fp, amountCents, now.AddDate(0, 0, -1), now.AddDate(0, 0, 30)).Scan(&id); err != nil {
		f.t.Fatalf("seed balance voucher: %v", err)
	}
	return id
}

func (f *subVoucherFixture) redeem(code, idemKey string, now time.Time) (RedeemResult, error) {
	return f.svc.Redeem(f.ctx, RedeemInput{
		TenantID: f.tenantID, UserID: f.userID, Code: code,
		IdempotencyKey: idemKey, RequestID: "req-" + idemKey, Now: now,
	})
}

func (f *subVoucherFixture) countInt(query string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.pool.QueryRow(f.ctx, query, args...).Scan(&n); err != nil {
		f.t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func capDec(s string) *decimal.Decimal {
	d := decimal.RequireFromString(s)
	return &d
}

// TestPG_RedeemSubscriptionVoucher_ActivatesZeroTouch 订阅券兑换 → 订阅激活 + 装日 cap 策略 + effect(voucher 源),
// 且零碰钱表 (billing_events 不增 / payment_credits=0 / voucher 余额视图=0)。
// 判别: Redeem 漏 grant_kind 分支走余额路径 → 写 voucher_redeemed → billing_events 增量 → 变红 (头号偷钱反向风险)。
func TestPG_RedeemSubscriptionVoucher_ActivatesZeroTouch(t *testing.T) {
	f := newSubVoucherFixture(t, t.Context(), openSubPool(t, t.Context()))
	plan := f.seedPlan("Pro", "premium", 30, capDec("10"))
	code := "SUBV-" + f.suffix
	now := subscriptionVoucherIntegrationNow()
	f.seedSubscriptionVoucher(plan.ID, code, 1, now)

	beBefore := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1`, f.tenantID)
	res, err := f.redeem(code, "idem-1", now)
	if err != nil {
		t.Fatalf("redeem subscription voucher: %v", err)
	}
	if res.Subscription == nil {
		t.Fatalf("expected subscription grant, got nil (balance path taken?)")
	}
	if res.Subscription.ResultKind != subscription.ResultCreated {
		t.Fatalf("result kind = %q, want created", res.Subscription.ResultKind)
	}
	if n := f.countInt(`SELECT count(*) FROM user_subscriptions WHERE tenant_id=$1 AND user_id=$2 AND status='active'`, f.tenantID, f.userID); n != 1 {
		t.Fatalf("active subs = %d, want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM quota_policies WHERE tenant_id=$1 AND metric='cost_usd' AND window_kind='calendar_day' AND enabled=true AND limit_value=$2`, f.tenantID, "10"); n != 1 {
		t.Fatalf("daily cap=10 policies = %d, want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_fulfillment_effects WHERE tenant_id=$1 AND source_kind='voucher' AND voucher_redemption_id=$2`, f.tenantID, res.Redemption.ID); n != 1 {
		t.Fatalf("voucher effect rows = %d, want 1", n)
	}
	// 红线: 订阅券零碰钱表。
	if got := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1`, f.tenantID); got != beBefore {
		t.Fatalf("billing_events changed %d -> %d (subscription voucher must not write money ledger)", beBefore, got)
	}
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1`, f.tenantID); got != 0 {
		t.Fatalf("payment_credits = %d, want 0", got)
	}
	if res.BalanceCents != 0 {
		t.Fatalf("voucher balance = %d, want 0 (subscription redemption excluded from balance view)", res.BalanceCents)
	}
	if n := f.countInt(`SELECT redeemed_count FROM voucher WHERE tenant_id=$1 AND grant_kind='subscription'`, f.tenantID); n != 1 {
		t.Fatalf("redeemed_count = %d, want 1", n)
	}
	// audit 分支: 记订阅维度, 不泄 balance/billing。
	var found bool
	for _, e := range f.audit.Events() {
		if e.EventType == AuditVoucherRedeemed {
			found = true
			if e.Payload["grant_kind"] != GrantKindSubscription {
				t.Fatalf("audit grant_kind = %v, want subscription", e.Payload["grant_kind"])
			}
			// 正向断言订阅维度键在场 (防 privacy allowlist 将来改成逐键丢弃时静默漏记)。
			if _, ok := e.Payload["subscription_plan_id"]; !ok {
				t.Fatalf("subscription audit must carry subscription_plan_id")
			}
			if _, ok := e.Payload["new_expires_at"]; !ok {
				t.Fatalf("subscription audit must carry new_expires_at")
			}
			if _, ok := e.Payload["balance_cents"]; ok {
				t.Fatalf("subscription audit must not carry balance_cents")
			}
			if _, ok := e.Payload["billing_event_id"]; ok {
				t.Fatalf("subscription audit must not carry billing_event_id")
			}
		}
	}
	if !found {
		t.Fatalf("no voucher_redeemed audit emitted")
	}
}

// TestPG_RedeemSubscriptionVoucher_IdempotentReplay 同幂等键二次兑换 → 订阅只延一次 + effect 仅一行 + 券只消耗一次。
// 判别: 去 effect 幂等 (唯一索引/预读) → 双激活 → 到期日二次跳 / redeemed_count=2 → 变红。
func TestPG_RedeemSubscriptionVoucher_IdempotentReplay(t *testing.T) {
	f := newSubVoucherFixture(t, t.Context(), openSubPool(t, t.Context()))
	plan := f.seedPlan("Pro", "premium", 30, capDec("10"))
	code := "SUBV-" + f.suffix
	now := subscriptionVoucherIntegrationNow()
	f.seedSubscriptionVoucher(plan.ID, code, 5, now) // max>1, 幂等键才是去重闸

	r1, err := f.redeem(code, "idem-rep", now)
	if err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	exp1 := r1.Subscription.NewExpiresAt

	r2, err := f.redeem(code, "idem-rep", now.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("replay redeem: %v", err)
	}
	if !r2.Idempotent {
		t.Fatalf("replay should be idempotent")
	}
	if r2.Subscription == nil || !r2.Subscription.NewExpiresAt.Equal(exp1) {
		t.Fatalf("replay expires = %+v, want unchanged %v", r2.Subscription, exp1)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_fulfillment_effects WHERE tenant_id=$1`, f.tenantID); n != 1 {
		t.Fatalf("effect rows = %d, want 1 (replay must not double-fulfill)", n)
	}
	if n := f.countInt(`SELECT count(*) FROM voucher_redemption WHERE tenant_id=$1`, f.tenantID); n != 1 {
		t.Fatalf("redemptions = %d, want 1", n)
	}
	if n := f.countInt(`SELECT redeemed_count FROM voucher WHERE tenant_id=$1`, f.tenantID); n != 1 {
		t.Fatalf("redeemed_count = %d, want 1", n)
	}
}

// TestPG_RedeemSubscriptionVoucher_DowngradeBlocked 已持高档活跃 → 自助兑换低档券被拒 + 整事务回滚 (券未消耗)。
// 判别: FulfillVoucherTx 传 EnforceUpgradeOnly=false → 降档生效 → 低档 cap 出现 / 券被消耗 → 变红。
func TestPG_RedeemSubscriptionVoucher_DowngradeBlocked(t *testing.T) {
	f := newSubVoucherFixture(t, t.Context(), openSubPool(t, t.Context()))
	high := f.seedPlan("High", "premium", 30, capDec("100"))
	low := f.seedPlan("Low", "premium", 30, capDec("10"))
	now := subscriptionVoucherIntegrationNow()

	highCode := "HIGH-" + f.suffix
	f.seedSubscriptionVoucher(high.ID, highCode, 1, now)
	if _, err := f.redeem(highCode, "idem-high", now); err != nil {
		t.Fatalf("redeem high: %v", err)
	}

	lowCode := "LOW-" + f.suffix
	lowVID := f.seedSubscriptionVoucher(low.ID, lowCode, 1, now)
	_, err := f.redeem(lowCode, "idem-low", now.AddDate(0, 0, 1))
	if !errors.Is(err, subscription.ErrDowngradeNotAllowed) {
		t.Fatalf("downgrade err = %v, want ErrDowngradeNotAllowed", err)
	}
	// 整事务回滚: 低档券未消耗。
	if n := f.countInt(`SELECT redeemed_count FROM voucher WHERE tenant_id=$1 AND id=$2`, f.tenantID, lowVID); n != 0 {
		t.Fatalf("low voucher redeemed_count = %d, want 0 (rejected redeem must not consume)", n)
	}
	// 低档无生效, effect 仍只有高档那条。
	if n := f.countInt(`SELECT count(*) FROM quota_policies WHERE tenant_id=$1 AND window_kind='calendar_day' AND enabled=true AND limit_value=$2`, f.tenantID, "10"); n != 0 {
		t.Fatalf("low cap policies = %d, want 0 (downgrade rejected)", n)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_fulfillment_effects WHERE tenant_id=$1`, f.tenantID); n != 1 {
		t.Fatalf("effect rows = %d, want 1 (rejected redeem wrote none)", n)
	}
}

// TestPG_RedeemBalanceVoucher_Regression 余额券路径零回归: 仍写 billing_events 'voucher_redeemed' + 余额增 + 无订阅授予。
// 判别: 若 grant_kind 分支误把余额券导向订阅 → 无 billing_events / Subscription!=nil → 变红。
func TestPG_RedeemBalanceVoucher_Regression(t *testing.T) {
	f := newSubVoucherFixture(t, t.Context(), openSubPool(t, t.Context()))
	code := "BAL-" + f.suffix
	now := subscriptionVoucherIntegrationNow()
	f.seedBalanceVoucher(code, 500, now)
	res, err := f.redeem(code, "idem-bal", now)
	if err != nil {
		t.Fatalf("redeem balance voucher: %v", err)
	}
	if res.Subscription != nil {
		t.Fatalf("balance voucher must not produce subscription grant")
	}
	if res.BalanceCents != 500 {
		t.Fatalf("voucher balance = %d, want 500", res.BalanceCents)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='voucher_redeemed'`, f.tenantID); n != 1 {
		t.Fatalf("voucher_redeemed billing events = %d, want 1 (balance path must still credit)", n)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_fulfillment_effects WHERE tenant_id=$1`, f.tenantID); n != 0 {
		t.Fatalf("effect rows = %d, want 0 (balance voucher writes no subscription effect)", n)
	}
}

func subscriptionVoucherIntegrationNow() time.Time {
	return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
}
