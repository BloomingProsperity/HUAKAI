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

// 生产接线必须在策略真相读取失败时停止选号、记录指标与 WARN。
func TestBuildGroupRoutingGates_RepoErrorFailsClosedAndAlerts(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	gates := buildGroupRoutingGates(errGroupRepo{}, nil, nil, nil, nil, zap.New(core))
	before := groupPolicyFailClosedTotal.Value()

	ok, reason, err := gates.GroupPolicy.Allow(context.Background(), nil, poolrouter.SelectionRequest{
		TenantID: 1, UserGroup: "premium", RequestedModel: "m", PoolGroupID: 5,
	})
	if ok || reason != poolrouter.GateFailureGroupPolicy || !errors.Is(err, poolrouter.ErrGroupPolicyUnavailable) {
		t.Fatalf("premium repo error: ok=%v reason=%q err=%v want fail-closed", ok, reason, err)
	}
	if got := groupPolicyFailClosedTotal.Value(); got != before+1 {
		t.Fatalf("group_policy_fail_closed_total=%d want %d", got, before+1)
	}
	if logs.Len() != 1 {
		t.Fatalf("WARN log count=%d want 1", logs.Len())
	}
	if msg := logs.All()[0].Message; !strings.Contains(msg, "停止选号") {
		t.Fatalf("WARN log message %q should mention 停止选号", msg)
	}

	if ok, _, err := gates.GroupPolicy.Allow(context.Background(), nil, poolrouter.SelectionRequest{
		TenantID: 1, UserGroup: "default", RequestedModel: "m", PoolGroupID: 5,
	}); ok || !errors.Is(err, poolrouter.ErrGroupPolicyUnavailable) {
		t.Fatalf("default repo error: ok=%v err=%v want fail-closed", ok, err)
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
