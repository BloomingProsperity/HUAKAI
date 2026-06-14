// HUAKAI · iKun

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	poolrouter "github.com/BloomingProsperity/HUAKAI/internal/pool/router"
	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionenforce"
)

// fakeGroupRepo: premium 档允许 pool {5}, 其它档允许 pool {7}, 均已配置。
type fakeGroupRepo struct{}

func (fakeGroupRepo) GroupRoutes(_ context.Context, _ int64, userGroup, _ string) (subscriptionenforce.GroupRoutes, error) {
	if userGroup == "premium" {
		return subscriptionenforce.GroupRoutes{Configured: true, Allowed: map[int64]struct{}{5: {}}}, nil
	}
	return subscriptionenforce.GroupRoutes{Configured: true, Allowed: map[int64]struct{}{7: {}}}, nil
}

type errGroupRepo struct{}

func (errGroupRepo) GroupRoutes(context.Context, int64, string, string) (subscriptionenforce.GroupRoutes, error) {
	return subscriptionenforce.GroupRoutes{}, errors.New("routes read failed")
}

// TestBuildGroupRoutingGates_WiresRealGroupPolicyGate 守生产激活点 (R-SUB-WIRE-1):
// buildSelector 用的 gate 链构造 (buildGroupRoutingGates) 必须把 GroupPolicy 接成接 routes
// 的真订阅 gate, 而非保持 DefaultGateChain 的 AllowAllGate。
// mutation: buildGroupRoutingGates 漏设 gates.GroupPolicy (退回 AllowAll) → default 越档不再
// 被拒 → 下面 deny 断言红, 防止 selector_wiring 漏接 gate 的回归。
func TestBuildGroupRoutingGates_WiresRealGroupPolicyGate(t *testing.T) {
	gates := buildGroupRoutingGates(fakeGroupRepo{}, nil, nil, nil, nil, nil)

	// premium 打 pool 5 (在其允许集) → 放行。
	if ok, _, err := gates.GroupPolicy.Allow(context.Background(), nil, poolrouter.SelectionRequest{
		TenantID: 1, UserGroup: "premium", RequestedModel: "m", PoolGroupID: 5,
	}); err != nil || !ok {
		t.Fatalf("premium pool5: ok=%v err=%v, want allow", ok, err)
	}

	// default 打 pool 5 (其允许集 {7}, 不含 5) → 拒。GroupPolicy 若仍是 AllowAll 此处会放行 → 红。
	ok, reason, err := gates.GroupPolicy.Allow(context.Background(), nil, poolrouter.SelectionRequest{
		TenantID: 1, UserGroup: "default", RequestedModel: "m", PoolGroupID: 5,
	})
	if err != nil {
		t.Fatalf("default pool5: unexpected err %v", err)
	}
	if ok {
		t.Fatal("buildGroupRoutingGates 必须接真 GroupPolicy gate; default 越档应被拒, got allow (AllowAll 未被替换?)")
	}
	if reason != poolrouter.GateFailureGroupPolicy {
		t.Fatalf("reason=%q want group_policy", reason)
	}
}

// TestBuildGroupRoutingGates_WiresRatePrecheckCounter 守 ROUTE-121 激活点:
// buildSelector 注入的 precheck.Counter 必须接进 gates.RatePrecheck, 否则即使
// HUAKAI_RATE_PRECHECK_ENABLED 也无效 (gate nil-counter fail-open)。
// mutation: buildGroupRoutingGates 漏把 counter 接进 gates.RatePrecheck → 超预算账号不再
// 被排除 → 下面 deny 断言红。
func TestBuildGroupRoutingGates_WiresRatePrecheckCounter(t *testing.T) {
	base := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	c := precheck.New(time.Minute, func() time.Time { return base })
	c.Record(5, 0) // 账号 5 的 RPM-1 预算已用满

	acc := &poolrouter.AccountSnapshot{ID: 5, RPMLimit: 1}

	// counter 接进 gate → 账号 5 超预算被排除。若漏接 (gate 拿到 nil counter) → 放行 → 红。
	gates := buildGroupRoutingGates(fakeGroupRepo{}, nil, nil, nil, c, nil)
	if ok, reason, _ := gates.RatePrecheck.Allow(context.Background(), acc, poolrouter.SelectionRequest{}); ok || reason != poolrouter.GateFailureRatePrecheck {
		t.Fatalf("接了 counter 的 gate 必须排除超预算账号, got ok=%v reason=%q", ok, reason)
	}

	// nil counter (默认关) → fail-open, 不排除。
	gatesOff := buildGroupRoutingGates(fakeGroupRepo{}, nil, nil, nil, nil, nil)
	if ok, _, _ := gatesOff.RatePrecheck.Allow(context.Background(), acc, poolrouter.SelectionRequest{}); !ok {
		t.Fatal("nil counter 必须 fail-open (默认关), got exclude")
	}
}

// TestBuildGroupRoutingGates_PremiumRepoErrorFailsOpenAndAlerts 守生产激活点的故障语义:
// buildSelector 接入的真实 GroupPolicy gate 在 routes repo 瞬时错误时, 对 premium 付费档
// fail-open 保可用性, 同时累计 error metric 并 WARN; default 组仍保留兼容放行。
// mutation: GroupPolicy 漏接告警 observer → metric/log 断言红; fail-closed → premium 放行断言红。
func TestBuildGroupRoutingGates_PremiumRepoErrorFailsOpenAndAlerts(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	gates := buildGroupRoutingGates(errGroupRepo{}, nil, nil, nil, nil, zap.New(core))
	before := groupPolicyFailOpenTotal.Value()

	ok, reason, err := gates.GroupPolicy.Allow(context.Background(), nil, poolrouter.SelectionRequest{
		TenantID: 1, UserGroup: "premium", RequestedModel: "m", PoolGroupID: 5,
	})
	if err != nil {
		t.Fatalf("premium repo error: unexpected err %v", err)
	}
	if !ok {
		t.Fatal("premium repo transient error must fail-open in production gate wiring, got deny")
	}
	if reason != "" {
		t.Fatalf("reason=%q want empty on fail-open", reason)
	}
	if got := groupPolicyFailOpenTotal.Value(); got != before+1 {
		t.Fatalf("group_policy_fail_open_total=%d want %d after premium repo error", got, before+1)
	}
	if logs.Len() != 1 {
		t.Fatalf("WARN log count=%d want 1", logs.Len())
	}
	if msg := logs.All()[0].Message; !strings.Contains(msg, "fail-open") {
		t.Fatalf("WARN log message %q should mention fail-open", msg)
	}

	if ok, _, err := gates.GroupPolicy.Allow(context.Background(), nil, poolrouter.SelectionRequest{
		TenantID: 1, UserGroup: "default", RequestedModel: "m", PoolGroupID: 5,
	}); err != nil || !ok {
		t.Fatalf("default repo error: ok=%v err=%v, want compatibility allow", ok, err)
	}
}

// TestBuildGroupRoutingGates_WiresContextWindowGate 守 ROUTE-023 生产激活点:
// buildGroupRoutingGates 必须把 ContextWindow 槽显式接成真 ContextWindowGate, 使其
// 在 ordered() 链里对超 window 的请求 bench 候选。
// mutation: buildGroupRoutingGates 漏设 gates.ContextWindow → 槽虽默认也是
// ContextWindowGate{} 但断言其类型 + 实际越界 deny 行为, 漏接线时 type 断言/行为断言红。
func TestBuildGroupRoutingGates_WiresContextWindowGate(t *testing.T) {
	gates := buildGroupRoutingGates(fakeGroupRepo{}, nil, nil, nil, nil, nil)

	if _, ok := gates.ContextWindow.(poolrouter.ContextWindowGate); !ok {
		t.Fatalf("ContextWindow slot type=%T, want router.ContextWindowGate (not AllowAll/default)", gates.ContextWindow)
	}

	// 超 window 的请求必须被该 gate bench (false, context_window)。
	ok, reason, err := gates.ContextWindow.Allow(context.Background(), nil, poolrouter.SelectionRequest{
		TenantID: 1, EstimatedInputTokens: 250000, ModelContextWindow: 200000,
	})
	if err != nil {
		t.Fatalf("ContextWindow.Allow err=%v", err)
	}
	if ok {
		t.Fatal("buildGroupRoutingGates 必须接真 ContextWindowGate; 250000>200000 应被拒, got allow")
	}
	if reason != poolrouter.GateFailureContextWindow {
		t.Fatalf("reason=%q want %q", reason, poolrouter.GateFailureContextWindow)
	}

	// fail-open: window 未配置 (0) 时放行。
	if ok, _, err := gates.ContextWindow.Allow(context.Background(), nil, poolrouter.SelectionRequest{
		TenantID: 1, EstimatedInputTokens: 250000, ModelContextWindow: 0,
	}); err != nil || !ok {
		t.Fatalf("unknown window must fail-open: ok=%v err=%v", ok, err)
	}
}
