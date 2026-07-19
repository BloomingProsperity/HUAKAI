package router

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestAdaptiveSignalsUseFreshDataOnly(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	account := &AccountSnapshot{
		LoadRate: 0.5, ErrorEWMA: 0.8, ResponseLatencyMSEWMA: 20000,
		RoutingSignalSampleCount: 10, RoutingSignalObservedAt: now.Add(-16 * time.Minute),
		UpstreamQuotaState: "available", UpstreamQuotaRemainingKnown: true, UpstreamQuotaRemaining: 90,
		UpstreamQuotaObservedAt: now.Add(-2*time.Hour - time.Second),
	}
	contributions := adaptiveContributions(account, now)
	if len(contributions) != 1 || contributions["capacity_headroom"] != 0.15 {
		t.Fatalf("过期信号必须保持中性：%+v", contributions)
	}

	account.RoutingSignalObservedAt = now.Add(-time.Minute)
	account.UpstreamQuotaObservedAt = now.Add(-time.Minute)
	contributions = adaptiveContributions(account, now)
	if contributions["reliability"] == 0 || contributions["response_speed"] == 0 || contributions["quota_headroom"] == 0 {
		t.Fatalf("新鲜信号未进入评分：%+v", contributions)
	}
}

func TestUpstreamCostRatioUsesNeutralUnknownAndBoundedContribution(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	unknown := adaptiveContributions(&AccountSnapshot{LoadRate: 0.5}, now)
	if _, ok := unknown["upstream_cost_efficiency"]; ok {
		t.Fatalf("未知成本不得伪造评分：%+v", unknown)
	}
	cheap, expensive := 0.5, 2.0
	cheapScore := adaptiveContributions(&AccountSnapshot{LoadRate: 0.5, UpstreamCostRatio: &cheap}, now)["upstream_cost_efficiency"]
	expensiveScore := adaptiveContributions(&AccountSnapshot{LoadRate: 0.5, UpstreamCostRatio: &expensive}, now)["upstream_cost_efficiency"]
	if cheapScore <= 0 || expensiveScore >= 0 || cheapScore != -expensiveScore {
		t.Fatalf("成本评分方向错误：cheap=%v expensive=%v", cheapScore, expensiveScore)
	}
	veryCheap, veryExpensive := 0.0001, 100.0
	if got := adaptiveContributions(&AccountSnapshot{UpstreamCostRatio: &veryCheap}, now)["upstream_cost_efficiency"]; got != 0.05 {
		t.Fatalf("低成本贡献未封顶：%v", got)
	}
	if got := adaptiveContributions(&AccountSnapshot{UpstreamCostRatio: &veryExpensive}, now)["upstream_cost_efficiency"]; got != -0.05 {
		t.Fatalf("高成本贡献未封底：%v", got)
	}
	policy := &RoutingPolicy{OperatorScoring: true}
	cheapWeight := accountSelectionWeight(&AccountSnapshot{Weight: 1, UpstreamCostRatio: &cheap}, policy, now)
	neutralWeight := accountSelectionWeight(&AccountSnapshot{Weight: 1}, policy, now)
	expensiveWeight := accountSelectionWeight(&AccountSnapshot{Weight: 1, UpstreamCostRatio: &expensive}, policy, now)
	if !(cheapWeight > neutralWeight && neutralWeight > expensiveWeight) {
		t.Fatalf("成本信号未进入正式选号权重：cheap=%d neutral=%d expensive=%d", cheapWeight, neutralWeight, expensiveWeight)
	}
}

func TestUpstreamQuotaGateRejectsOnlyFreshExhaustion(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	gate := upstreamQuotaGate{Now: func() time.Time { return now }}
	account := &AccountSnapshot{UpstreamQuotaState: "exhausted", UpstreamQuotaObservedAt: now.Add(-time.Minute)}
	ok, reason, err := gate.Allow(context.Background(), account, SelectionRequest{})
	if err != nil || ok || reason != GateFailureUpstreamQuota {
		t.Fatalf("新鲜耗尽 Allow=(%v,%q,%v)", ok, reason, err)
	}
	account.UpstreamQuotaObservedAt = now.Add(-2*time.Hour - time.Second)
	ok, reason, err = gate.Allow(context.Background(), account, SelectionRequest{})
	if err != nil || !ok || reason != "" {
		t.Fatalf("过期耗尽不得继续封禁 Allow=(%v,%q,%v)", ok, reason, err)
	}
}

func TestSelectorEscapesDegradedStickyAndExplainsDecision(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	bad := &AccountSnapshot{
		ID: 101, TenantID: 7, Priority: 1, Weight: 1, MaxConcurrency: 4, HealthState: "healthy",
		ErrorEWMA: 0.8, RoutingSignalSampleCount: 10, RoutingSignalObservedAt: now.Add(-time.Minute),
	}
	good := &AccountSnapshot{
		ID: 202, TenantID: 7, Priority: 1, Weight: 1, MaxConcurrency: 4, HealthState: "healthy",
		ErrorEWMA: 0.05, RoutingSignalSampleCount: 10, RoutingSignalObservedAt: now.Add(-time.Minute),
	}
	selector := NewDefaultSelector(
		&stubAccountSource{accounts: []*AccountSnapshot{bad, good}},
		WithNow(func() time.Time { return now }),
		WithStickyStore(&stubSticky{bindings: map[string]int64{"session-a": bad.ID}}),
		WithRoutingPolicySource(&stubPolicy{p: &RoutingPolicy{
			SelectionMode: SelectionModePriorityWeighted, OperatorScoring: true,
			TopKDefault: 2, ScoringPolicyVersion: "adaptive-v1",
		}}),
	)
	result, err := selector.Select(context.Background(), SelectionRequest{TenantID: 7, RequestedModel: "m", SessionHash: "session-a"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if result.AccountID != good.ID || result.StickyState != StickyStateMiss {
		t.Fatalf("选号 account=%d sticky=%q，期望逃离到 %d", result.AccountID, result.StickyState, good.ID)
	}
	var reason struct {
		StickyBreakReason    *string            `json:"sticky_break_reason"`
		ScoringPolicyVersion string             `json:"scoring_policy_version"`
		Contributions        map[string]float64 `json:"signal_contributions"`
	}
	if err := json.Unmarshal(result.RoutingReasonJSON, &reason); err != nil {
		t.Fatalf("解析路由原因: %v", err)
	}
	if reason.StickyBreakReason == nil || *reason.StickyBreakReason != "recent_error_rate_high" || reason.ScoringPolicyVersion != "adaptive-v1" || reason.Contributions["reliability"] == 0 {
		t.Fatalf("路由原因不完整：%s", result.RoutingReasonJSON)
	}
}

func TestBrokenStickyRemainsLastResortWhenAlternativesCannotAcquire(t *testing.T) {
	accounts := []*AccountSnapshot{{ID: 101}, {ID: 202}, {ID: 303}}
	got := deprioritizeBrokenSticky(accounts, 101, false)
	if got[0].ID != 202 || got[1].ID != 303 || got[2].ID != 101 {
		t.Fatalf("恶化黏性账号没有移动到末位兜底：%v,%v,%v", got[0].ID, got[1].ID, got[2].ID)
	}
	kept := deprioritizeBrokenSticky([]*AccountSnapshot{{ID: 101}}, 101, false)
	if len(kept) != 1 || kept[0].ID != 101 {
		t.Fatalf("唯一候选不得被移除：%+v", kept)
	}
}
