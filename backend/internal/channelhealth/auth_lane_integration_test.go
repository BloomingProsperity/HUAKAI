package channelhealth

import (
	"context"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

// authLaneTestSetup 建一个接了真 authcooldown.Store 车道的 channelhealth Service + 内存健康 store。
func authLaneTestSetup(t *testing.T) (context.Context, *Service, *MemoryStore, *authcooldown.Store, *fixedClock, ChannelKey) {
	t.Helper()
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)}
	lane := authcooldown.NewStore(authcooldown.Config{Base: 30 * time.Second, Cap: 30 * time.Minute, HardDisableStrikeK: 3})
	svc := NewService(store, testPolicy(), clock, WithAuthCooldownLane(lane))
	return ctx, svc, store, lane, clock, testKey()
}

func authSnapshot(key ChannelKey) *pool.AccountSnapshot {
	return &pool.AccountSnapshot{ID: key.ProviderAccountID, TenantID: key.TenantID}
}

func authReq(key ChannelKey) pool.SelectionRequest {
	return pool.SelectionRequest{TenantID: key.TenantID, RequestedModel: "gpt-test"}
}

// TestAuthLane_ChallengeSuspendsWithoutHealthChange:auth 失败信号(SignalAuthChallenge)必须
// ①把账号移出选号(PoolGate=false, 原因 auth_cooldown);②完全不改健康记录(State/Score/cooldown 不动)。
// 判别:去掉 applySignal 的 Suspend 调用 → PoolGate 放行 → ①断言红;若把 auth 信号误走健康 FSM
// → 健康记录被改 → ②断言红。这正是「auth blip 不污染健康分 + 补上临时排除」的双重不变量。
func TestAuthLane_ChallengeSuspendsWithoutHealthChange(t *testing.T) {
	ctx, svc, store, _, clock, key := authLaneTestSetup(t)
	rec, err := svc.EnsureDefaultActive(ctx, key)
	if err != nil || rec.State != StateActive || rec.Score != 100 {
		t.Fatalf("前置:应 active/score100,实得 state=%s score=%v err=%v", rec.State, rec.Score, err)
	}
	if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalAuthChallenge, AuthFailureClass: authcooldown.ClassAmbiguous}); err != nil {
		t.Fatalf("ApplySignal: %v", err)
	}
	// ② 健康记录必须原样不变。
	after, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.State != StateActive || after.CooldownUntil != nil || after.Score != 100 || after.SampleWindow.TotalAttempts != 0 {
		t.Fatalf("auth blip 污染健康分:state=%s cooldown=%v score=%v attempts=%d",
			after.State, after.CooldownUntil, after.Score, after.SampleWindow.TotalAttempts)
	}
	// ① PoolGate 必须把账号移出选号,原因=auth_cooldown(与 GateFailureHealth 区分)。
	gate := NewServicePoolGate(svc, clock)
	ok, why, err := gate.Allow(ctx, authSnapshot(key), authReq(key))
	if err != nil {
		t.Fatalf("Allow err: %v", err)
	}
	if ok {
		t.Fatal("auth 失败后账号应被 PoolGate 移出选号")
	}
	if why != pool.GateFailureAuthCooldown {
		t.Fatalf("gate 失败原因=%s, 期望 auth_cooldown", why)
	}
}

// TestAuthLane_SuccessClears:一次成功请求(SignalSuccess)即时清 auth 车道(等价 CLIProxy self-heal)。
// 判别:去掉 applySignal 成功分支的 Clear → 成功后仍被排除 → 断言红。
func TestAuthLane_SuccessClears(t *testing.T) {
	ctx, svc, _, _, clock, key := authLaneTestSetup(t)
	gate := NewServicePoolGate(svc, clock)
	if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalAuthChallenge, AuthFailureClass: authcooldown.ClassAmbiguous}); err != nil {
		t.Fatalf("ApplySignal: %v", err)
	}
	if ok, _, _ := gate.Allow(ctx, authSnapshot(key), authReq(key)); ok {
		t.Fatal("前置:auth 失败后应被排除")
	}
	if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalSuccess}); err != nil {
		t.Fatalf("ApplySignal success: %v", err)
	}
	ok, why, err := gate.Allow(ctx, authSnapshot(key), authReq(key))
	if err != nil || !ok {
		t.Fatalf("一次成功应解除 auth 冷却:ok=%v why=%s err=%v", ok, why, err)
	}
}

// TestAuthLane_DisableCoolingExemptsSoftNotHard(修正5):高价值号逃生阀只豁免软退避,不豁免 HardDisabled。
// 判别:若逃生阀也豁免 HardDisabled(去掉 !hardDisabled 条件),第二段断言(硬禁仍被排除)转红——
// 这正是「不给 revoked 号重开黑洞」的护栏。
func TestAuthLane_DisableCoolingExemptsSoftNotHard(t *testing.T) {
	ctx, svc, _, lane, clock, key := authLaneTestSetup(t)
	gate := NewServicePoolGate(svc, clock)
	snap := authSnapshot(key)
	snap.DisableCooling = true

	// 软退避 + DisableCooling → 豁免放行。
	if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalAuthChallenge, AuthFailureClass: authcooldown.ClassAmbiguous}); err != nil {
		t.Fatalf("ApplySignal: %v", err)
	}
	if ok, why, _ := gate.Allow(ctx, snap, authReq(key)); !ok {
		t.Fatalf("DisableCooling 应豁免软退避,却被拒 why=%s", why)
	}
	// 硬禁 + DisableCooling → 不豁免(仍排除)。
	lane.OnRefreshResult(ctx, key.ProviderAccountID, false, true)
	if ok, _, _ := gate.Allow(ctx, snap, authReq(key)); ok {
		t.Fatal("DisableCooling 不应豁免 HardDisabled(否则给 revoked 号重开黑洞)")
	}
}

// TestAuthLane_OperatorResumeClears(§17 修正2,跨模块):运营 ForceActive/ManualResume 必须清 auth 车道
// (含 HardDisabled),否则被硬禁的死号运营者救不回。判别:去掉 manualTransitionLocked 的 Clear → 断言红。
func TestAuthLane_OperatorResumeClears(t *testing.T) {
	ctx, svc, _, lane, clock, key := authLaneTestSetup(t)
	gate := NewServicePoolGate(svc, clock)

	// 死号:热刷新证实 invalid_grant → HardDisabled。
	lane.OnRefreshResult(ctx, key.ProviderAccountID, false, true)
	if ok, _, _ := gate.Allow(ctx, authSnapshot(key), authReq(key)); ok {
		t.Fatal("前置:HardDisabled 死号应被排除")
	}

	// ForceActive → 走 Active 态,必须清车道(含 HardDisabled),之后账号可再被选。
	if _, err := svc.ForceActive(ctx, key, "operator-1", "manual recovery"); err != nil {
		t.Fatalf("ForceActive: %v", err)
	}
	if ok, why, err := gate.Allow(ctx, authSnapshot(key), authReq(key)); err != nil || !ok {
		t.Fatalf("ForceActive 后死号应可再选:ok=%v why=%s err=%v", ok, why, err)
	}

	// ManualResume 同样清车道(经车道直查,避开 ramping 概率准入的干扰)。
	lane.OnRefreshResult(ctx, key.ProviderAccountID, false, true)
	if _, err := svc.ManualResume(ctx, key, "operator-1", "manual recovery"); err != nil {
		t.Fatalf("ManualResume: %v", err)
	}
	if ok, _ := lane.Eligible(key.ProviderAccountID, clock.Now()); !ok {
		t.Fatal("ManualResume 应清 auth 车道(账号在车道内恢复合格)")
	}
}

// TestAuthLane_NotWiredIsNoop:未接线车道(nil lane)时 SignalAuthChallenge 变纯 no-op,PoolGate 照旧放行——
// 证 kill-switch 关闭态逐字节保持既有行为(缺口修复前的默认)。
func TestAuthLane_NotWiredIsNoop(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)}
	svc := NewService(store, testPolicy(), clock) // 不接 auth 车道
	key := testKey()
	if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalAuthChallenge, AuthFailureClass: authcooldown.ClassIronClad}); err != nil {
		t.Fatalf("ApplySignal: %v", err)
	}
	gate := NewServicePoolGate(svc, clock)
	if ok, _, err := gate.Allow(ctx, authSnapshot(key), authReq(key)); err != nil || !ok {
		t.Fatalf("车道未接线时应照旧放行(no-op):ok=%v err=%v", ok, err)
	}
}
