// HUAKAI · iKun
//go:build integration_pg

package subscription

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// insertActiveSub 直接插一条 active 订阅 (精确控制 expires_at), 返回 sub id。
func (f *subFixture) insertActiveSub(planID, userID int64, expiresAt time.Time) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(f.ctx, `
INSERT INTO user_subscriptions
	(tenant_id, user_id, plan_id, granted_group, status, source, prev_user_group, starts_at, expires_at)
VALUES ($1, $2, $3, 'premium', 'active', 'admin', 'default', $4, $5)
RETURNING id`, f.tenantA, userID, planID, expiresAt.Add(-30*24*time.Hour), expiresAt).Scan(&id); err != nil {
		f.t.Fatalf("insert active sub: %v", err)
	}
	return id
}

func (f *subFixture) seedPlan() int64 {
	f.t.Helper()
	store := NewPostgresStore(f.pool)
	plan, err := store.CreatePlan(f.ctx, createPlanRecord{
		TenantID: f.tenantA, Name: "月付Pro", CurrencyCode: "USD",
		ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: dec("100"),
		ForSale: true, Now: time.Now().UTC(),
	})
	if err != nil {
		f.t.Fatalf("seed plan: %v", err)
	}
	return plan.ID
}

// TestPG_ReminderFlow_SendsDedupsAndRecords 端到端: 真 PG store + ReminderService 发送 → 记账 → 去重。
// 判别性: 去掉 RecordReminder 的 ON CONFLICT DO NOTHING 或 ProcessDueReminders 的去重检查,
// 第二次 tick 会再发 -> mailer 调用 2 次 / 账本 2 行 -> 红。
func TestPG_ReminderFlow_SendsDedupsAndRecords(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	planID := f.seedPlan()
	f.setUserEmail(f.tenantA, f.userA, "alice@example.com")
	subID := f.insertActiveSub(planID, f.userA, now.Add(2*24*time.Hour)) // 剩 2 天 -> tier 3

	mailer := &fakeMailer{outcome: ReminderSent}
	clock := now
	rsvc := NewReminderService(NewPostgresStore(pool), mailer, WithReminderClock(func() time.Time { return clock }))

	if sent, err := rsvc.ProcessDueReminders(ctx, 100); err != nil || sent != 1 {
		t.Fatalf("first tick: sent=%d err=%v, want 1", sent, err)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_expiry_reminders WHERE tenant_id=$1 AND user_subscription_id=$2`, f.tenantA, subID); n != 1 {
		t.Fatalf("reminder rows = %d, want 1", n)
	}
	// 校验记录内容: 档位 / 状态 / 收件人。
	var key, status, recipient string
	if err := pool.QueryRow(ctx, `SELECT reminder_key, status, recipient FROM subscription_expiry_reminders WHERE tenant_id=$1 AND user_subscription_id=$2`, f.tenantA, subID).Scan(&key, &status, &recipient); err != nil {
		t.Fatalf("read reminder row: %v", err)
	}
	// 去重键含到期日(reminderDedupKey): sub 到期 2026-06-03 → 复合键 "3@2026-06-03"。
	if key != "3@2026-06-03" || status != ReminderStatusSent || recipient != "alice@example.com" {
		t.Fatalf("row = (key=%q status=%q recipient=%q), want (3@2026-06-03, sent, alice@example.com)", key, status, recipient)
	}

	// 第二次 tick: 去重, 不再发。
	if sent, err := rsvc.ProcessDueReminders(ctx, 100); err != nil || sent != 0 {
		t.Fatalf("second tick: sent=%d err=%v, want 0 (dedup)", sent, err)
	}
	if mailer.count() != 1 {
		t.Fatalf("mailer calls = %d, want 1 across two ticks", mailer.count())
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_expiry_reminders WHERE tenant_id=$1 AND user_subscription_id=$2`, f.tenantA, subID); n != 1 {
		t.Fatalf("reminder rows after second tick = %d, want 1", n)
	}
}

// TestPG_ReminderPaginationDrainsAllPages 真 PG 游标翻页: limit=1 时窗口内 3 条订阅一次 tick 全发,
// 验证 (expires_at, id) 行值游标在 Postgres 上正确推进 (不卡在第一页)。
// 判别性: 若游标条件失效 (总返回最早一页), 后两条永不处理 -> sent<3 -> 红。
func TestPG_ReminderPaginationDrainsAllPages(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	planID := f.seedPlan()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	// 3 个用户各一条订阅, 错开到期时间 (确保 (expires_at,id) 严格有序, 翻页确定)。
	userA3 := f.seedUser(f.tenantA, "a3")
	for i, uid := range []int64{f.userA, f.userA2, userA3} {
		f.setUserEmail(f.tenantA, uid, fmt.Sprintf("u%d@example.com", uid))
		f.insertActiveSub(planID, uid, now.Add(time.Duration(2*24+i)*time.Hour))
	}

	mailer := &fakeMailer{outcome: ReminderSent}
	clock := now
	rsvc := NewReminderService(NewPostgresStore(pool), mailer, WithReminderClock(func() time.Time { return clock }))

	// limit=1 强制多页; 游标翻页应一次 tick 发完全部 3 条。
	sent, err := rsvc.ProcessDueReminders(ctx, 1)
	if err != nil || sent != 3 {
		t.Fatalf("paginated process: sent=%d err=%v, want 3", sent, err)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_expiry_reminders WHERE tenant_id=$1 AND status='sent'`, f.tenantA); n != 3 {
		t.Fatalf("sent rows = %d, want 3", n)
	}
}

// TestPG_RecordReminderUniqueIndex 唯一索引去重: 同 (tenant, sub, key) 第二次插入返回 false。
func TestPG_RecordReminderUniqueIndex(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	planID := f.seedPlan()
	subID := f.insertActiveSub(planID, f.userA, time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC))

	rec := reminderRecord{TenantID: f.tenantA, SubscriptionID: subID, ReminderKey: "3", Status: ReminderStatusSent, Recipient: "a@x.com", ExpiresAt: time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)}
	if ins, err := store.RecordReminder(ctx, rec); err != nil || !ins {
		t.Fatalf("first record: inserted=%v err=%v, want true", ins, err)
	}
	if ins, err := store.RecordReminder(ctx, rec); err != nil || ins {
		t.Fatalf("second record: inserted=%v err=%v, want false (unique index)", ins, err)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_expiry_reminders WHERE tenant_id=$1 AND user_subscription_id=$2 AND reminder_key='3'`, f.tenantA, subID); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

// TestPG_ListDueReminderWindowAndEmail 窗口边界 + 用户邮箱 join。
func TestPG_ListDueReminderWindowAndEmail(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	planID := f.seedPlan()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	f.setUserEmail(f.tenantA, f.userA, "in-window@example.com")
	inWindow := f.insertActiveSub(planID, f.userA, now.Add(2*24*time.Hour))    // 命中 (2d)
	outWindow := f.insertActiveSub(planID, f.userA2, now.Add(10*24*time.Hour)) // 超 7d 窗口
	// userA2 也作过期 sub 用户: 直接插一条已过期的 (不同 group 避免唯一冲突)。
	var expired int64
	if err := pool.QueryRow(ctx, `
INSERT INTO user_subscriptions (tenant_id, user_id, plan_id, granted_group, status, source, prev_user_group, starts_at, expires_at)
VALUES ($1,$2,$3,'legacy','active','admin','default',$4,$5) RETURNING id`,
		f.tenantA, f.userA, planID, now.Add(-40*24*time.Hour), now.Add(-time.Hour)).Scan(&expired); err != nil {
		t.Fatalf("insert expired-window sub: %v", err)
	}

	cands, err := store.ListDueReminder(ctx, now, 7*24*time.Hour, ReminderCursor{}, 100)
	if err != nil {
		t.Fatalf("list due reminder: %v", err)
	}
	got := map[int64]ReminderCandidate{}
	for _, c := range cands {
		got[c.SubscriptionID] = c
	}
	if _, ok := got[outWindow]; ok {
		t.Fatalf("sub expiring in 10d must be outside 7d window")
	}
	if _, ok := got[expired]; ok {
		t.Fatalf("already-expired sub must be excluded")
	}
	c, ok := got[inWindow]
	if !ok {
		t.Fatalf("in-window sub missing from candidates")
	}
	if c.RecipientEmail != "in-window@example.com" || c.PlanName != "月付Pro" {
		t.Fatalf("candidate = (email=%q plan=%q), want (in-window@example.com, 月付Pro)", c.RecipientEmail, c.PlanName)
	}
}

// 停用租户的订阅仍由到期 worker 正常收尾，但不得继续创建提醒投递事实或发送邮件。
// mutation: 去掉 ListDueReminder 的活跃租户 join，会返回候选并触发 mailer。
func TestPG_ReminderDisabledTenantIsNotDelivered(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	planID := f.seedPlan()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f.setUserEmail(f.tenantA, f.userA, "disabled@example.com")
	subID := f.insertActiveSub(planID, f.userA, now.Add(2*24*time.Hour))
	if _, err := pool.Exec(ctx, `
UPDATE tenants
SET status='disabled', version=version+1, status_changed_at=now()
WHERE id=$1`, f.tenantA); err != nil {
		t.Fatalf("disable tenant: %v", err)
	}

	mailer := &fakeMailer{outcome: ReminderSent}
	rsvc := NewReminderService(
		NewPostgresStore(pool),
		mailer,
		WithReminderClock(func() time.Time { return now }),
	)
	sent, err := rsvc.ProcessDueReminders(ctx, 100)
	if err != nil {
		t.Fatalf("process reminders: %v", err)
	}
	if sent != 0 || mailer.count() != 0 {
		t.Fatalf("sent=%d mailer_calls=%d, want 0/0", sent, mailer.count())
	}
	if n := f.countInt(`
SELECT count(*)
FROM subscription_expiry_reminders
WHERE tenant_id=$1 AND user_subscription_id=$2`, f.tenantA, subID); n != 0 {
		t.Fatalf("reminder rows=%d, want 0", n)
	}
}

// TestPG_MissingRecipientSkippedNotSent 无邮箱 -> 记 skipped_no_recipient, mailer 不被调用。
// 判别性: 去掉空邮箱守卫, 会用空 To 调 mailer -> count>0 -> 红。
func TestPG_MissingRecipientSkippedNotSent(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	planID := f.seedPlan()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	subID := f.insertActiveSub(planID, f.userA, now.Add(2*24*time.Hour)) // userA 无 email (未 set)

	mailer := &fakeMailer{outcome: ReminderSent}
	clock := now
	rsvc := NewReminderService(NewPostgresStore(pool), mailer, WithReminderClock(func() time.Time { return clock }))

	if sent, err := rsvc.ProcessDueReminders(ctx, 100); err != nil || sent != 0 {
		t.Fatalf("tick: sent=%d err=%v, want 0", sent, err)
	}
	if mailer.count() != 0 {
		t.Fatalf("mailer calls = %d, want 0 (no recipient)", mailer.count())
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM subscription_expiry_reminders WHERE tenant_id=$1 AND user_subscription_id=$2`, f.tenantA, subID).Scan(&status); err != nil {
		t.Fatalf("read skip row: %v", err)
	}
	if status != ReminderStatusSkippedNoRecipient {
		t.Fatalf("status = %q, want skipped_no_recipient", status)
	}
}

// TestPG_CrossTenantReminderIsolation A 租户提醒记录不串到 B。
// 判别性: 去掉 SentReminderKeys 的 tenant 谓词 -> B 看到 A 的档位 -> 红。
func TestPG_CrossTenantReminderIsolation(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	planA := f.seedPlan()
	subA := f.insertActiveSub(planA, f.userA, time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC))

	if _, err := store.RecordReminder(ctx, reminderRecord{
		TenantID: f.tenantA, SubscriptionID: subA, ReminderKey: "3", Status: ReminderStatusSent,
		Recipient: "a@x.com", ExpiresAt: time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("record A: %v", err)
	}
	// 用 B 租户 + A 的 sub id 查 -> 不得命中 (tenant 谓词)。
	keysWrongTenant, err := store.SentReminderKeys(ctx, f.tenantB, subA)
	if err != nil {
		t.Fatalf("sent keys wrong tenant: %v", err)
	}
	if len(keysWrongTenant) != 0 {
		t.Fatalf("tenant B must not see tenant A reminder keys, got %v", keysWrongTenant)
	}
	// 正确租户能看到。
	keysRight, _ := store.SentReminderKeys(ctx, f.tenantA, subA)
	if _, ok := keysRight["3"]; !ok {
		t.Fatalf("tenant A should see its own key '3', got %v", keysRight)
	}
}
