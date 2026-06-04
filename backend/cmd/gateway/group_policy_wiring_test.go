// HUAKAI · iKun

package main

import (
	"context"
	"testing"

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

// TestBuildGroupRoutingGates_WiresRealGroupPolicyGate 守生产激活点 (R-SUB-WIRE-1):
// buildSelector 用的 gate 链构造 (buildGroupRoutingGates) 必须把 GroupPolicy 接成接 routes
// 的真订阅 gate, 而非保持 DefaultGateChain 的 AllowAllGate。
// mutation: buildGroupRoutingGates 漏设 gates.GroupPolicy (退回 AllowAll) → default 越档不再
// 被拒 → 下面 deny 断言红, 防止 selector_wiring 漏接 gate 的回归。
func TestBuildGroupRoutingGates_WiresRealGroupPolicyGate(t *testing.T) {
	gates := buildGroupRoutingGates(fakeGroupRepo{}, nil, nil)

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
