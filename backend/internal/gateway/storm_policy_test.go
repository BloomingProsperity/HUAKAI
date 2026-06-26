package gateway

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func stormTime() time.Time { return time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC) }

func TestStormPolicy_HighBudgetAdmits(t *testing.T) {
	p := NewStormPolicy(StormPolicyConfig{
		GlobalRate: 1000, GlobalBurst: 100,
		PerEndpointRate: 1000, PerEndpointBurst: 100,
	})
	now := stormTime()
	val, err, denied := p.Acquire(now, "acct-1", "ep-A", func() (any, error) {
		return "ok", nil
	})
	if err != nil || denied != DenyNone || val != "ok" {
		t.Fatalf("admit failed: val=%v err=%v denied=%s", val, err, denied)
	}
}

func TestStormPolicy_GlobalDenialRefundsEndpoint(t *testing.T) {
	p := NewStormPolicy(StormPolicyConfig{
		GlobalRate: 0, GlobalBurst: 0, // 全局:永不放行
		PerEndpointRate: 100, PerEndpointBurst: 5,
	})
	now := stormTime()
	val, err, denied := p.Acquire(now, "acct-1", "ep-A", func() (any, error) {
		return nil, errors.New("should not run")
	})
	if denied != DenyGlobal {
		t.Fatalf("denied=%s; want DenyGlobal", denied)
	}
	if err != nil || val != nil {
		t.Fatalf("denied call should not run fn: val=%v err=%v", val, err)
	}
	// endpoint 桶应仍处于满 burst(全局拒绝后已退还)
	eb := p.endpointBucket("ep-A")
	tokens, _ := eb.Snapshot()
	if tokens != 5 {
		t.Fatalf("endpoint refund failed; tokens=%.1f want 5", tokens)
	}
}

func TestStormPolicy_EndpointDenialDoesNotConsumeGlobal(t *testing.T) {
	p := NewStormPolicy(StormPolicyConfig{
		GlobalRate: 100, GlobalBurst: 10,
		PerEndpointRate: 0, PerEndpointBurst: 0, // endpoint:永不放行
	})
	now := stormTime()
	_, _, denied := p.Acquire(now, "acct-1", "ep-A", func() (any, error) { return nil, nil })
	if denied != DenyEndpoint {
		t.Fatalf("denied=%s; want DenyEndpoint", denied)
	}
	// 全局桶应保持不动
	tokens, _ := p.globalBucket.Snapshot()
	if tokens != 10 {
		t.Fatalf("global must not consume on endpoint denial; tokens=%.1f want 10", tokens)
	}
}

func TestStormPolicy_FailedFnKeepsTokensConsumed(t *testing.T) {
	p := NewStormPolicy(StormPolicyConfig{
		GlobalRate: 0.1, GlobalBurst: 1,
		PerEndpointRate: 0.1, PerEndpointBurst: 1,
	})
	now := stormTime()
	wantErr := errors.New("vendor down")
	_, err, denied := p.Acquire(now, "acct-1", "ep-A", func() (any, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) || denied != DenyNone {
		t.Fatalf("err=%v denied=%s; want vendor-down + DenyNone", err, denied)
	}
	// 失败的 fn 必须不退还 —— 第二次调用应被拒绝(桶已空)
	_, _, denied2 := p.Acquire(now, "acct-2", "ep-A", func() (any, error) {
		return "should-not-run", nil
	})
	if denied2 == DenyNone {
		t.Fatalf("after fn-error storm window must stay closed; denied2=%s", denied2)
	}
}

func TestStormPolicy_SameAccountDedupsToOneFnCall(t *testing.T) {
	p := NewStormPolicy(StormPolicyConfig{
		GlobalRate: 1, GlobalBurst: 1, // 总共只有 1 个令牌
		PerEndpointRate: 1, PerEndpointBurst: 1,
	})
	now := stormTime()
	var fnCalls atomic.Int32
	gate := make(chan struct{})
	fn := func() (any, error) {
		<-gate
		fnCalls.Add(1)
		return "shared-result", nil
	}
	var wg sync.WaitGroup
	results := make([]any, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, _, _ := p.Acquire(now, "same-acct", "ep-A", fn)
			results[i] = v
		}(i)
	}
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()
	if got := fnCalls.Load(); got != 1 {
		t.Fatalf("same-account dedup: fn calls=%d; want 1", got)
	}
	for _, r := range results {
		if r != "shared-result" {
			t.Fatalf("follower got wrong result: %v", r)
		}
	}
	// 关键:应只消耗 1 个令牌(跟随者不付费)
	gtokens, _ := p.globalBucket.Snapshot()
	if gtokens != 0 {
		t.Fatalf("dedup: global should consume only 1; tokens=%.1f", gtokens)
	}
}

func TestStormPolicy_DifferentAccountsDoNotInterfere(t *testing.T) {
	p := NewStormPolicy(StormPolicyConfig{
		GlobalRate: 100, GlobalBurst: 10,
		PerEndpointRate: 100, PerEndpointBurst: 10,
	})
	now := stormTime()
	var calls atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		acct := []string{"a", "b", "c"}[i]
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			_, _, _ = p.Acquire(now, a, "ep-A", func() (any, error) {
				calls.Add(1)
				return a, nil
			})
		}(acct)
	}
	wg.Wait()
	if got := calls.Load(); got != 3 {
		t.Fatalf("distinct accounts; calls=%d want 3", got)
	}
}

func TestStormPolicy_DifferentEndpointsIndependentBudget(t *testing.T) {
	p := NewStormPolicy(StormPolicyConfig{
		GlobalRate: 100, GlobalBurst: 100,
		PerEndpointRate: 0.1, PerEndpointBurst: 1,
	})
	now := stormTime()
	// 耗尽 ep-A
	_, _, d1 := p.Acquire(now, "a1", "ep-A", func() (any, error) { return 1, nil })
	if d1 != DenyNone {
		t.Fatalf("ep-A first acquire denied=%s", d1)
	}
	// ep-A 第二次应失败
	_, _, d2 := p.Acquire(now, "a2", "ep-A", func() (any, error) { return 2, nil })
	if d2 != DenyEndpoint {
		t.Fatalf("ep-A second got %s; want DenyEndpoint", d2)
	}
	// ep-B 独立 —— 应成功
	_, _, d3 := p.Acquire(now, "a3", "ep-B", func() (any, error) { return 3, nil })
	if d3 != DenyNone {
		t.Fatalf("ep-B independent budget; got %s", d3)
	}
}

func TestStormPolicy_NextEligibleAtMaxOfTwoBuckets(t *testing.T) {
	p := NewStormPolicy(StormPolicyConfig{
		GlobalRate: 1, GlobalBurst: 1,
		PerEndpointRate: 2, PerEndpointBurst: 1,
	})
	now := stormTime()
	// 两个桶都耗尽
	_, _, _ = p.Acquire(now, "a", "ep-A", func() (any, error) { return nil, nil })
	// 此时:global 需要 1.0s,endpoint 需要 0.5s → 取最大值 = 1.0s
	next := p.NextEligibleAt(now, "ep-A")
	gNext := p.globalBucket.NextAvailableAt(now)
	if !next.Equal(gNext) {
		t.Fatalf("NextEligibleAt should be max (global) %v; got %v", gNext, next)
	}
}

func TestStormPolicy_NextEligibleAtZeroPropagation(t *testing.T) {
	// 全局速率为 0 → 空桶的 NextAvailable=zero
	p := NewStormPolicy(StormPolicyConfig{
		GlobalRate: 0, GlobalBurst: 0,
		PerEndpointRate: 100, PerEndpointBurst: 100,
	})
	now := stormTime()
	got := p.NextEligibleAt(now, "ep-A")
	if !got.IsZero() {
		t.Fatalf("zero (never) must propagate; got %v", got)
	}
}

func TestStormPolicy_FnErrorReturnedToCaller(t *testing.T) {
	p := NewStormPolicy(StormPolicyConfig{
		GlobalRate: 100, GlobalBurst: 10,
		PerEndpointRate: 100, PerEndpointBurst: 10,
	})
	now := stormTime()
	wantErr := errors.New("upstream 500")
	_, err, denied := p.Acquire(now, "a", "ep", func() (any, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("fn error not propagated: %v", err)
	}
	if denied != DenyNone {
		t.Fatalf("fn-error case denied=%s; want DenyNone", denied)
	}
}
