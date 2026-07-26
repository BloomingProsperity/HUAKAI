// HUAKAI · iKun
//go:build integration_pg

// 订阅自动续费 worker 的真 PG 判别测试。每个测试守一个具体 money 缺陷, fixture 设计成
// mutation 即变红:
//   A1 余额够 → 精确扣款 + expires_at 延长 + 写一条扣款账本行 (原子)
//   A2 余额不足 → 绝不扣款 + 不续期 (余额/到期日都不变)
//   A3 幂等 → 同周期跑两次只扣一次只续一次 (worker 重跑安全)
//   A4 auto_renew=false → 不进 due, 不续
//   A5 免费套餐 (price<=0) → 不扣款仍续期
//   A6 并发多 worker 同订阅 → 只扣一次只续一次 (Serializable + FOR UPDATE + 唯一锚)

package subscription

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// seedBalance 预置用户钱包余额 (user_balances 行); amount 为 USD 字符串。
func (f *subFixture) seedBalance(tenantID, userID int64, amount string) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO user_balances (tenant_id, user_id, balance, held, version)
VALUES ($1, $2, $3::numeric, 0, 0)
ON CONFLICT (tenant_id, user_id) DO UPDATE SET balance=$3::numeric`,
		tenantID, userID, amount); err != nil {
		f.t.Fatalf("seed balance: %v", err)
	}
}

// balanceOf 读用户当前钱包余额。
func (f *subFixture) balanceOf(tenantID, userID int64) decimal.Decimal {
	f.t.Helper()
	var raw string
	if err := f.pool.QueryRow(f.ctx, `SELECT balance::text FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID).Scan(&raw); err != nil {
		f.t.Fatalf("read balance: %v", err)
	}
	return decimal.RequireFromString(raw)
}

// createPaidPlan 建一个授予 premium + price_cents 的套餐 (续费要扣这个价)。
func createPaidPlan(t *testing.T, ctx context.Context, svc *Service, tenantID int64, priceCents int64) Plan {
	t.Helper()
	plan, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: tenantID, Name: "paid", ValidityDays: 30,
		GrantedGroup: "premium", PriceCents: priceCents, MonthlyCapUSD: dec("10"),
	})
	if err != nil {
		t.Fatalf("create paid plan: %v", err)
	}
	return plan
}

// A1: 余额够 → 精确扣款 + 续期 + 一条账本行。
// mutation: store_postgres_auto_renewal 把 ActivateOrRenewTx 之后的 insert 提到扣款之前并拆事务
// (扣款后 return) → 钱扣了没续 (expires_at 不变) → 续期断言红。
func TestPG_AutoRenew_DebitsAndRenews(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPaidPlan(t, ctx, svc, f.tenantA, 500) // $5.00
	f.seedBalance(f.tenantA, f.userA, "20")             // $20 余额

	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	prevExpires := assigned.Subscription.ExpiresAt

	// 推进到到期后, 跑续费。订阅已逻辑过期 → 续期基点 = max(now, 旧到期日) = now。
	renewAt := prevExpires.Add(time.Hour)
	clk.set(renewAt)
	res, err := svc.ProcessAutoRenewal(ctx, 10)
	if err != nil {
		t.Fatalf("process auto renewal: %v", err)
	}
	if res.Renewed != 1 || res.Skipped != 0 {
		t.Fatalf("batch result renewed=%d skipped=%d, want 1/0", res.Renewed, res.Skipped)
	}

	// 扣款精确: $20 - $5 = $15。
	if got := f.balanceOf(f.tenantA, f.userA); !got.Equal(decimal.RequireFromString("15")) {
		t.Fatalf("balance after renew = %s, want 15 (扣款不精确)", got.String())
	}
	// 续期: 已过期 → 从 now 起算新 30 天窗口 (而非旧到期日)。
	got := f.userSubExpires(f.tenantA, f.userA)
	wantExpires := renewAt.AddDate(0, 0, 30)
	if !got.Equal(wantExpires) {
		t.Fatalf("expires after renew = %v, want %v (未续期或续期基点错)", got, wantExpires)
	}
	if !got.After(prevExpires) {
		t.Fatalf("expires %v 未超过续期前 %v —— 没真正续期", got, prevExpires)
	}
	// 恰好一条扣款账本行。
	if n := f.countInt(`SELECT count(*) FROM subscription_auto_renewal_charges WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); n != 1 {
		t.Fatalf("auto renewal charge rows = %d, want 1", n)
	}
	if n := f.countInt(`SELECT amount_cents FROM subscription_auto_renewal_charges WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); n != 500 {
		t.Fatalf("charge amount_cents = %d, want 500", n)
	}
}

// A2: 余额不足 → 绝不扣款 + 不续期。
// mutation: debitUserBalanceTx 去掉 `AND balance - held >= $3` 守卫 → 余额被扣成负 / 续期发生 → 红。
func TestPG_AutoRenew_InsufficientFundSkips(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPaidPlan(t, ctx, svc, f.tenantA, 500) // $5.00
	f.seedBalance(f.tenantA, f.userA, "3")              // 只 $3, 不够 $5

	assigned, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID})
	prevExpires := assigned.Subscription.ExpiresAt

	clk.set(prevExpires.Add(time.Hour))
	res, err := svc.ProcessAutoRenewal(ctx, 10)
	if err != nil {
		t.Fatalf("process auto renewal: %v", err)
	}
	if res.Renewed != 0 || res.Skipped != 1 {
		t.Fatalf("batch renewed=%d skipped=%d, want 0/1 (余额不足必须跳过)", res.Renewed, res.Skipped)
	}
	// 余额一分未扣。
	if got := f.balanceOf(f.tenantA, f.userA); !got.Equal(decimal.RequireFromString("3")) {
		t.Fatalf("balance after insufficient = %s, want 3 unchanged (余额不足却扣了款)", got.String())
	}
	// 到期日不变 (订阅仍是旧到期日, 未续期; 注意此刻订阅已逻辑过期但 worker 未到期它, 仍 active)。
	got := f.userSubExpires(f.tenantA, f.userA)
	if !got.Equal(prevExpires) {
		t.Fatalf("expires after insufficient = %v, want %v unchanged (余额不足却续了期)", got, prevExpires)
	}
	// 无扣款账本行。
	if n := f.countInt(`SELECT count(*) FROM subscription_auto_renewal_charges WHERE tenant_id=$1`, f.tenantA); n != 0 {
		t.Fatalf("charge rows = %d, want 0 (余额不足不应记账)", n)
	}
}

// A2-2: 第一页余额不足时仍须继续翻页，不能让后面的可续费订阅永久饿死。
// 两条订阅到期时间相同且 A 先创建，批量设为 1，确保第一页只有 A、第二页只有 B。
// mutation: worker 在 Renewed==0 时直接返回，或查询不带 (expires_at,id) 游标，都会让 B 不续费。
func TestPG_AutoRenewWorker_SkippedFirstPageDoesNotStarveNextPage(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))

	planA := createPaidPlan(t, ctx, svc, f.tenantA, 500)
	planB := createPaidPlan(t, ctx, svc, f.tenantB, 500)
	f.seedBalance(f.tenantA, f.userA, "3")
	f.seedBalance(f.tenantB, f.userB, "20")

	first, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: planA.ID})
	if err != nil {
		t.Fatalf("assign first: %v", err)
	}
	second, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantB, UserID: f.userB, PlanID: planB.ID})
	if err != nil {
		t.Fatalf("assign second: %v", err)
	}
	if !first.Subscription.ExpiresAt.Equal(second.Subscription.ExpiresAt) || first.Subscription.ID >= second.Subscription.ID {
		t.Fatalf("fixture 顺序不成立: first=(%v,%d) second=(%v,%d)",
			first.Subscription.ExpiresAt, first.Subscription.ID, second.Subscription.ExpiresAt, second.Subscription.ID)
	}

	clk.set(first.Subscription.ExpiresAt.Add(time.Hour))
	w := NewAutoRenewWorker(AutoRenewWorkerConfig{Service: svc, BatchSize: 1})
	w.TickOnce(ctx)

	if got := w.SkippedTotal(); got != 1 {
		t.Fatalf("skipped total=%d, want 1", got)
	}
	if got := w.RenewedTotal(); got != 1 {
		t.Fatalf("renewed total=%d, want 1 (第一页跳过项挡住了后续可续费订阅)", got)
	}
	if got := f.balanceOf(f.tenantA, f.userA); !got.Equal(decimal.RequireFromString("3")) {
		t.Fatalf("first balance=%s, want 3 unchanged", got.String())
	}
	if got := f.balanceOf(f.tenantB, f.userB); !got.Equal(decimal.RequireFromString("15")) {
		t.Fatalf("second balance=%s, want 15", got.String())
	}
	if got := f.userSubExpires(f.tenantA, f.userA); !got.Equal(first.Subscription.ExpiresAt) {
		t.Fatalf("first expires=%v, want %v unchanged", got, first.Subscription.ExpiresAt)
	}
	if got := f.userSubExpires(f.tenantB, f.userB); !got.Equal(clk.now().AddDate(0, 0, 30)) {
		t.Fatalf("second expires=%v, want %v", got, clk.now().AddDate(0, 0, 30))
	}
}

// A3: 幂等 → 同周期跑两次只扣一次只续一次。
// mutation: tryAutoRenewOnce 去掉 autoRenewalChargeExistsTx 预查 + 唯一索引 → 第二次又扣一次 → 红。
func TestPG_AutoRenew_IdempotentNoDoubleCharge(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPaidPlan(t, ctx, svc, f.tenantA, 500)
	f.seedBalance(f.tenantA, f.userA, "20")

	assigned, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID})
	prevExpires := assigned.Subscription.ExpiresAt

	// 冻结时钟在同一到期时刻: 两次续费"周期键"相同 (= 续费前 expires_at), 触发幂等。
	clk.set(prevExpires.Add(time.Hour))

	// 第一次续费成功。
	r1, err := svc.store.TryAutoRenewSubscription(ctx, autoRenewRecord{TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, Now: clk.now()})
	if err != nil || !r1.Renewed {
		t.Fatalf("first renew: renewed=%v err=%v", r1.Renewed, err)
	}
	// 第二次:用与第一次相同的 periodKey (= 续费前 expires_at, 即 prevExpires) 直接打幂等锚。
	// 模拟 worker 重跑同一周期: 直接对同 (订阅) 再调一次, 此时订阅 expires_at 已被推后,
	// 不再 due → 跳过 not_due。为真正测幂等锚, 直接验证账本只 1 条 + 余额只扣一次。
	r2, err := svc.store.TryAutoRenewSubscription(ctx, autoRenewRecord{TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, Now: clk.now()})
	if err != nil {
		t.Fatalf("second renew: %v", err)
	}
	if r2.Renewed {
		t.Fatal("第二次续费又 Renewed=true —— 同一时刻订阅已续期且不再 due, 不应再扣再续")
	}

	// 余额只扣一次: $20 - $5 = $15。
	if got := f.balanceOf(f.tenantA, f.userA); !got.Equal(decimal.RequireFromString("15")) {
		t.Fatalf("balance after double run = %s, want 15 (双扣!)", got.String())
	}
	// 账本只一条。
	if n := f.countInt(`SELECT count(*) FROM subscription_auto_renewal_charges WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); n != 1 {
		t.Fatalf("charge rows after double run = %d, want 1 (重复记账)", n)
	}
}

// A3b: 幂等锚硬防线 —— 即使绕过 due 重查, 同 (订阅, 周期) 的扣款行唯一约束也挡住第二次。
// 直接连续两次以"同一 periodKey"语义调 (通过把 expires_at 还原回原到期日模拟 worker 抢跑)。
// mutation: 去掉唯一索引 uq_sub_auto_renewal_period → 第二条插入成功 → count==2 → 红。
func TestPG_AutoRenew_ChargeUniqueAnchorBlocksReplay(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPaidPlan(t, ctx, svc, f.tenantA, 500)
	f.seedBalance(f.tenantA, f.userA, "20")

	assigned, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID})
	prevExpires := assigned.Subscription.ExpiresAt
	clk.set(prevExpires.Add(time.Hour))

	if r, err := svc.store.TryAutoRenewSubscription(ctx, autoRenewRecord{TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, Now: clk.now()}); err != nil || !r.Renewed {
		t.Fatalf("first renew: renewed=%v err=%v", r.Renewed, err)
	}
	// 把订阅到期日还原回原值 + 重置余额, 模拟"worker 在同一周期抢跑 (expires_at 看起来仍是 prevExpires)"。
	if _, err := f.pool.Exec(ctx, `UPDATE user_subscriptions SET expires_at=$3 WHERE tenant_id=$1 AND id=$2`,
		f.tenantA, assigned.Subscription.ID, prevExpires); err != nil {
		t.Fatalf("rewind expires: %v", err)
	}
	f.seedBalance(f.tenantA, f.userA, "20")

	// 再跑: periodKey 仍 = prevExpires → 幂等锚命中 (预查或唯一索引) → 跳过, 不再扣。
	r2, err := svc.store.TryAutoRenewSubscription(ctx, autoRenewRecord{TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, Now: clk.now()})
	if err != nil {
		t.Fatalf("replay renew: %v", err)
	}
	if r2.Renewed {
		t.Fatal("同 (订阅, 周期) 重放又续期了 —— 幂等锚未挡住, 会双扣")
	}
	if r2.SkipReason != AutoRenewSkipAlreadyRenewed {
		t.Fatalf("replay skip reason = %q, want already_renewed", r2.SkipReason)
	}
	// 账本仍只一条 (唯一索引硬防线)。
	if n := f.countInt(`SELECT count(*) FROM subscription_auto_renewal_charges WHERE tenant_id=$1 AND user_subscription_id=$2`, f.tenantA, assigned.Subscription.ID); n != 1 {
		t.Fatalf("charge rows after replay = %d, want 1", n)
	}
	// 余额未被第二次扣 (仍 $20, 第二次重置后没动)。
	if got := f.balanceOf(f.tenantA, f.userA); !got.Equal(decimal.RequireFromString("20")) {
		t.Fatalf("balance after replay = %s, want 20 unchanged (重放双扣)", got.String())
	}
}

// A4: auto_renew=false → 不进 due, 不续。
// mutation: ListAutoRenewDue 去掉 `auto_renew=true` 过滤 → 关了续订的订阅被续 → 红。
func TestPG_AutoRenew_AutoRenewOffNotRenewed(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPaidPlan(t, ctx, svc, f.tenantA, 500)
	f.seedBalance(f.tenantA, f.userA, "20")

	assigned, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID})
	prevExpires := assigned.Subscription.ExpiresAt
	if _, err := svc.SetAutoRenew(ctx, f.tenantA, f.userA, false); err != nil {
		t.Fatalf("set auto_renew off: %v", err)
	}

	clk.set(prevExpires.Add(time.Hour))
	res, err := svc.ProcessAutoRenewal(ctx, 10)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Scanned != 0 || res.Renewed != 0 {
		t.Fatalf("scanned=%d renewed=%d, want 0/0 (auto_renew=off 不应进 due)", res.Scanned, res.Renewed)
	}
	if got := f.balanceOf(f.tenantA, f.userA); !got.Equal(decimal.RequireFromString("20")) {
		t.Fatalf("balance = %s, want 20 (auto_renew=off 却扣了款)", got.String())
	}
}

// 停用租户不得再进入续费扫描；即使调用方拿着停用前的订阅 ID 直接重放，
// 扣款事务内的租户锁也必须让它零副作用跳过。
// mutation: 去掉列表的 tenants join 或事务内活跃租户锁，扫描数、余额或扣款行断言会变红。
func TestPG_AutoRenew_DisabledTenantCannotBeCharged(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPaidPlan(t, ctx, svc, f.tenantA, 500)
	f.seedBalance(f.tenantA, f.userA, "20")

	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
		TenantID: f.tenantA,
		UserID:   f.userA,
		PlanID:   plan.ID,
	})
	if err != nil {
		t.Fatalf("assign subscription: %v", err)
	}
	originalExpiry := assigned.Subscription.ExpiresAt
	clk.set(originalExpiry.Add(time.Hour))
	if _, err := pool.Exec(ctx, `
UPDATE tenants
SET status='disabled', version=version+1, status_changed_at=now()
WHERE id=$1`, f.tenantA); err != nil {
		t.Fatalf("disable tenant: %v", err)
	}

	batch, err := svc.ProcessAutoRenewal(ctx, 100)
	if err != nil {
		t.Fatalf("process auto renewal: %v", err)
	}
	if batch.Scanned != 0 || batch.Renewed != 0 || batch.Skipped != 0 {
		t.Fatalf("disabled tenant batch=%+v, want no scanned work", batch)
	}

	replayed, err := svc.store.TryAutoRenewSubscription(ctx, autoRenewRecord{
		TenantID:       f.tenantA,
		SubscriptionID: assigned.Subscription.ID,
		Now:            clk.now(),
		DueCutoff:      clk.now(),
	})
	if err != nil {
		t.Fatalf("direct replay: %v", err)
	}
	if replayed.Renewed || replayed.SkipReason != AutoRenewSkipTenantInactive {
		t.Fatalf("direct replay=%+v, want tenant_inactive zero-side-effect skip", replayed)
	}
	if got := f.balanceOf(f.tenantA, f.userA); !got.Equal(decimal.RequireFromString("20")) {
		t.Fatalf("balance=%s, want 20 unchanged", got.String())
	}
	if got := f.userSubExpires(f.tenantA, f.userA); !got.Equal(originalExpiry) {
		t.Fatalf("expiry=%v, want %v unchanged", got, originalExpiry)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_auto_renewal_charges WHERE tenant_id=$1`, f.tenantA); n != 0 {
		t.Fatalf("charge rows=%d, want 0", n)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='subscription_auto_renewed'`, f.tenantA); n != 0 {
		t.Fatalf("billing events=%d, want 0", n)
	}
}

// 停用或删除的最终用户不得进入续费扫描；直接重放旧订阅 ID 也必须在同一事务内
// 零副作用跳过。变异：删除列表 JOIN 或事务内用户锁，会使扫描、余额或账本断言变红。
func TestPG_AutoRenew_InactiveUserCannotBeCharged(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		deletedSQL string
	}{
		{name: "停用用户", status: "disabled", deletedSQL: "NULL"},
		{name: "删除用户", status: "deleted", deletedSQL: "now()"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pool := openIntegrationPool(t, ctx)
			f := newSubFixture(t, ctx, pool)
			clk := &fakeClock{t: baseTime()}
			svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
			plan := createPaidPlan(t, ctx, svc, f.tenantA, 500)
			f.seedBalance(f.tenantA, f.userA, "20")

			assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
				TenantID: f.tenantA,
				UserID:   f.userA,
				PlanID:   plan.ID,
			})
			if err != nil {
				t.Fatalf("assign subscription: %v", err)
			}
			originalExpiry := assigned.Subscription.ExpiresAt
			clk.set(originalExpiry.Add(time.Hour))
			query := `UPDATE users SET status=$2, deleted_at=` + tc.deletedSQL + ` WHERE tenant_id=$1 AND id=$3`
			if _, err := pool.Exec(ctx, query, f.tenantA, tc.status, f.userA); err != nil {
				t.Fatalf("mark user inactive: %v", err)
			}

			batch, err := svc.ProcessAutoRenewal(ctx, 100)
			if err != nil {
				t.Fatalf("process auto renewal: %v", err)
			}
			if batch.Scanned != 0 || batch.Renewed != 0 || batch.Skipped != 0 {
				t.Fatalf("inactive user batch=%+v, want no scanned work", batch)
			}

			replayed, err := svc.store.TryAutoRenewSubscription(ctx, autoRenewRecord{
				TenantID:       f.tenantA,
				SubscriptionID: assigned.Subscription.ID,
				Now:            clk.now(),
				DueCutoff:      clk.now(),
			})
			if err != nil {
				t.Fatalf("direct replay: %v", err)
			}
			if replayed.Renewed || replayed.SkipReason != AutoRenewSkipUserInactive {
				t.Fatalf("direct replay=%+v, want user_inactive zero-side-effect skip", replayed)
			}
			if got := f.balanceOf(f.tenantA, f.userA); !got.Equal(decimal.RequireFromString("20")) {
				t.Fatalf("balance=%s, want 20 unchanged", got.String())
			}
			if got := f.userSubExpires(f.tenantA, f.userA); !got.Equal(originalExpiry) {
				t.Fatalf("expiry=%v, want %v unchanged", got, originalExpiry)
			}
			if n := f.countInt(`SELECT count(*) FROM subscription_auto_renewal_charges WHERE tenant_id=$1`, f.tenantA); n != 0 {
				t.Fatalf("charge rows=%d, want 0", n)
			}
			if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='subscription_auto_renewed'`, f.tenantA); n != 0 {
				t.Fatalf("billing events=%d, want 0", n)
			}
		})
	}
}

// A5: 免费套餐 (price_cents<=0) → 不扣款仍续期 (无钱包行也能续)。
// mutation: tryAutoRenewOnce 把 priceCents>0 改成 priceCents>=0 → 免费套餐去查不存在的钱包行扣款失败 →
// 续期被错误跳过 → 红。
func TestPG_AutoRenew_FreePlanRenewsWithoutCharge(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPaidPlan(t, ctx, svc, f.tenantA, 0) // 免费套餐, 无钱包行。

	assigned, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID})
	prevExpires := assigned.Subscription.ExpiresAt

	renewAt := prevExpires.Add(time.Hour)
	clk.set(renewAt)
	res, err := svc.ProcessAutoRenewal(ctx, 10)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Renewed != 1 {
		t.Fatalf("renewed=%d, want 1 (免费套餐应无条件续期)", res.Renewed)
	}
	got := f.userSubExpires(f.tenantA, f.userA)
	if !got.Equal(renewAt.AddDate(0, 0, 30)) {
		t.Fatalf("expires = %v, want %v (免费续期未延长)", got, renewAt.AddDate(0, 0, 30))
	}
	// 免费续费仍记一条 amount_cents=0 的账本行 (幂等锚)。
	if n := f.countInt(`SELECT count(*) FROM subscription_auto_renewal_charges WHERE tenant_id=$1 AND amount_cents=0`, f.tenantA); n != 1 {
		t.Fatalf("free charge rows = %d, want 1", n)
	}
}

// B1-1: 提前续费窗口 (renew-ahead grace) —— 订阅尚未到点但落在 now+lead 窗口内, 余额够 →
// 续费在到期前完成 (扣款 + 从旧到期日累加续期, 未过期续期不重置用量窗口)。
// 这是 B1 核心修复: 不加提前窗口时 ListAutoRenewDue 只扫已到点行, 续费永远抢不过 1min 节拍的
// ExpiryWorker, 付费用户被停服降级还没续上。
// mutation: ProcessAutoRenewal 的 cutoff 退回 `now`(去掉 .Add(lead)) → 窗口内订阅漏扫 → Renewed=0 → 红。
func TestPG_AutoRenew_GraceWindowRenewsBeforeExpiry(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPaidPlan(t, ctx, svc, f.tenantA, 500) // $5.00
	f.seedBalance(f.tenantA, f.userA, "20")

	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	prevExpires := assigned.Subscription.ExpiresAt

	// 推进到到期前 10 分钟: 订阅仍 active 且未到点, 落在 30min 提前窗口内。
	clk.set(prevExpires.Add(-10 * time.Minute))
	res, err := svc.ProcessAutoRenewal(ctx, 10)
	if err != nil {
		t.Fatalf("process auto renewal: %v", err)
	}
	if res.Renewed != 1 || res.Skipped != 0 {
		t.Fatalf("batch renewed=%d skipped=%d, want 1/0 (提前窗口内应续费, 否则续费抢不过到期 worker)", res.Renewed, res.Skipped)
	}
	// 扣款精确 $20-$5=$15。
	if got := f.balanceOf(f.tenantA, f.userA); !got.Equal(decimal.RequireFromString("15")) {
		t.Fatalf("balance = %s, want 15", got.String())
	}
	// 未过期续期: 从旧到期日累加 30 天 (用户不损失时长), 而非从 now 起算。
	got := f.userSubExpires(f.tenantA, f.userA)
	wantExpires := prevExpires.AddDate(0, 0, 30)
	if !got.Equal(wantExpires) {
		t.Fatalf("expires = %v, want %v (提前续期应从旧到期日累加)", got, wantExpires)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_auto_renewal_charges WHERE tenant_id=$1`, f.tenantA); n != 1 {
		t.Fatalf("charge rows = %d, want 1", n)
	}
}

// B1-2: 到期 worker 锁内复查堵 TOCTOU —— 订阅先被续费推后到期日, 随后到期路径不得错杀。
// 模拟时序: 续费成功 (expires_at 推后到远期) → 冻结在续费后、到期日之前的时刻调 ExpireSubscription,
// 应 no-op 保持 active (到期扫描快照与拿锁之间被续费的经典竞态)。
// mutation: closeSubscriptionOnce 去掉 `terminal==StatusExpired && !sub.IsExpiredAt(rec.Now)` 复查
// → 刚续费的订阅被置 expired → 红 (付费用户刚扣完钱就被停服)。
func TestPG_Expire_SkipsRowRenewedAfterScan(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	store := NewPostgresStore(pool)
	svc := NewService(store, WithClock(clk.now))
	plan := createPaidPlan(t, ctx, svc, f.tenantA, 500)
	f.seedBalance(f.tenantA, f.userA, "20")

	assigned, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID})
	prevExpires := assigned.Subscription.ExpiresAt

	// 提前窗口内续费成功 → expires_at 推后到 prevExpires+30 天。
	clk.set(prevExpires.Add(-10 * time.Minute))
	if _, err := svc.ProcessAutoRenewal(ctx, 10); err != nil {
		t.Fatalf("renew: %v", err)
	}
	renewedExpires := f.userSubExpires(f.tenantA, f.userA)
	if !renewedExpires.After(prevExpires) {
		t.Fatalf("续费未推后到期日: %v", renewedExpires)
	}

	// 现在直接对同订阅调 ExpireSubscription, now 仍在续费后的到期日之前 → 锁内复查应 no-op。
	got, err := store.ExpireSubscription(ctx, lifecycleRecord{
		TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, ActorKind: ActorKindSystem, Now: clk.now(),
	})
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("到期复查失效: 刚续费的订阅被置 %s, 应保持 active (错杀付费用户)", got.Status)
	}
	// 库内确认仍 active + 到期日未回退。
	reloaded, _ := store.GetSubscription(ctx, f.tenantA, assigned.Subscription.ID)
	if reloaded.Status != StatusActive || !reloaded.ExpiresAt.Equal(renewedExpires) {
		t.Fatalf("库内 status=%s expires=%v, want active + %v", reloaded.Status, reloaded.ExpiresAt, renewedExpires)
	}
}

// B6-1: 付费续费同事务写统一 money 账本 billing_events(与充值/退款/兑换同流可对账)。
// 断言: 恰一行 subscription_auto_renewed、actual_cost_signed=-5(钱包流出负号,沿用退款先例)、
// 关联列指向 charge 行、charge 行回链 billing_event_id。
// mutation: tryAutoRenewOnce 去掉 insertAutoRenewalBillingEventTx 调用 → 零事件行 → 红。
func TestPG_AutoRenew_WritesUnifiedBillingEvent(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPaidPlan(t, ctx, svc, f.tenantA, 500) // $5.00
	f.seedBalance(f.tenantA, f.userA, "20")

	assigned, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID})
	clk.set(assigned.Subscription.ExpiresAt.Add(time.Hour))
	if _, err := svc.ProcessAutoRenewal(ctx, 10); err != nil {
		t.Fatalf("renew: %v", err)
	}

	// 恰一行 subscription_auto_renewed, 负符号精确 -5。
	var n int64
	var signed string
	if err := pool.QueryRow(ctx, `
SELECT count(*), COALESCE(min(actual_cost_signed::text), '')
FROM billing_events WHERE tenant_id=$1 AND event_type='subscription_auto_renewed'`, f.tenantA).Scan(&n, &signed); err != nil {
		t.Fatalf("query billing events: %v", err)
	}
	if n != 1 {
		t.Fatalf("subscription_auto_renewed 事件行 = %d, want 1 (续费扣款没进统一账本, 对账缺环)", n)
	}
	if !decimal.RequireFromString(signed).Equal(decimal.RequireFromString("-5")) {
		t.Fatalf("actual_cost_signed = %s, want -5 (钱包流出必须负号, 否则净流向对账错)", signed)
	}
	// 关联列与回链双向配对。
	var chargeRef, backlink int64
	if err := pool.QueryRow(ctx, `
SELECT be.subscription_auto_renewal_charge_id, c.billing_event_id
FROM billing_events be
JOIN subscription_auto_renewal_charges c
  ON c.tenant_id=be.tenant_id AND c.id=be.subscription_auto_renewal_charge_id
WHERE be.tenant_id=$1 AND be.event_type='subscription_auto_renewed'`, f.tenantA).Scan(&chargeRef, &backlink); err != nil {
		t.Fatalf("join charge link: %v", err)
	}
	var billingID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM billing_events WHERE tenant_id=$1 AND event_type='subscription_auto_renewed'`, f.tenantA).Scan(&billingID); err != nil {
		t.Fatalf("read billing id: %v", err)
	}
	if backlink != billingID {
		t.Fatalf("charge.billing_event_id = %d, want %d (回链断裂)", backlink, billingID)
	}
}

// B6-2: 免费续费 (price=0) 无钱移动 → 不写 billing_events(账本只记 money movement);
// 幂等重跑同周期 → 事件行不重复。
// mutation: 把 priceCents>0 门槛去掉 → 免费续费也写一行(signed=0 噪声)→ 第一断言红。
func TestPG_AutoRenew_FreePlanNoBillingEventAndIdempotentNoDup(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))

	// 免费续费: 零事件行。
	freePlan := createPaidPlan(t, ctx, svc, f.tenantA, 0)
	freeAssigned, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: freePlan.ID})
	clk.set(freeAssigned.Subscription.ExpiresAt.Add(time.Hour))
	if _, err := svc.ProcessAutoRenewal(ctx, 10); err != nil {
		t.Fatalf("free renew: %v", err)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='subscription_auto_renewed'`, f.tenantA); n != 0 {
		t.Fatalf("免费续费事件行 = %d, want 0 (无钱移动不进账本)", n)
	}

	// 顺序重跑: 第一次续费推后 expires_at, 第二次锁内到点复查即跳过 → 事件行不重复。
	// (守的是锁内复查防双扣双记账; 变异: 删掉复查 → 第二次真双扣双记账 → 事件行=2 → 红。)
	paidPlan := createPaidPlan(t, ctx, svc, f.tenantB, 300)
	f.seedBalance(f.tenantB, f.userB, "20")
	paidAssigned, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantB, UserID: f.userB, PlanID: paidPlan.ID})
	clk.set(paidAssigned.Subscription.ExpiresAt.Add(time.Hour))
	store := NewPostgresStore(pool)
	for i := 0; i < 2; i++ {
		if _, err := store.TryAutoRenewSubscription(ctx, autoRenewRecord{TenantID: f.tenantB, SubscriptionID: paidAssigned.Subscription.ID, Now: clk.now()}); err != nil {
			t.Fatalf("renew #%d: %v", i+1, err)
		}
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='subscription_auto_renewed'`, f.tenantB); n != 1 {
		t.Fatalf("幂等重跑后事件行 = %d, want 1 (重复写账本=对账虚增流出)", n)
	}
}

// A6: 并发多 worker 同时续费同一订阅 → 必须只扣一次只续一次。
// 真并发下双扣是最危险的 money 缺陷; Serializable + getSubscriptionForUpdateTx 行锁串行化,
// 加唯一锚 uq_sub_auto_renewal_period 硬兜底。
// mutation: 去掉唯一索引 + 把 isPgRetryableTxConflict 当 already_renewed 之外的处理 → 多条扣款 → 红。
func TestPG_AutoRenew_ConcurrentNoDoubleCharge(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPaidPlan(t, ctx, svc, f.tenantA, 500)
	f.seedBalance(f.tenantA, f.userA, "20")
	assigned, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID})
	clk.set(assigned.Subscription.ExpiresAt.Add(time.Hour))

	store := NewPostgresStore(pool)
	var wg sync.WaitGroup
	renewed := make([]bool, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, _ := store.TryAutoRenewSubscription(ctx, autoRenewRecord{TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, Now: clk.now()})
			renewed[i] = r.Renewed
		}(i)
	}
	wg.Wait()

	cnt := 0
	for _, r := range renewed {
		if r {
			cnt++
		}
	}
	if cnt != 1 {
		t.Fatalf("并发续费成功次数 = %d, want 1 (并发双扣!)", cnt)
	}
	if got := f.balanceOf(f.tenantA, f.userA); !got.Equal(decimal.RequireFromString("15")) {
		t.Fatalf("balance = %s, want 15 (并发把余额多扣了)", got.String())
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_auto_renewal_charges WHERE tenant_id=$1`, f.tenantA); n != 1 {
		t.Fatalf("charge rows = %d, want 1 (并发重复记账)", n)
	}
	// 统一账本同样只一行: 撞锚回滚必须连带事件行回滚 (同事务), 并发不虚增钱包流出。
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='subscription_auto_renewed'`, f.tenantA); n != 1 {
		t.Fatalf("billing_events 行 = %d, want 1 (并发把统一账本写重了)", n)
	}
}
