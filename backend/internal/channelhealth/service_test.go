package channelhealth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time      { return c.now }
func (c *fixedClock) Add(d time.Duration) { c.now = c.now.Add(d) }

type fakeHealthCooldownSource struct {
	values map[int]time.Duration
	calls  []int
}

func (s *fakeHealthCooldownSource) CooldownForStatus(_ context.Context, status int) (time.Duration, error) {
	s.calls = append(s.calls, status)
	return s.values[status], nil
}

func testPolicy() Policy {
	p := DefaultPolicy()
	p.MinSampleCount = 3
	p.MinObservation = time.Millisecond
	p.ErrorRateThresholdPct = 50
	p.ErrorRateCooldown = time.Minute
	p.RateLimitHitRateThresholdPct = 50
	p.DefaultRateLimitCooldown = time.Minute
	p.Upstream5xxRateThresholdPct = 50
	p.Upstream5xxCooldown = time.Minute
	p.LatencyP99ThresholdMS = 100
	p.LatencyCooldown = time.Minute
	p.RampStageMinDuration = time.Millisecond
	p.RampStageMinSamples = 1
	p.RampErrorThresholdPct = 40
	p.RepeatedRampRollbackAlertThreshold = 2
	return p
}

func testKey() ChannelKey {
	return ChannelKey{
		TenantID: 7, Vendor: "openai", ProviderAccountID: 101,
		AccountCredentialID: 9001, CredentialVersion: 1,
	}
}

func TestChannelHealth_AT001_DefaultActiveSubject(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 5, 16, 8, 0, 0, 0, time.UTC)}
	svc := NewService(store, testPolicy(), clock)

	rec, err := svc.EnsureDefaultActive(ctx, testKey())
	if err != nil {
		t.Fatalf("EnsureDefaultActive: %v", err)
	}
	if rec.State != StateActive || rec.Key.StableChannelID() == "" {
		t.Fatalf("default record mismatch: %+v", rec)
	}
	if len(store.Audits()) != 0 {
		t.Fatalf("default active should not emit degradation audit")
	}
}

func TestCreditsExhaustedUsesServerResetOrFiveHourFallback(t *testing.T) {
	tests := []struct {
		name      string
		reset     time.Duration
		wantDelay time.Duration
	}{
		{name: "上游给出恢复时间", reset: 93 * time.Minute, wantDelay: 93 * time.Minute},
		{name: "上游未给恢复时间", wantDelay: 5 * time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := NewMemoryStore()
			clock := &fixedClock{now: time.Date(2026, 7, 23, 3, 0, 0, 0, time.UTC)}
			svc := NewService(store, testPolicy(), clock)
			signal := Signal{Key: testKey(), Class: SignalCreditsExhausted, StatusCode: 429}
			if tc.reset > 0 {
				resetAt := clock.now.Add(tc.reset)
				signal.RateLimitResetAt = &resetAt
			}
			record, err := svc.ApplySignal(ctx, signal)
			if err != nil {
				t.Fatalf("ApplySignal: %v", err)
			}
			if record.State != StateCoolingDown || record.ReasonClass != SignalCreditsExhausted || record.CooldownUntil == nil {
				t.Fatalf("record=%+v，期望额度耗尽立即进入冷却", record)
			}
			if got := record.CooldownUntil.Sub(clock.now); got != tc.wantDelay {
				t.Fatalf("cooldown=%s，期望 %s", got, tc.wantDelay)
			}
		})
	}
}

func TestChannelHealthLatestByProviderAccountMatchesPersistentOrdering(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	svc := NewService(store, testPolicy(), &fixedClock{now: time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)})
	base := testKey()
	olderHighVersion := Record{
		Key:       base,
		State:     StateManualPaused,
		UpdatedAt: time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC),
	}
	olderHighVersion.Key.CredentialVersion = 2
	newerLowVersion := Record{
		Key:       base,
		State:     StateActive,
		UpdatedAt: time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC),
	}
	newerLowVersion.Key.AccountCredentialID = 9002
	newerLowVersion.Key.CredentialVersion = 1
	if _, err := store.UpsertRecord(ctx, olderHighVersion); err != nil {
		t.Fatalf("写入高版本记录：%v", err)
	}
	if _, err := store.UpsertRecord(ctx, newerLowVersion); err != nil {
		t.Fatalf("写入低版本记录：%v", err)
	}

	rec, err := svc.LatestByProviderAccount(ctx, base.TenantID, base.ProviderAccountID)
	if err != nil {
		t.Fatalf("LatestByProviderAccount: %v", err)
	}
	if rec.Key.CredentialVersion != 2 || rec.State != StateManualPaused {
		t.Fatalf("latest=%+v，期望凭据版本优先选择 v2/manual_paused", rec)
	}
	if _, err := svc.LatestByProviderAccount(ctx, 0, base.ProviderAccountID); err == nil {
		t.Fatal("tenant_id=0 必须拒绝")
	}
	if _, err := svc.LatestByProviderAccount(ctx, base.TenantID, 0); err == nil {
		t.Fatal("provider_account_id=0 必须拒绝")
	}
}

func TestLatestByProviderAccountReturnsSelectorRecord(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	svc := NewService(store, testPolicy(), &fixedClock{now: time.Date(2026, 5, 16, 8, 0, 0, 0, time.UTC)})
	older := testKey()
	older.CredentialVersion = 1
	newer := older
	newer.CredentialVersion = 2
	if _, err := svc.EnsureDefaultActive(ctx, older); err != nil {
		t.Fatalf("写入旧版本：%v", err)
	}
	if _, err := svc.EnsureDefaultActive(ctx, newer); err != nil {
		t.Fatalf("写入新版本：%v", err)
	}

	got, err := svc.LatestByProviderAccount(ctx, newer.TenantID, newer.ProviderAccountID)
	if err != nil {
		t.Fatalf("LatestByProviderAccount：%v", err)
	}
	if got.Key.CredentialVersion != 2 || got.State != StateActive {
		t.Fatalf("最新 selector 记录不一致：%+v", got)
	}
	if _, err := svc.LatestByProviderAccount(ctx, 0, newer.ProviderAccountID); err == nil {
		t.Fatal("tenant_id=0 必须拒绝")
	}
	if _, err := svc.LatestByProviderAccount(ctx, newer.TenantID, 0); err == nil {
		t.Fatal("provider_account_id=0 必须拒绝")
	}
}

func TestChannelHealth_AT002_ErrorRateCooldownAndAudit(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError}); err != nil {
			t.Fatalf("ApplySignal: %v", err)
		}
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateCoolingDown || rec.CooldownUntil == nil {
		t.Fatalf("state=%s cooldown=%v want cooling_down with cooldown", rec.State, rec.CooldownUntil)
	}
	events := auditTypes(store.Audits())
	if !hasAudit(events, EventDegraded) || !hasAudit(events, EventDisabled) {
		t.Fatalf("events=%v want degraded and disabled", events)
	}
}

func TestChannelHealth_AT003_Upstream5xxDegradesBeforeCooldownAndLocal5xxIgnored(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalLocalGateway5xx}); err != nil {
			t.Fatalf("local ApplySignal: %v", err)
		}
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateActive {
		t.Fatalf("local gateway 5xx changed state to %s", rec.State)
	}
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalUpstream5xx}); err != nil {
			t.Fatalf("upstream ApplySignal: %v", err)
		}
	}
	rec, _ = store.Get(ctx, key)
	if rec.State != StateDegraded || rec.ReasonClass != SignalUpstream5xx {
		t.Fatalf("first upstream breach = %+v, want degraded/upstream_5xx", rec)
	}
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalUpstream5xx}); err != nil {
			t.Fatalf("second upstream ApplySignal: %v", err)
		}
	}
	rec, _ = store.Get(ctx, key)
	if rec.State != StateCoolingDown {
		t.Fatalf("repeated upstream breach state=%s want cooling_down", rec.State)
	}
}

func TestChannelHealth_AT004_RateLimitCooldownUsesResetTimestamp(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	reset := clock.Now().Add(17 * time.Minute)
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalRateLimit, RateLimitResetAt: &reset}); err != nil {
			t.Fatalf("ApplySignal: %v", err)
		}
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateCoolingDown || rec.CooldownUntil == nil || !rec.CooldownUntil.Equal(reset) {
		t.Fatalf("cooldown_until=%v want reset %v", rec.CooldownUntil, reset)
	}
}

func TestChannelHealthRateLimitReadsRuntimeCooldownForNewEvent(t *testing.T) {
	// 变异：删掉运行时设置源后会回到 testPolicy 的 1 分钟，精确的 37 秒截止时间立即变红。
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)}
	source := &fakeHealthCooldownSource{values: map[int]time.Duration{429: 37 * time.Second}}
	svc := NewService(store, testPolicy(), clock, WithCooldownSource(source))
	key := testKey()
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalRateLimit, StatusCode: 429}); err != nil {
			t.Fatalf("ApplySignal: %v", err)
		}
	}
	rec, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := clock.Now().Add(37 * time.Second)
	if rec.CooldownUntil == nil || !rec.CooldownUntil.Equal(want) {
		t.Fatalf("cooldown_until=%v, want %s", rec.CooldownUntil, want)
	}
	if len(source.calls) != 1 || source.calls[0] != 429 {
		t.Fatalf("设置源调用=%v, want [429]", source.calls)
	}
}

func TestChannelHealth529ReadsOverloadCooldownOnlyForNewEvent(t *testing.T) {
	// 变异：把 529 继续当普通 5xx 使用 1 分钟，会使 43 秒截止时间断言变红。
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)}
	source := &fakeHealthCooldownSource{values: map[int]time.Duration{529: 43 * time.Second}}
	svc := NewService(store, testPolicy(), clock, WithCooldownSource(source))
	key := testKey()
	var rec Record
	for i := 0; i < 8; i++ {
		clock.Add(time.Millisecond)
		var err error
		rec, err = svc.ApplySignal(ctx, Signal{Key: key, Class: SignalUpstream5xx, StatusCode: 529})
		if err != nil {
			t.Fatalf("ApplySignal: %v", err)
		}
		if rec.State == StateCoolingDown {
			break
		}
	}
	want := clock.Now().Add(43 * time.Second)
	if rec.State != StateCoolingDown || rec.CooldownUntil == nil || !rec.CooldownUntil.Equal(want) {
		t.Fatalf("state=%s cooldown_until=%v, want cooling_down/%s", rec.State, rec.CooldownUntil, want)
	}
	if len(source.calls) != 1 || source.calls[0] != 529 {
		t.Fatalf("设置源调用=%v, want [529]", source.calls)
	}
}

func TestChannelHealthRuntimeCooldownUnconfiguredKeepsPolicyDefault(t *testing.T) {
	// 防翻转守卫：不注入设置源时仍严格使用接线前 Policy.DefaultRateLimitCooldown。
	ctx, svc, store, clock := testService()
	key := testKey()
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalRateLimit, StatusCode: 429}); err != nil {
			t.Fatalf("ApplySignal: %v", err)
		}
	}
	rec, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := clock.Now().Add(testPolicy().DefaultRateLimitCooldown)
	if rec.CooldownUntil == nil || !rec.CooldownUntil.Equal(want) {
		t.Fatalf("cooldown_until=%v, want pre-wiring default %s", rec.CooldownUntil, want)
	}
}

func TestChannelHealthCooldownChangeDoesNotRewriteStoredDeadline(t *testing.T) {
	// 变异：若设置更新会回写已有记录，旧账号的截止时间会从 17 秒漂到 29 秒；
	// 若服务只在构造时读一次，新账号又不会拿到 29 秒。两个方向由同一用例同时咬住。
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 7, 14, 11, 0, 0, 0, time.UTC)}
	source := &fakeHealthCooldownSource{values: map[int]time.Duration{429: 17 * time.Second}}
	svc := NewService(store, testPolicy(), clock, WithCooldownSource(source))
	applyThree := func(key ChannelKey) Record {
		t.Helper()
		var rec Record
		for i := 0; i < 3; i++ {
			clock.Add(time.Millisecond)
			var err error
			rec, err = svc.ApplySignal(ctx, Signal{Key: key, Class: SignalRateLimit, StatusCode: 429})
			if err != nil {
				t.Fatalf("ApplySignal: %v", err)
			}
		}
		return rec
	}

	oldKey := testKey()
	oldRec := applyThree(oldKey)
	if oldRec.CooldownUntil == nil {
		t.Fatal("旧记录缺少 cooldown_until")
	}
	oldDeadline := *oldRec.CooldownUntil

	source.values[429] = 29 * time.Second
	newKey := oldKey
	newKey.ProviderAccountID++
	newKey.AccountCredentialID++
	newRec := applyThree(newKey)
	if newRec.CooldownUntil == nil || !newRec.CooldownUntil.Equal(clock.Now().Add(29*time.Second)) {
		t.Fatalf("新记录 cooldown_until=%v, want %s", newRec.CooldownUntil, clock.Now().Add(29*time.Second))
	}
	oldAfter, err := store.Get(ctx, oldKey)
	if err != nil {
		t.Fatalf("Get old: %v", err)
	}
	if oldAfter.CooldownUntil == nil || !oldAfter.CooldownUntil.Equal(oldDeadline) {
		t.Fatalf("存量 deadline 从 %s 漂到 %v", oldDeadline, oldAfter.CooldownUntil)
	}
}

func TestChannelHealth_ForceCooldownBypassesRateLimitSampleFloor(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	until := clock.Now().Add(time.Hour)

	rec, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalRateLimit, RateLimitResetAt: &until})
	if err != nil {
		t.Fatalf("ApplySignal: %v", err)
	}
	if rec.State != StateActive {
		t.Fatalf("single rate-limit signal state=%s want active before ForceCooldown", rec.State)
	}

	rec, err = svc.ForceCooldown(ctx, key, until, "rate_limit_rpm")
	if err != nil {
		t.Fatalf("ForceCooldown: %v", err)
	}
	if rec.State != StateCoolingDown {
		t.Fatalf("ForceCooldown state=%s want cooling_down", rec.State)
	}
	if rec.CooldownUntil == nil || !rec.CooldownUntil.Equal(until.UTC()) {
		t.Fatalf("CooldownUntil=%v want %s", rec.CooldownUntil, until.UTC())
	}
	if rec.ReasonClass != SignalClass("rate_limit_rpm") {
		t.Fatalf("ReasonClass=%s want rate_limit_rpm", rec.ReasonClass)
	}
	if !hasAudit(auditTypes(store.Audits()), EventDisabled) {
		t.Fatalf("ForceCooldown audit missing EventDisabled: %+v", store.Audits())
	}
}

func TestChannelHealth_AT005_BanSignalDisablesAndAlertsWithoutRawText(t *testing.T) {
	ctx, svc, store, _ := testService()
	key := testKey()
	_, err := svc.ApplySignal(ctx, Signal{
		Key: key, Class: SignalTokenRevoked,
		RawUpstreamText: "token hk_live_secret should never be stored",
	})
	if err != nil {
		t.Fatalf("ApplySignal: %v", err)
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateDisabled || rec.CooldownUntil == nil {
		t.Fatalf("ban state=%s cooldown=%v", rec.State, rec.CooldownUntil)
	}
	if len(store.Alerts()) != 1 || store.Alerts()[0].Type != AlertBanSignal {
		t.Fatalf("alerts=%+v want ban alert", store.Alerts())
	}
	for _, ev := range store.Audits() {
		raw, _ := json.Marshal(ev)
		if strings.Contains(string(raw), "hk_live_secret") || strings.Contains(string(raw), "should never be stored") {
			t.Fatalf("audit leaked raw upstream text: %s", raw)
		}
	}
}

func TestChannelHealth_AT007_AT008_RampRecoveryAndRollback(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	rec, _ := svc.EnsureDefaultActive(ctx, key)
	cooldownUntil := clock.Now().Add(-time.Second)
	rec.State = StateCoolingDown
	rec.CooldownUntil = &cooldownUntil
	rec, _ = store.UpsertRecord(ctx, rec)

	rec, err := svc.MaybeStartRamp(ctx, key)
	if err != nil {
		t.Fatalf("MaybeStartRamp: %v", err)
	}
	if rec.State != StateRamping || rec.RampStagePct != 1 {
		t.Fatalf("ramp start=%+v want 1%%", rec)
	}
	for _, want := range []int{10, 50, 100, 0} {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalSuccess}); err != nil {
			t.Fatalf("clean signal: %v", err)
		}
		rec, err = svc.AdvanceRamp(ctx, key)
		if err != nil {
			t.Fatalf("AdvanceRamp: %v", err)
		}
		if want == 0 {
			if rec.State != StateActive {
				t.Fatalf("final ramp state=%s want active", rec.State)
			}
		} else if rec.RampStagePct != want {
			t.Fatalf("ramp stage=%d want %d", rec.RampStagePct, want)
		}
	}

	rec.State = StateRamping
	rec.RampStagePct = 10
	start := clock.Now().Add(-time.Second)
	rec.RampStartedAt = &start
	rec.SampleWindow = WindowSummary{}
	rec.RampFailureCount = 1
	rec, _ = store.UpsertRecord(ctx, rec)
	clock.Add(time.Millisecond)
	_, _ = svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError})
	rec, err = svc.AdvanceRamp(ctx, key)
	if err != nil {
		t.Fatalf("AdvanceRamp rollback: %v", err)
	}
	if rec.State != StateCoolingDown || rec.RampFailureCount != 2 {
		t.Fatalf("rollback rec=%+v", rec)
	}
	if got := store.Alerts(); len(got) == 0 || got[len(got)-1].Type != AlertRepeatedRampRollback {
		t.Fatalf("expected repeated rollback alert, got %+v", got)
	}
}

func TestChannelHealth_RampAdvancesAfterMaxDurationWithoutTraffic(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)}
	policy := DefaultPolicy()
	policy.RampStageMinDuration = time.Minute
	policy.RampStageMaxDuration = 10 * time.Minute
	policy.RampStageMinSamples = 3
	svc := NewService(store, policy, clock)
	key := testKey()
	rec, _ := svc.EnsureDefaultActive(ctx, key)
	rec.State = StateRamping
	rec.RampStagePct = 1
	started := clock.Now()
	rec.RampStartedAt = &started
	rec, _ = store.UpsertRecord(ctx, rec)

	clock.Add(9 * time.Minute)
	got, err := svc.AdvanceRamp(ctx, key)
	if err != nil || got.RampStagePct != 1 {
		t.Fatalf("max duration 前不应推进: state=%s stage=%d err=%v", got.State, got.RampStagePct, err)
	}
	clock.Add(time.Minute)
	got, err = svc.AdvanceRamp(ctx, key)
	if err != nil || got.State != StateRamping || got.RampStagePct != 10 {
		t.Fatalf("低流量最长观察期后应推进: state=%s stage=%d err=%v", got.State, got.RampStagePct, err)
	}
}

func TestChannelHealth_RampLowSampleFailureRollsBackAfterMaxDuration(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)}
	policy := DefaultPolicy()
	policy.MinSampleCount = 10
	policy.MinObservation = time.Hour
	policy.RampStageMinDuration = time.Minute
	policy.RampStageMaxDuration = 10 * time.Minute
	policy.RampStageMinSamples = 3
	svc := NewService(store, policy, clock)
	key := testKey()
	rec, _ := svc.EnsureDefaultActive(ctx, key)
	rec.State = StateRamping
	rec.RampStagePct = 1
	started := clock.Now()
	rec.RampStartedAt = &started
	rec, _ = store.UpsertRecord(ctx, rec)
	if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError, At: clock.Now()}); err != nil {
		t.Fatalf("写入失败样本: %v", err)
	}

	clock.Add(10 * time.Minute)
	got, err := svc.AdvanceRamp(ctx, key)
	if err != nil || got.State != StateCoolingDown {
		t.Fatalf("少量失败样本不得被超时恢复绕过: state=%s stage=%d err=%v", got.State, got.RampStagePct, err)
	}
}

func TestChannelHealth_AT009_IsolatesVendorAndCredentialVersion(t *testing.T) {
	ctx, svc, store, _ := testService()
	key := testKey()
	otherVersion := key
	otherVersion.CredentialVersion = 2
	otherVendor := key
	otherVendor.Vendor = "anthropic"

	for i := 0; i < 3; i++ {
		_, _ = svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError})
	}
	for _, untouched := range []ChannelKey{otherVersion, otherVendor} {
		rec, err := svc.EnsureDefaultActive(ctx, untouched)
		if err != nil {
			t.Fatalf("ensure untouched: %v", err)
		}
		if rec.State != StateActive {
			t.Fatalf("untouched %+v state=%s", untouched, rec.State)
		}
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateCoolingDown {
		t.Fatalf("target state=%s want cooling_down", rec.State)
	}
}

func TestChannelHealth_AT010_ManualOverridesAudit(t *testing.T) {
	ctx, svc, store, _ := testService()
	key := testKey()
	if _, err := svc.ManualPause(ctx, key, "11", "ops pause"); err != nil {
		t.Fatalf("ManualPause: %v", err)
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateManualPaused || rec.ManualPauseReason != "ops pause" {
		t.Fatalf("pause rec=%+v", rec)
	}
	if _, err := svc.ManualResume(ctx, key, "11", "resume ramp"); err != nil {
		t.Fatalf("ManualResume: %v", err)
	}
	if _, err := svc.ForceActive(ctx, key, "11", "break glass"); err != nil {
		t.Fatalf("ForceActive: %v", err)
	}
	if !hasAudit(auditTypes(store.Audits()), EventManualOverride) {
		t.Fatalf("manual override audit missing: %+v", store.Audits())
	}
	if got := store.Alerts(); len(got) == 0 || got[len(got)-1].Type != AlertManualForceActive {
		t.Fatalf("force-active alert missing: %+v", got)
	}
	if _, err := svc.ForceActive(ctx, key, "11", ""); err == nil {
		t.Fatalf("force-active without reason should fail")
	}
}

func TestChannelHealth_AT013_LatencyCooldownThenRecoveryWithoutDisable(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		_, _ = svc.ApplySignal(ctx, Signal{Key: key, Class: SignalSuccess, LatencyMS: 500})
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateDegraded || rec.ReasonClass != SignalLatencyP99 {
		t.Fatalf("latency first state=%s reason=%s", rec.State, rec.ReasonClass)
	}
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		_, _ = svc.ApplySignal(ctx, Signal{Key: key, Class: SignalSuccess, LatencyMS: 600})
	}
	rec, _ = store.Get(ctx, key)
	if rec.State != StateCoolingDown || rec.ReasonClass != SignalLatencyP99 {
		t.Fatalf("latency repeated rec=%+v", rec)
	}
	if rec.State == StateDisabled {
		t.Fatalf("latency-only path must not disable")
	}
	expired := clock.Now().Add(-time.Second)
	rec.CooldownUntil = &expired
	rec.SampleWindow = WindowSummary{}
	rec, _ = store.UpsertRecord(ctx, rec)
	rec, _ = svc.MaybeStartRamp(ctx, key)
	if rec.State != StateRamping {
		t.Fatalf("latency recovery state=%s want ramping", rec.State)
	}
}

func TestChannelHealthSummaryMemory_CountsByStateTenantScopeAndOldestCooldown(t *testing.T) {
	// 变异:把每一行都计到同一个 state 下、忽略零计数的 state、或纳入另一个租户;精确计数与 total 都会变红。
	ctx := context.Background()
	store := NewMemoryStore()
	svc := NewService(store, testPolicy(), &fixedClock{now: time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)})
	olderCooldown := time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)
	newerCooldown := time.Date(2026, 6, 6, 13, 30, 0, 0, time.UTC)
	upsertSummaryRecord(t, store, 7, "openai", 101, 9001, StateActive, nil)
	upsertSummaryRecord(t, store, 7, "anthropic", 102, 9002, StateActive, nil)
	upsertSummaryRecord(t, store, 7, "gemini", 103, 9003, StateCoolingDown, &newerCooldown)
	upsertSummaryRecord(t, store, 7, "openai", 104, 9004, StateDisabled, &olderCooldown)
	upsertSummaryRecord(t, store, 8, "openai", 201, 9901, StateManualPaused, nil)

	summary, err := svc.SummarizeChannelHealth(ctx, 7)
	if err != nil {
		t.Fatalf("SummarizeChannelHealth: %v", err)
	}
	if summary.Total != 4 {
		t.Fatalf("total=%d want 4", summary.Total)
	}
	want := map[HealthState]int64{
		StateActive:       2,
		StateDegraded:     0,
		StateCoolingDown:  1,
		StateRamping:      0,
		StateDisabled:     1,
		StateManualPaused: 0,
	}
	for state, count := range want {
		if summary.ByState[state] != count {
			t.Fatalf("by_state[%s]=%d want %d; all=%+v", state, summary.ByState[state], count, summary.ByState)
		}
	}
	if summary.OldestCooldownAt == nil || !summary.OldestCooldownAt.Equal(olderCooldown) {
		t.Fatalf("oldest=%v want %s", summary.OldestCooldownAt, olderCooldown.Format(time.RFC3339))
	}
}

func TestChannelHealth_AuditFailureRollsBackStateMutation(t *testing.T) {
	ctx := context.Background()
	store := &failingAuditStore{MemoryStore: NewMemoryStore()}
	clock := &fixedClock{now: time.Date(2026, 5, 16, 8, 0, 0, 0, time.UTC)}
	svc := NewService(store, testPolicy(), clock)
	key := testKey()
	if _, err := svc.EnsureDefaultActive(ctx, key); err != nil {
		t.Fatalf("EnsureDefaultActive: %v", err)
	}
	for i := 0; i < 2; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError}); err != nil {
			t.Fatalf("warmup ApplySignal: %v", err)
		}
	}
	before, _ := store.Get(ctx, key)
	store.failAudit = true
	clock.Add(time.Millisecond)
	if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError}); err == nil {
		t.Fatal("ApplySignal should fail when audit append fails")
	}
	after, _ := store.Get(ctx, key)
	if after.State != before.State || after.SampleWindow.TotalAttempts != before.SampleWindow.TotalAttempts {
		t.Fatalf("state/window changed despite rollback: before=%+v after=%+v", before, after)
	}
	if len(store.Audits()) != 0 {
		t.Fatalf("audit half-write remained after rollback: %+v", store.Audits())
	}
}

func testService() (context.Context, *Service, *MemoryStore, *fixedClock) {
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 5, 16, 8, 0, 0, 0, time.UTC)}
	return ctx, NewService(store, testPolicy(), clock), store, clock
}

func upsertSummaryRecord(t *testing.T, store *MemoryStore, tenantID int64, vendor string, providerAccountID, credentialID int64, state HealthState, cooldownUntil *time.Time) {
	t.Helper()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	_, err := store.UpsertRecord(context.Background(), Record{
		Key: ChannelKey{
			TenantID:            tenantID,
			Vendor:              vendor,
			ProviderAccountID:   providerAccountID,
			AccountCredentialID: credentialID,
			CredentialVersion:   1,
		},
		State:            state,
		Score:            100,
		ReasonClass:      SignalNone,
		Confidence:       ConfidenceObserved,
		CooldownUntil:    cooldownUntil,
		PolicyVersion:    "channel-health-v1",
		StateEnteredAt:   now.Add(-time.Minute),
		LastTransitionAt: now.Add(-time.Minute),
		CreatedAt:        now.Add(-time.Hour),
		UpdatedAt:        now,
	})
	if err != nil {
		t.Fatalf("upsert summary record: %v", err)
	}
}

func auditTypes(events []AuditEvent) []AuditEventType {
	out := make([]AuditEventType, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Type)
	}
	return out
}

func hasAudit(events []AuditEventType, want AuditEventType) bool {
	for _, got := range events {
		if got == want {
			return true
		}
	}
	return false
}

type failingAuditStore struct {
	*MemoryStore
	failAudit bool
}

func (s *failingAuditStore) AppendAudit(ctx context.Context, ev AuditEvent) error {
	if s.failAudit {
		return errors.New("simulated audit failure")
	}
	return s.MemoryStore.AppendAudit(ctx, ev)
}

func (s *failingAuditStore) WithTx(ctx context.Context, fn func(Store) error) error {
	s.mu.RLock()
	records := make(map[string]Record, len(s.records))
	for k, v := range s.records {
		records[k] = v
	}
	audits := append([]AuditEvent(nil), s.audits...)
	alerts := append([]Alert(nil), s.alerts...)
	s.mu.RUnlock()
	if err := fn(s); err != nil {
		s.mu.Lock()
		s.records = records
		s.audits = audits
		s.alerts = alerts
		s.mu.Unlock()
		return err
	}
	_ = ctx
	return nil
}

// TestChannelHealth_RampRollbackExponentialBackoffAndReset 守卫两半:
// (1) 连续 ramp 回滚的 cooldown 随 RampFailureCount 指数升级(非固定系数);
// (2) ramp 完全恢复到 StateActive 时 RampFailureCount 清零。
// 变异守卫:rollbackRamp 改回固定 d*factor → 升级断言变红;default 分支去掉
// RampFailureCount=0 → 重置断言变红。
func TestChannelHealth_RampRollbackExponentialBackoffAndReset(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()

	// 给定回滚前 RampFailureCount,驱动一次 ramp 回滚,返回 cooldown 时长。
	rollbackCooldown := func(failBefore int) time.Duration {
		rec, _ := svc.EnsureDefaultActive(ctx, key)
		rec.State = StateRamping
		rec.RampStagePct = 10
		start := clock.Now().Add(-time.Second)
		rec.RampStartedAt = &start
		rec.SampleWindow = WindowSummary{}
		rec.RampFailureCount = failBefore
		rec.CooldownUntil = nil
		rec, _ = store.UpsertRecord(ctx, rec)
		clock.Add(time.Millisecond)
		_, _ = svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError})
		rec, err := svc.AdvanceRamp(ctx, key)
		if err != nil {
			t.Fatalf("AdvanceRamp rollback: %v", err)
		}
		if rec.State != StateCoolingDown || rec.CooldownUntil == nil {
			t.Fatalf("expected cooling down with cooldown; rec=%+v", rec)
		}
		return rec.CooldownUntil.Sub(clock.Now())
	}

	first := rollbackCooldown(0) // → RampFailureCount 1 → d*factor^1
	third := rollbackCooldown(3) // → RampFailureCount 4 → d*factor^4 (capped)
	if !(third > first) {
		t.Fatalf("连续回滚 cooldown 应指数升级: first(count1)=%s not < later(count4)=%s", first, third)
	}

	// 完全恢复清零 streak:从 ramping 一路推进到 StateActive,断言 RampFailureCount=0。
	rec, _ := svc.EnsureDefaultActive(ctx, key)
	rec.State = StateRamping
	rec.RampStagePct = 50
	rstart := clock.Now().Add(-time.Second)
	rec.RampStartedAt = &rstart
	rec.RampFailureCount = 4
	rec.SampleWindow = WindowSummary{}
	rec, _ = store.UpsertRecord(ctx, rec)
	for {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalSuccess}); err != nil {
			t.Fatalf("clean signal: %v", err)
		}
		rec, _ = svc.AdvanceRamp(ctx, key)
		if rec.State == StateActive {
			break
		}
		if rec.State != StateRamping {
			t.Fatalf("unexpected state during recovery: %s", rec.State)
		}
	}
	if rec.RampFailureCount != 0 {
		t.Fatalf("ramp 完全恢复后 RampFailureCount 应清零, got %d", rec.RampFailureCount)
	}
}
