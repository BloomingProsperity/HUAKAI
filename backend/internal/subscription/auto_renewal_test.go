// HUAKAI · iKun

// 自动续费 worker 的纯逻辑单测 (无 PG): worker 生命周期 + ListAutoRenewDue 过滤。
// money 续费事务 (扣 user_balances + 幂等锚 + 续期) 的不变量以 integration_pg 真 PG 测试为准
// (见 auto_renewal_integration_test.go), 内存 store 无钱包模型, 不在此假实现。

package subscription

import (
	"context"
	"testing"
	"time"
)

// TestAutoRenewWorker_StartStopLifecycle 守 worker 生命周期: Start 后 TickOnce 计数累加,
// Stop 优雅退出 (不挂死)。svc==nil 时 Start no-op (不 panic)。
// mutation: 把 tick 里的 tickCount.Add(1) 删掉 → TickCount 断言红。
func TestAutoRenewWorker_StartStopLifecycle(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	svc, _ := newTestService(&now)
	ctx := context.Background()

	w := NewAutoRenewWorker(AutoRenewWorkerConfig{Service: svc, BatchSize: 10})
	w.TickOnce(ctx)
	if w.TickCount() != 1 {
		t.Fatalf("tick count = %d, want 1", w.TickCount())
	}
	w.Start(ctx)
	w.Stop() // 必须返回, 不挂死。
	w.Stop() // 多次 Stop no-op。

	// svc==nil 的 worker: Start 是 no-op, 不 panic。
	nilWorker := NewAutoRenewWorker(AutoRenewWorkerConfig{})
	nilWorker.Start(ctx)
	nilWorker.Stop()
}

// TestAutoRenewWorker_DefaultsApplied 守构造默认值: interval/batch 非正时回退默认。
// mutation: 删掉 NewAutoRenewWorker 里的默认回退 → batchSize=0 → 此断言红。
func TestAutoRenewWorker_DefaultsApplied(t *testing.T) {
	w := NewAutoRenewWorker(AutoRenewWorkerConfig{})
	if w.interval != DefaultAutoRenewInterval {
		t.Fatalf("interval = %v, want default %v", w.interval, DefaultAutoRenewInterval)
	}
	if w.batchSize != DefaultAutoRenewBatchSize {
		t.Fatalf("batch = %d, want default %d", w.batchSize, DefaultAutoRenewBatchSize)
	}
}

// TestListAutoRenewDue_GraceWindowIncludesSoonToExpire 守提前续费窗口(renew-ahead grace):
// 未到期但落在 [now, cutoff] 窗口的订阅, 用 cutoff=now+lead 扫得到、用 cutoff=now 扫不到。
// 这是 B1 修复的核心——续费须抢在 ExpiryWorker(1min 节拍)把订阅置 expired 前完成。
// mutation: store_memory.ListAutoRenewDue 的过滤退回 `!After(now)` 忽略 cutoff → 提前窗口断言红。
func TestListAutoRenewDue_GraceWindowIncludesSoonToExpire(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 10, "default")
	plan, _ := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: 1, GrantedGroup: "premium"})
	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 10, PlanID: plan.ID})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	expires := assigned.Subscription.ExpiresAt // now + 1 天

	// 把时钟推进到到期前 10 分钟: 订阅仍 active 且未到点, 但落在 30min 提前窗口内。
	soon := expires.Add(-10 * time.Minute)

	// cutoff=now(=soon): 订阅尚未到点 → 不返回 (旧语义, 正是竞态成因)。
	if got, _ := store.ListAutoRenewDue(ctx, soon, 100); len(got) != 0 {
		t.Fatalf("cutoff=now 时提前窗口内订阅返回了 %d 条, want 0", len(got))
	}
	// cutoff=now+lead: 订阅落入提前窗口 → 应返回, 供续费抢在到期前完成。
	got, err := store.ListAutoRenewDue(ctx, soon.Add(DefaultAutoRenewLeadWindow), 100)
	if err != nil {
		t.Fatalf("list auto renew due (grace): %v", err)
	}
	if len(got) != 1 || got[0].ID != assigned.Subscription.ID {
		t.Fatalf("cutoff=now+lead 应返回提前窗口内订阅 1 条, got %d (提前续费窗口失效→续费永远抢不过到期)", len(got))
	}
}

// TestProcessAutoRenewal_UsesLeadWindowCutoff 守 service 层用 now+lead 作 cutoff 下扫:
// 提前窗口内的订阅被扫描到 (Scanned>=1)。内存 store 的续费事务是桩(返回 not_due), 故只断言
// Scanned——它直接来自 ListAutoRenewDue 结果, 能证明 cutoff 带了提前量。
// mutation: ProcessAutoRenewal 里 `cutoff := now.Add(DefaultAutoRenewLeadWindow)` 退回 `cutoff := now`
// → 提前窗口内订阅扫不到 → Scanned=0 → 红。
func TestProcessAutoRenewal_UsesLeadWindowCutoff(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 10, "default")
	plan, _ := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: 1, GrantedGroup: "premium"})
	assigned, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 10, PlanID: plan.ID})

	// 推进到到期前 10 分钟 (窗口内, 未到点)。
	now = assigned.Subscription.ExpiresAt.Add(-10 * time.Minute)
	res, err := svc.ProcessAutoRenewal(ctx, 100)
	if err != nil {
		t.Fatalf("process auto renewal: %v", err)
	}
	if res.Scanned != 1 {
		t.Fatalf("Scanned = %d, want 1 (service cutoff 未带提前量→提前窗口订阅漏扫)", res.Scanned)
	}
}

// TestExpireSubscription_SkipsIfRenewedAfterScan 守到期路径锁内复查(堵 TOCTOU 错杀):
// 订阅仍 active 但 expires_at 在未来(模拟到期扫描后、置终态前被续费推后到期日),
// 以 now<expires_at 调 ExpireSubscription 应 no-op, 订阅保持 active。
// mutation: closeMemWithAudit 去掉 `terminal==StatusExpired && sub.ExpiresAt.After(rec.Now)` 复查
// → 未到期订阅被置 expired → 红(这正是错杀刚续费订阅的缺陷)。
func TestExpireSubscription_SkipsIfRenewedAfterScan(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 10, "default")
	plan, _ := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: 30, GrantedGroup: "premium"})
	assigned, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 10, PlanID: plan.ID})

	// now 不推进: 订阅 expires_at = now+30天 (未到点)。直接调 Expire 模拟"扫描时到点、拿锁时已被续费推后"。
	got, err := store.ExpireSubscription(ctx, lifecycleRecord{TenantID: 1, SubscriptionID: assigned.Subscription.ID, ActorKind: ActorKindSystem, Now: now})
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("到期复查失效: 未到点订阅被置 %s, 应保持 active(否则错杀刚续费的订阅)", got.Status)
	}
	// 二次确认库内状态。
	reloaded, _ := store.GetSubscription(ctx, 1, assigned.Subscription.ID)
	if reloaded.Status != StatusActive {
		t.Fatalf("库内订阅状态 = %s, 应 active", reloaded.Status)
	}
}

// TestListAutoRenewDue_FiltersAutoRenewAndDue 守 ListAutoRenewDue 的过滤语义:只返
// active + auto_renew=true + 到点 (expires_at<=now) 的订阅。两个判别陷阱各设一个对照:
//   - 关 auto_renew 的订阅 (user11) 即使到点也不能进结果 (否则给未 opt-in 用户自动扣费)。
//   - 未到点 (now 未推进) 时谁都不该进结果 (守 expires_at<=now 闸)。
//
// mutation: store_memory.ListAutoRenewDue 去 `&& sub.AutoRenew` → user11 混入 (count 2) → 红;
//
//	去 `&& !sub.ExpiresAt.After(now)` → 未推进时 count!=0 → 第一处断言红。
func TestListAutoRenewDue_FiltersAutoRenewAndDue(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 10, "default") // 到点 + auto_renew=true → 应入
	store.seedUser(1, 11, "default") // 到点 + auto_renew=false → 不应入
	plan, _ := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: 30, GrantedGroup: "premium"})

	due, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 10, PlanID: plan.ID})
	off, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 11, PlanID: plan.ID})

	// 关掉 user 11 的 auto_renew。
	if _, err := svc.SetAutoRenew(ctx, 1, 11, false); err != nil {
		t.Fatalf("set auto_renew off: %v", err)
	}

	// 未到点 (now 未推进): 守 expires_at<=now 闸, 谁都不该进。
	if got, _ := store.ListAutoRenewDue(ctx, now, 100); len(got) != 0 {
		t.Fatalf("到期前 due count = %d, want 0 (expires_at<=now 闸失效会让未到点订阅被自动续费)", len(got))
	}

	now = now.AddDate(0, 0, 31) // 两者都到点。
	got, err := store.ListAutoRenewDue(ctx, now, 100)
	if err != nil {
		t.Fatalf("list auto renew due: %v", err)
	}
	// 期望只 user10 (due+auto_renew on)。user11 auto_renew=off 被滤除。
	if len(got) != 1 {
		t.Fatalf("due count = %d, want 1 (只 user10; user11 auto_renew=off 必须被滤除)", len(got))
	}
	if got[0].ID != due.Subscription.ID {
		t.Fatalf("due[0] id = %d, want user10 sub %d", got[0].ID, due.Subscription.ID)
	}
	if got[0].ID == off.Subscription.ID {
		t.Fatal("auto_renew=off 订阅混入了 due 列表 —— 会对未 opt-in 用户自动扣费")
	}
}
