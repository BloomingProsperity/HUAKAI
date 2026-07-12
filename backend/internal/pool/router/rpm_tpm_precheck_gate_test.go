package router

import (
	"context"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
)

func precheckClock() func() time.Time {
	base := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return base }
}

// 到达 RPM 预算时 gate 会剔除该账号;变异守卫:若 Counter.Check 的结果
// 被忽略,此处必须变红。
func TestRatePrecheckGate_AtRPM_Excludes(t *testing.T) {
	c := precheck.New(time.Minute, precheckClock())
	acc := &AccountSnapshot{ID: 42, RPMLimit: 3}
	g := RatePrecheckGate{Counter: c}
	// 为该账号记录 3 次请求以填满预算。
	for i := 0; i < 3; i++ {
		c.Record(42, 0)
	}
	ok, reason, err := g.Allow(context.Background(), acc, SelectionRequest{})
	if err != nil || ok || reason != GateFailureRatePrecheck {
		t.Fatalf("at-budget account must be excluded, got ok=%v reason=%q err=%v", ok, reason, err)
	}
	// 区分性:另一个尚未用掉预算的账号会被放行。
	other := &AccountSnapshot{ID: 99, RPMLimit: 3}
	if ok, _, _ := g.Allow(context.Background(), other, SelectionRequest{}); !ok {
		t.Fatalf("untouched account must still be allowed")
	}
}

// 在预算之下时 gate 放行。
func TestRatePrecheckGate_UnderBudget_Allows(t *testing.T) {
	c := precheck.New(time.Minute, precheckClock())
	acc := &AccountSnapshot{ID: 7, RPMLimit: 5}
	c.Record(7, 0)
	g := RatePrecheckGate{Counter: c}
	if ok, _, _ := g.Allow(context.Background(), acc, SelectionRequest{}); !ok {
		t.Fatalf("1 of 5 used must allow")
	}
}

// TPM 使用请求的 EstimatedInputTokens,因此单个超大请求会在引发上游
// token-rate 429 之前就被预先剔除。
func TestRatePrecheckGate_TPM_UsesEstimatedTokens(t *testing.T) {
	c := precheck.New(time.Minute, precheckClock())
	acc := &AccountSnapshot{ID: 8, TPMLimit: 100}
	g := RatePrecheckGate{Counter: c}
	req := SelectionRequest{EstimatedInputTokens: 101}
	if ok, reason, _ := g.Allow(context.Background(), acc, req); ok || reason != GateFailureRatePrecheck {
		t.Fatalf("oversized request must be excluded on tpm, got ok=%v reason=%q", ok, reason)
	}
	if ok, _, _ := g.Allow(context.Background(), acc, SelectionRequest{EstimatedInputTokens: 100}); !ok {
		t.Fatalf("a request exactly at the tpm budget must fit")
	}
}

// Fail-open:未配置 limit、nil counter、nil account 三种情况都放行。
func TestRatePrecheckGate_FailOpen(t *testing.T) {
	c := precheck.New(time.Minute, precheckClock())
	// 未设置 limit 的账号 → 即使大量消耗后也始终放行
	noLimit := &AccountSnapshot{ID: 5}
	for i := 0; i < 1000; i++ {
		c.Record(5, 1000)
	}
	if ok, _, _ := (RatePrecheckGate{Counter: c}).Allow(context.Background(), noLimit, SelectionRequest{}); !ok {
		t.Fatalf("account with no rpm/tpm limit must always be allowed")
	}
	// nil counter → fail-open 放行
	if ok, _, _ := (RatePrecheckGate{}).Allow(context.Background(), &AccountSnapshot{ID: 1, RPMLimit: 1}, SelectionRequest{}); !ok {
		t.Fatalf("nil counter must fail-open")
	}
	// nil account → 放行
	if ok, _, _ := (RatePrecheckGate{Counter: c}).Allow(context.Background(), nil, SelectionRequest{}); !ok {
		t.Fatalf("nil account must allow")
	}
}

// 默认 chain 携带一个 fail-open 的 RatePrecheck gate,且该 gate 位于
// 有序 chain 中(因此接线是真实的,而非悬空)。
func TestRatePrecheckGate_InDefaultChainFailOpen(t *testing.T) {
	chain := DefaultGateChain()
	if chain.RatePrecheck == nil {
		t.Fatalf("default chain must wire a RatePrecheck gate")
	}
	var found bool
	for _, ng := range chain.ordered() {
		if ng.fallback == GateFailureRatePrecheck {
			found = true
		}
	}
	if !found {
		t.Fatalf("RatePrecheck gate must be in the ordered chain")
	}
	// 默认 chain(nil counter)不得剔除有预算的账号。
	ok, _, err := chain.Allow(context.Background(), &AccountSnapshot{ID: 3, RPMLimit: 1}, SelectionRequest{})
	if err != nil || !ok {
		t.Fatalf("default chain must fail-open for rate precheck, got ok=%v err=%v", ok, err)
	}
}
