// HUAKAI · iKun

package subscription

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeMailer 记录每次发送调用并按配置返回结果 (测试用)。
type fakeMailer struct {
	mu      sync.Mutex
	calls   []fakeSend
	outcome ReminderOutcome
}

type fakeSend struct {
	tenantID int64
	to       string
	subject  string
	body     string
}

type reminderResult struct {
	sent int
	err  error
}

type sentKeyBarrierStore struct {
	ReminderStore

	mu      sync.Mutex
	needed  int
	waiting int
	release chan struct{}
}

func newSentKeyBarrierStore(inner ReminderStore, needed int) *sentKeyBarrierStore {
	return &sentKeyBarrierStore{
		ReminderStore: inner,
		needed:        needed,
		release:       make(chan struct{}),
	}
}

func (s *sentKeyBarrierStore) SentReminderKeys(ctx context.Context, tenantID, subscriptionID int64) (map[string]struct{}, error) {
	keys, err := s.ReminderStore.SentReminderKeys(ctx, tenantID, subscriptionID)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.waiting++
	if s.waiting == s.needed {
		close(s.release)
	}
	release := s.release
	s.mu.Unlock()

	select {
	case <-release:
		return keys, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeMailer) SendReminder(_ context.Context, tenantID int64, to, subject, body string) ReminderOutcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeSend{tenantID, to, subject, body})
	return f.outcome
}

func (f *fakeMailer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// day 是一天的时长 (与 currentReminderTier 内部按 24h*天数 计算对齐)。
const day = 24 * time.Hour

// TestCurrentReminderTier_BandModel 校验 band 分档: 命中"满足 remaining<=offset 的最小 offset"。
// 判别性: 若改成"最大已越过档"(remaining=2d 误判 7), 或把 <= 改成 <, 下列 case 会变红。
func TestCurrentReminderTier_BandModel(t *testing.T) {
	offsets := []int{7, 3, 1}
	cases := []struct {
		name      string
		remaining time.Duration
		wantTier  int
		wantOK    bool
	}{
		{"2 days left -> tier 3 (not 7)", 2 * day, 3, true},
		{"5 days left -> tier 7", 5 * day, 7, true},
		{"half day left -> tier 1", 12 * time.Hour, 1, true},
		{"exactly 1 day -> tier 1", 1 * day, 1, true},
		{"exactly 3 days -> tier 3", 3 * day, 3, true},
		{"exactly 7 days -> tier 7", 7 * day, 7, true},
		{"just over 1 day -> tier 3", 1*day + time.Minute, 3, true},
		{"8 days left -> none (too early)", 8 * day, 0, false},
		{"already expired -> none", -time.Hour, 0, false},
		{"zero remaining -> none", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tier, ok := currentReminderTier(tc.remaining, offsets)
			if ok != tc.wantOK || tier != tc.wantTier {
				t.Fatalf("remaining=%v -> (tier=%d, ok=%v), want (tier=%d, ok=%v)",
					tc.remaining, tier, ok, tc.wantTier, tc.wantOK)
			}
		})
	}
}

// reminderHarness 建一个共享 store 的分配 Service + 提醒 Service, 两个时钟独立可控。
func reminderHarness(assignNow, reminderNow *time.Time, mailer ReminderMailer, offsets []int) (*Service, *ReminderService, *memoryStore) {
	store := newMemoryStore()
	svc := NewService(store, WithClock(func() time.Time { return *assignNow }))
	opts := []ReminderOption{WithReminderClock(func() time.Time { return *reminderNow })}
	if offsets != nil {
		opts = append(opts, WithReminderOffsets(offsets))
	}
	rsvc := NewReminderService(store, mailer, opts...)
	return svc, rsvc, store
}

// assignSubExpiringIn 分配一条 30 天有效期订阅, 调整 reminderNow 使剩余 = remaining。
func assignSubExpiringIn(t *testing.T, svc *Service, store *memoryStore, assignNow, reminderNow *time.Time, tenantID, userID int64, email string, remaining time.Duration) int64 {
	t.Helper()
	ctx := context.Background()
	store.seedUser(tenantID, userID, "default")
	store.setUserEmail(tenantID, userID, email)
	plan, err := svc.CreatePlan(ctx, CreatePlanInput{TenantID: tenantID, Name: "月付Pro", ValidityDays: 30, GrantedGroup: "premium"})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	res, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: tenantID, UserID: userID, PlanID: plan.ID})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	// 订阅到期 = assignNow + 30d; 设 reminderNow 使 (expires - reminderNow) = remaining。
	*reminderNow = res.Subscription.ExpiresAt.Add(-remaining)
	return res.Subscription.ID
}

// TestReminder_DedupAcrossTicks 同档位多 tick 只发一次。
// 判别性: 去掉 SentReminderKeys 去重, 第二次 tick 会再发 -> count==2 -> 红。
func TestReminder_DedupAcrossTicks(t *testing.T) {
	assignNow := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	var reminderNow time.Time
	mailer := &fakeMailer{outcome: ReminderSent}
	svc, rsvc, store := reminderHarness(&assignNow, &reminderNow, mailer, nil)
	subID := assignSubExpiringIn(t, svc, store, &assignNow, &reminderNow, 1, 42, "u@example.com", 2*day)

	ctx := context.Background()
	if sent, err := rsvc.ProcessDueReminders(ctx, 100); err != nil || sent != 1 {
		t.Fatalf("first tick: sent=%d err=%v, want 1", sent, err)
	}
	if sent, err := rsvc.ProcessDueReminders(ctx, 100); err != nil || sent != 0 {
		t.Fatalf("second tick: sent=%d err=%v, want 0 (dedup)", sent, err)
	}
	if c := mailer.count(); c != 1 {
		t.Fatalf("mailer calls = %d, want 1", c)
	}
	keys, _ := store.SentReminderKeys(ctx, 1, subID)
	if _, ok := keys["3"]; !ok {
		t.Fatalf("expected tier '3' recorded, got %v", keys)
	}
}

// TestReminder_ConcurrentReplicasClaimBeforeSend forces two reminder services to
// race the same subscription tier after both have observed no existing reminder.
// 判别性: 把 claim 移回 SendReminder 之后时, 两个副本都会先发邮件, mailer calls=2 -> 红。
func TestReminder_ConcurrentReplicasClaimBeforeSend(t *testing.T) {
	assignNow := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	var reminderNow time.Time
	mailer := &fakeMailer{outcome: ReminderSent}
	svc, _, store := reminderHarness(&assignNow, &reminderNow, mailer, nil)
	subID := assignSubExpiringIn(t, svc, store, &assignNow, &reminderNow, 1, 43, "u@example.com", 2*day)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	barrierStore := newSentKeyBarrierStore(store, 2)
	opts := []ReminderOption{WithReminderClock(func() time.Time { return reminderNow })}
	replicas := []*ReminderService{
		NewReminderService(barrierStore, mailer, opts...),
		NewReminderService(barrierStore, mailer, opts...),
	}
	results := make(chan reminderResult, len(replicas))
	for _, replica := range replicas {
		go func(rsvc *ReminderService) {
			sent, err := rsvc.ProcessDueReminders(ctx, 100)
			results <- reminderResult{sent: sent, err: err}
		}(replica)
	}

	totalSent := 0
	for range replicas {
		res := <-results
		if res.err != nil {
			t.Fatalf("concurrent reminder process err=%v", res.err)
		}
		totalSent += res.sent
	}
	if totalSent != 1 {
		t.Fatalf("sent total = %d, want 1 durable claim winner", totalSent)
	}
	if c := mailer.count(); c != 1 {
		t.Fatalf("mailer calls = %d, want 1 cross-replica send", c)
	}
	keys, _ := store.SentReminderKeys(context.Background(), 1, subID)
	if _, ok := keys["3"]; !ok {
		t.Fatalf("expected tier '3' recorded, got %v", keys)
	}
}

// TestReminder_DistinctTiersFireSeparately 进入不同档位各发一次。
func TestReminder_DistinctTiersFireSeparately(t *testing.T) {
	assignNow := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	var reminderNow time.Time
	mailer := &fakeMailer{outcome: ReminderSent}
	svc, rsvc, store := reminderHarness(&assignNow, &reminderNow, mailer, nil)
	subID := assignSubExpiringIn(t, svc, store, &assignNow, &reminderNow, 1, 7, "u@example.com", 5*day) // tier 7
	ctx := context.Background()

	expires := reminderNow.Add(5 * day) // 固定到期点

	rsvc.ProcessDueReminders(ctx, 100) // 发 tier 7
	reminderNow = expires.Add(-2 * day)
	rsvc.ProcessDueReminders(ctx, 100) // 发 tier 3
	reminderNow = expires.Add(-12 * time.Hour)
	rsvc.ProcessDueReminders(ctx, 100) // 发 tier 1

	keys, _ := store.SentReminderKeys(ctx, 1, subID)
	for _, want := range []string{"7", "3", "1"} {
		if _, ok := keys[want]; !ok {
			t.Fatalf("expected tier %q recorded, got %v", want, keys)
		}
	}
	if c := mailer.count(); c != 3 {
		t.Fatalf("mailer calls = %d, want 3 (one per tier)", c)
	}
}

// TestReminder_MissingRecipientSkippedNotSent 无邮箱 -> 记 skipped, 不发不重试。
// 判别性: 去掉空邮箱守卫, 会用空 To 调 mailer -> count>0 -> 红。
func TestReminder_MissingRecipientSkippedNotSent(t *testing.T) {
	assignNow := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	var reminderNow time.Time
	mailer := &fakeMailer{outcome: ReminderSent}
	svc, rsvc, store := reminderHarness(&assignNow, &reminderNow, mailer, nil)
	subID := assignSubExpiringIn(t, svc, store, &assignNow, &reminderNow, 1, 8, "", 2*day) // 无邮箱
	ctx := context.Background()

	if sent, err := rsvc.ProcessDueReminders(ctx, 100); err != nil || sent != 0 {
		t.Fatalf("tick: sent=%d err=%v, want 0", sent, err)
	}
	if c := mailer.count(); c != 0 {
		t.Fatalf("mailer calls = %d, want 0 (no recipient must not call mailer)", c)
	}
	keys, _ := store.SentReminderKeys(ctx, 1, subID)
	if _, ok := keys["3"]; !ok {
		t.Fatalf("expected skip recorded for tier '3' (dedup), got %v", keys)
	}
	// 第二次 tick 仍不发 (跳过记录已去重)。
	rsvc.ProcessDueReminders(ctx, 100)
	if c := mailer.count(); c != 0 {
		t.Fatalf("second tick mailer calls = %d, want 0", c)
	}
}

// TestReminder_UnconfiguredClaimedAtMostOnce 未配 SMTP 也保留 claim 并记录失败 tick, 后续不重发同档。
// 判别性: 若失败后删除/跳过 claim, 配好后第二 tick 会再发 -> mailer calls=2 -> 红。
func TestReminder_UnconfiguredClaimedAtMostOnce(t *testing.T) {
	assignNow := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	var reminderNow time.Time
	mailer := &fakeMailer{outcome: ReminderSkippedUnconfigured}
	svc, rsvc, store := reminderHarness(&assignNow, &reminderNow, mailer, nil)
	subID := assignSubExpiringIn(t, svc, store, &assignNow, &reminderNow, 1, 9, "u@example.com", 2*day)
	ctx := context.Background()

	if sent, err := rsvc.ProcessDueReminders(ctx, 100); sent != 0 || err == nil {
		t.Fatalf("unconfigured tick sent=%d err=%v, want sent=0 with failure error", sent, err)
	}
	keys, _ := store.SentReminderKeys(ctx, 1, subID)
	if _, ok := keys["3"]; !ok {
		t.Fatalf("unconfigured attempt must claim tier '3', got %v", keys)
	}
	if c := mailer.count(); c != 1 {
		t.Fatalf("unconfigured mailer calls=%d, want 1 initial attempt", c)
	}
	// SMTP 配好后仍不重发同档: claim 已经代表一次尝试。
	mailer.outcome = ReminderSent
	if sent, err := rsvc.ProcessDueReminders(ctx, 100); sent != 0 || err != nil {
		t.Fatalf("after configured sent=%d err=%v, want dedup skip", sent, err)
	}
	if c := mailer.count(); c != 1 {
		t.Fatalf("after configured mailer calls=%d, want still 1", c)
	}
}

// TestReminder_RetryableFailureClaimedAtMostOnce 发送失败保留 claim, 避免多副本/多 tick 重复轰炸。
// 判别性: 若失败后不保留 claim, provider 恢复后第二 tick 会再发 -> mailer calls=2 -> 红。
func TestReminder_RetryableFailureClaimedAtMostOnce(t *testing.T) {
	assignNow := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	var reminderNow time.Time
	mailer := &fakeMailer{outcome: ReminderRetry}
	svc, rsvc, store := reminderHarness(&assignNow, &reminderNow, mailer, nil)
	subID := assignSubExpiringIn(t, svc, store, &assignNow, &reminderNow, 1, 10, "u@example.com", 2*day)
	ctx := context.Background()

	// 第一次失败: claim 保留, err 让 worker failedTicks/日志可见。
	if sent, err := rsvc.ProcessDueReminders(ctx, 100); sent != 0 || err == nil {
		t.Fatalf("failing tick sent=%d err=%v, want sent=0 with failure error", sent, err)
	}
	keys, _ := store.SentReminderKeys(ctx, 1, subID)
	if _, ok := keys["3"]; !ok {
		t.Fatalf("retryable failure must claim tier '3', got %v", keys)
	}
	if c := mailer.count(); c != 1 {
		t.Fatalf("failing tick mailer calls=%d, want 1 initial attempt", c)
	}
	// provider 恢复后: at-most-once 跳过该档, 不重复提醒。
	mailer.outcome = ReminderSent
	if sent, err := rsvc.ProcessDueReminders(ctx, 100); sent != 0 || err != nil {
		t.Fatalf("after recovery sent=%d err=%v, want dedup skip", sent, err)
	}
	if c := mailer.count(); c != 1 {
		t.Fatalf("after recovery mailer calls=%d, want still 1", c)
	}
}

// TestReminder_NoStarvationPastFirstPage 窗口内订阅数 > 页大小且最早一页已记录时,
// 后面的订阅仍被处理 (游标翻页, 不以发送数当进度)。
// 判别性: 旧逻辑 (无游标 + n<batchSize 当翻页完) 会卡在已记录的最早一页 -> 后续订阅 sent=0 -> 红。
func TestReminder_NoStarvationPastFirstPage(t *testing.T) {
	assignNow := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	var reminderNow time.Time
	mailer := &fakeMailer{outcome: ReminderSent}
	svc, rsvc, store := reminderHarness(&assignNow, &reminderNow, mailer, nil)
	ctx := context.Background()

	// 5 个用户各一条 premium 订阅, 同到期 (都 tier 3); 不同 user 避免同组幂等。
	var ids []int64
	for u := int64(101); u <= 105; u++ {
		id := assignSubExpiringIn(t, svc, store, &assignNow, &reminderNow, 1, u, "u@example.com", 2*day)
		ids = append(ids, id)
	}
	// 全部同到期, 按 id 升序 = ListDueReminder 顺序; 预先把最小两个 id (最早一页) 标记已发。
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids[:2] {
		if _, err := store.RecordReminder(ctx, reminderRecord{TenantID: 1, SubscriptionID: id, ReminderKey: "3", Status: ReminderStatusSent, ExpiresAt: reminderNow.Add(2 * day)}); err != nil {
			t.Fatalf("pre-record: %v", err)
		}
	}

	// 页大小 2: 最早一页 (min1, min2) 已记录会被跳过; 必须翻页处理其余 3 条。
	sent, err := rsvc.ProcessDueReminders(ctx, 2)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if sent != 3 {
		t.Fatalf("sent = %d, want 3 (remaining subs past the already-recorded first page)", sent)
	}
	if mailer.count() != 3 {
		t.Fatalf("mailer calls = %d, want 3", mailer.count())
	}
}

// TestReminderWorker_TickOnceSends worker 同步 tick 发送并累计。
func TestReminderWorker_TickOnceSends(t *testing.T) {
	assignNow := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	var reminderNow time.Time
	mailer := &fakeMailer{outcome: ReminderSent}
	svc, rsvc, store := reminderHarness(&assignNow, &reminderNow, mailer, nil)
	assignSubExpiringIn(t, svc, store, &assignNow, &reminderNow, 1, 11, "u@example.com", 2*day)

	worker := NewReminderWorker(ReminderWorkerConfig{Service: rsvc, BatchSize: 10})
	worker.TickOnce(context.Background())
	if worker.SentTotal() != 1 {
		t.Fatalf("worker sent total = %d, want 1", worker.SentTotal())
	}
	if worker.TickCount() != 1 {
		t.Fatalf("worker tick count = %d, want 1", worker.TickCount())
	}
}
