package channelhealth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

type staticAccounts []*pool.AccountSnapshot

func (s staticAccounts) ListAccounts(context.Context, pool.SelectionRequest) ([]*pool.AccountSnapshot, error) {
	return []*pool.AccountSnapshot(s), nil
}

type acceptingSlots struct{}

func (acceptingSlots) Acquire(context.Context, *pool.AccountSnapshot, pool.SelectionRequest) (*pool.AcquireResult, error) {
	return &pool.AcquireResult{AcquisitionToken: uuid.New()}, nil
}

func TestChannelHealth_AT006_AT012_PoolGateSkipsCooledAndSurfacesAllUnhealthy(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	gate := NewPoolGate(store, nil)
	gates := pool.DefaultGateChain()
	gates.Health = gate
	sel := pool.NewDefaultSelector(staticAccounts{
		{ID: 101, TenantID: 7, Priority: 1, MaxConcurrency: 1},
		{ID: 202, TenantID: 7, Priority: 2, MaxConcurrency: 1},
	}, pool.WithGateChain(gates), pool.WithSlotManager(acceptingSlots{}))

	key1 := testKey()
	key1.ProviderAccountID = 101
	key2 := testKey()
	key2.ProviderAccountID = 202
	key2.AccountCredentialID = 9002
	svc := NewService(store, testPolicy(), nil)
	rec1, _ := svc.EnsureDefaultActive(ctx, key1)
	rec1.State = StateCoolingDown
	_, _ = store.UpsertRecord(ctx, rec1)
	_, _ = svc.EnsureDefaultActive(ctx, key2)

	res, err := sel.Select(ctx, pool.SelectionRequest{TenantID: 7, PoolGroupID: 1, RequestedModel: "gpt-test"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID != 202 {
		t.Fatalf("selected account=%d want healthy secondary 202", res.AccountID)
	}

	rec2, _ := store.Get(ctx, key2)
	rec2.State = StateDisabled
	_, _ = store.UpsertRecord(ctx, rec2)
	_, err = sel.Select(ctx, pool.SelectionRequest{TenantID: 7, PoolGroupID: 1, RequestedModel: "gpt-test"})
	if !errors.Is(err, pool.ErrAllChannelsDegraded) {
		t.Fatalf("err=%v want ErrAllChannelsDegraded", err)
	}
}

func TestChannelHealth_PoolGateLazyCooldownExpiryStartsRamp(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	rec, _ := svc.EnsureDefaultActive(ctx, key)
	expired := clock.Now().Add(-time.Second)
	rec.State = StateCoolingDown
	rec.CooldownUntil = &expired
	_, _ = store.UpsertRecord(ctx, rec)

	gate := NewServicePoolGate(svc, clock)
	req := rampAdmittedRequest(t, key.ProviderAccountID)
	ok, why, err := gate.Allow(ctx, &pool.AccountSnapshot{ID: key.ProviderAccountID, TenantID: key.TenantID}, req)
	if err != nil || !ok {
		t.Fatalf("Allow ok=%v why=%s err=%v, want lazy ramp admission", ok, why, err)
	}
	rec, _ = store.Get(ctx, key)
	if rec.State != StateRamping || rec.RampStagePct != 1 || rec.CooldownUntil != nil {
		t.Fatalf("lazy ramp rec=%+v, want ramping 1%% with cleared cooldown", rec)
	}
	if !hasAudit(auditTypes(store.Audits()), EventRampStarted) {
		t.Fatalf("ramp-start audit missing: %+v", store.Audits())
	}
}

func TestChannelHealth_AT012_SampleFloorPreventsSingleFailureCooldown(t *testing.T) {
	ctx, svc, store, _ := testService()
	key := testKey()
	if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError}); err != nil {
		t.Fatalf("ApplySignal: %v", err)
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateActive {
		t.Fatalf("single failure below sample floor state=%s want active", rec.State)
	}
}

func rampAdmittedRequest(t *testing.T, accountID int64) pool.SelectionRequest {
	t.Helper()
	for i := 0; i < 10000; i++ {
		req := pool.SelectionRequest{
			TenantID:       7,
			RequestedModel: fmt.Sprintf("ramp-%d", i),
		}
		if AdmitRamp(RampAdmissionKey(req, accountID), 1) {
			return req
		}
	}
	t.Fatal("could not find 1% ramp admission key")
	return pool.SelectionRequest{}
}

// IsEligible 直测:cooling_down 且冷却已到期必须放行(自动恢复闸门);未到期 / 无截止时间保守拒绝。
// 变异:把 StateCoolingDown 过期分支改回 return false → "已过期→放行" 断言转红。
func TestChannelHealth_IsEligibleCooldownExpiryAdmits(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	past := now.Add(-time.Second)
	future := now.Add(time.Minute)

	if !IsEligible(Record{State: StateCoolingDown, CooldownUntil: &past}, "k", now) {
		t.Fatal("冷却已到期应放行(自动恢复),实得拒绝——通道将永久卡死")
	}
	if IsEligible(Record{State: StateCoolingDown, CooldownUntil: &future}, "k", now) {
		t.Fatal("冷却未到期应拒绝,实得放行")
	}
	if IsEligible(Record{State: StateCoolingDown, CooldownUntil: nil}, "k", now) {
		t.Fatal("冷却无截止时间应保守拒绝,实得放行")
	}
}

// Allow() 在 ramp 未接线(NewPoolGate,g.ramp==nil)时,过期冷却仍须经 IsEligible 放行,
// 且记录保持 cooling_down——证明恢复来自 IsEligible 闸门而非 ramp 状态转移。这正是
// TestChannelHealth_PoolGateLazyCooldownExpiryStartsRamp(走 NewServicePoolGate→ramp)
// 漏覆盖的分支:bug 真正咬人处是 ramp 未接线的 gate。
func TestChannelHealth_PoolGateRampUnwiredExpiredCooldownAdmits(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	rec, _ := svc.EnsureDefaultActive(ctx, key)
	expired := clock.Now().Add(-time.Second)
	rec.State = StateCoolingDown
	rec.CooldownUntil = &expired
	_, _ = store.UpsertRecord(ctx, rec)

	gate := NewPoolGate(store, clock) // 关键:无 service → g.ramp == nil,不会发生 ramp 转移
	ok, why, err := gate.Allow(ctx, &pool.AccountSnapshot{ID: key.ProviderAccountID, TenantID: key.TenantID},
		pool.SelectionRequest{TenantID: key.TenantID, RequestedModel: "gpt-test"})
	if err != nil || !ok {
		t.Fatalf("ramp 未接线时过期冷却应放行 ok=%v why=%s err=%v", ok, why, err)
	}
	rec, _ = store.Get(ctx, key)
	if rec.State != StateCoolingDown {
		t.Fatalf("ramp 未接线不应转移状态,实得 %s(放行应来自 IsEligible 而非 ramp)", rec.State)
	}
}

// disable_cooling 运维逃生阀:被 flag 账号在 channelhealth gate 豁免**未到期冷却**(满流量放行),
// 但**仍尊重 disabled(ban 硬停)**。这把生产里此前死掉的 DisableCooling 真正点亮(生产 Health gate
// 是 channelhealth PoolGate,原本唯一读它的 ProviderAccountHealthGate 不跑)。
// 变异:删 Allow 里 `if account.DisableCooling` 豁免块 → "exempt 放行" 断言转红。
func TestChannelHealth_DisableCoolingBypassesCooldownNotBan(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	rec, _ := svc.EnsureDefaultActive(ctx, key)
	future := clock.Now().Add(time.Hour) // 未到期冷却:正常会被拦
	rec.State = StateCoolingDown
	rec.CooldownUntil = &future
	_, _ = store.UpsertRecord(ctx, rec)

	gate := NewPoolGate(store, clock) // ramp 未接线;未到期冷却本会被 IsEligible 拦
	req := pool.SelectionRequest{TenantID: key.TenantID, RequestedModel: "m"}

	// baseline:disable_cooling=false → 未到期冷却被拦(行为不变)。
	cooled := &pool.AccountSnapshot{ID: key.ProviderAccountID, TenantID: key.TenantID, DisableCooling: false}
	if ok, _, _ := gate.Allow(ctx, cooled, req); ok {
		t.Fatal("disable_cooling=false 的未到期冷却应被拦")
	}
	// 主修:disable_cooling=true → 豁免冷却,放行。
	exempt := &pool.AccountSnapshot{ID: key.ProviderAccountID, TenantID: key.TenantID, DisableCooling: true}
	if ok, why, _ := gate.Allow(ctx, exempt, req); !ok {
		t.Fatalf("disable_cooling=true 应豁免未到期冷却放行,why=%s", why)
	}
	// 安全边界:即便 disable_cooling=true,disabled(ban 硬停)仍必须被拦——不可被逃生阀绕过。
	rec, _ = store.Get(ctx, key)
	rec.State = StateDisabled
	rec.CooldownUntil = nil
	_, _ = store.UpsertRecord(ctx, rec)
	if ok, _, _ := gate.Allow(ctx, exempt, req); ok {
		t.Fatal("disable_cooling 不能绕过 disabled(ban 硬停),必须仍拦")
	}
}

// disable_cooling 同样豁免 ramping(渐进放量也是流量抑制,逃生阀=满流量)。
// 用一个 1% ramp 会拒绝的 req 做判别;变异删豁免块 → 该 req 被 AdmitRamp 拒 → 断言转红。
func TestChannelHealth_DisableCoolingBypassesRamping(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	rec, _ := svc.EnsureDefaultActive(ctx, key)
	rec.State = StateRamping
	rec.RampStagePct = 1 // 1%:绝大多数 admissionKey 被拒
	rec.CooldownUntil = nil
	_, _ = store.UpsertRecord(ctx, rec)

	// 找一个会被 1% ramp 拒绝的 req(否则偶然命中 admission 会让测试失去判别力)。
	var deniedReq pool.SelectionRequest
	found := false
	for i := 0; i < 10000; i++ {
		r := pool.SelectionRequest{TenantID: key.TenantID, RequestedModel: fmt.Sprintf("deny-%d", i)}
		if !AdmitRamp(RampAdmissionKey(r, key.ProviderAccountID), 1) {
			deniedReq, found = r, true
			break
		}
	}
	if !found {
		t.Fatal("找不到会被 1% ramp 拒绝的 req")
	}

	gate := NewPoolGate(store, clock)
	exempt := &pool.AccountSnapshot{ID: key.ProviderAccountID, TenantID: key.TenantID, DisableCooling: true}
	if ok, why, _ := gate.Allow(ctx, exempt, deniedReq); !ok {
		t.Fatalf("disable_cooling=true 应豁免 ramping 满流量放行,why=%s", why)
	}
}
