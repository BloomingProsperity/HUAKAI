// HUAKAI · iKun

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	poolrouter "github.com/BloomingProsperity/HUAKAI/internal/pool/router"
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
	gates := buildGroupRoutingGates(fakeGroupRepo{}, nil, nil, nil)

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

// TestBuildGroupRoutingGates_PremiumRepoErrorFailsOpenAndAlerts 守生产激活点的故障语义:
// buildSelector 接入的真实 GroupPolicy gate 在 routes repo 瞬时错误时, 对 premium 付费档
// fail-open 保可用性, 同时累计 error metric 并 WARN; default 组仍保留兼容放行。
// mutation: GroupPolicy 漏接告警 observer → metric/log 断言红; fail-closed → premium 放行断言红。
func TestBuildGroupRoutingGates_PremiumRepoErrorFailsOpenAndAlerts(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	gates := buildGroupRoutingGates(errGroupRepo{}, nil, nil, zap.New(core))
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
