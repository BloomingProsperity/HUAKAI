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

func TestChannelHealth_PoolGateExpiredCooldownIsReadOnly(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	rec, _ := svc.EnsureDefaultActive(ctx, key)
	expired := clock.Now().Add(-time.Second)
	rec.State = StateCoolingDown
	rec.CooldownUntil = &expired
	_, _ = store.UpsertRecord(ctx, rec)

	gate := NewServicePoolGate(svc, clock)
	req := pool.SelectionRequest{TenantID: key.TenantID, RequestID: "req-read-only"}
	ok, why, err := gate.Allow(ctx, &pool.AccountSnapshot{ID: key.ProviderAccountID, TenantID: key.TenantID}, req)
	if err != nil || ok || why != pool.GateFailureHealth {
		t.Fatalf("Allow ok=%v why=%s err=%v, want read-only health rejection", ok, why, err)
	}
	rec, _ = store.Get(ctx, key)
	if rec.State != StateCoolingDown || rec.CooldownUntil == nil {
		t.Fatalf("request path mutated health record: %+v", rec)
	}
	if hasAudit(auditTypes(store.Audits()), EventRampStarted) {
		t.Fatalf("request path emitted ramp transition log: %+v", store.Audits())
	}
}

func TestRampAdmissionKeyStableAcrossRetriesAndDistinctAcrossRequests(t *testing.T) {
	base := pool.SelectionRequest{
		TenantID:       7,
		RequestedModel: "model-a",
		RequestID:      "req-1",
		ClaimID:        91,
		AttemptSeq:     1,
	}
	retry := base
	retry.AttemptSeq = 9
	if got, want := RampAdmissionKey(retry, 33), RampAdmissionKey(base, 33); got != want {
		t.Fatalf("same request changed ramp key across retries: got %q want %q", got, want)
	}

	other := base
	other.RequestID = "req-2"
	other.ClaimID = 92
	if RampAdmissionKey(other, 33) == RampAdmissionKey(base, 33) {
		t.Fatal("distinct requests must not share the same ramp admission key")
	}

	continuation := base
	continuation.ContinuationKey = "chain-1"
	continuation.RequestID = "req-3"
	continuationRetry := continuation
	continuationRetry.RequestID = "req-4"
	continuationRetry.AttemptSeq = 5
	if RampAdmissionKey(continuationRetry, 33) != RampAdmissionKey(continuation, 33) {
		t.Fatal("continuation affinity must remain stable across HTTP requests and retries")
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

// cooling_down 无论时间是否到期都必须拒绝；只有后台协调器可把它转成 ramping。
func TestChannelHealth_IsEligibleCoolingAlwaysRejects(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	past := now.Add(-time.Second)
	future := now.Add(time.Minute)

	if IsEligible(Record{State: StateCoolingDown, CooldownUntil: &past}, "k", now) {
		t.Fatal("冷却已到期但后台尚未转成 ramping，不得由请求热路径直接放行")
	}
	if IsEligible(Record{State: StateCoolingDown, CooldownUntil: &future}, "k", now) {
		t.Fatal("冷却未到期应拒绝,实得放行")
	}
	if IsEligible(Record{State: StateCoolingDown, CooldownUntil: nil}, "k", now) {
		t.Fatal("冷却无截止时间应保守拒绝,实得放行")
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

	gate := NewPoolGate(store, clock)
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
