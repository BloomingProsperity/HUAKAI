package hermesops

import (
	"context"
	"errors"
	"testing"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

func TestPoolListSpec(t *testing.T) {
	deps := PoolListDeps{
		List: func(_ context.Context, params dbbilling.ListPoolsParams) ([]dbbilling.PoolGroup, error) {
			if params.TenantID != 7 {
				t.Fatalf("scope leaked: tenantID=%d want 7(必须用已鉴权 req.TenantID)", params.TenantID)
			}
			if params.LimitCount != poolListLimit {
				t.Fatalf("LimitCount 应为 poolListLimit=%d, got %d", poolListLimit, params.LimitCount)
			}
			return []dbbilling.PoolGroup{
				{ID: 1, TenantID: 7, Name: "default-pool", RoutingPolicyVersion: "v3", TopKDefault: 3, CapabilityDefault: "chat", Enabled: true},
				{ID: 2, TenantID: 7, Name: "overflow-pool", RoutingPolicyVersion: "v3", TopKDefault: 5, Enabled: true, AllowLastResort: true},
				{ID: 3, TenantID: 7, Name: "drained-pool", RoutingPolicyVersion: "v2", Enabled: false},
			}, nil
		},
	}
	spec := PoolListSpec(deps)

	res, err := spec.Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["pool_count"].(int) != 3 {
		t.Fatalf("pool_count 应 3, got %v", res.Summary["pool_count"])
	}
	// enabled_count 是计算值(过滤 Enabled),2 个启用。破坏计数逻辑会转红。
	if res.Summary["enabled_count"].(int) != 2 {
		t.Fatalf("enabled_count 应 2(两个 Enabled=true), got %v", res.Summary["enabled_count"])
	}
	items := res.Summary["items"].([]map[string]any)
	if len(items) != 3 {
		t.Fatalf("items 应 3 条, got %d", len(items))
	}

	// 配置字段正确投影。
	p0 := items[0]
	if p0["id"].(int64) != 1 || p0["name"] != "default-pool" || p0["routing_policy_version"] != "v3" {
		t.Fatalf("pool[0] 配置投影错: %v", p0)
	}
	if p0["top_k_default"].(int32) != 3 || p0["capability_default"] != "chat" || p0["enabled"] != true {
		t.Fatalf("pool[0] 配置投影错: %v", p0)
	}
	if items[2]["enabled"] != false {
		t.Fatalf("pool[2] 应 enabled=false: %v", items[2])
	}
	if items[1]["allow_last_resort"] != true {
		t.Fatalf("pool[1] 应 allow_last_resort=true: %v", items[1])
	}

	// 不回投 tenant_id(调用方已知自己租户)/不投 deleted_at(已过滤恒空)——显式列举投影,
	// 防未来给 PoolGroup 新增字段自动外泄。
	for _, omit := range []string{"tenant_id", "deleted_at"} {
		if _, has := p0[omit]; has {
			t.Fatalf("不应投影字段 %q: %v", omit, p0)
		}
	}
}

func TestPoolListNilDep(t *testing.T) {
	_, err := PoolListSpec(PoolListDeps{}).Run(context.Background(), req(7))
	if !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("nil dep 应 ErrDependencyUnwired, got %v", err)
	}
}

// List 读出错时上抛(不吞)。
func TestPoolListReadErrorBubbles(t *testing.T) {
	sentinel := errors.New("db down")
	deps := PoolListDeps{
		List: func(_ context.Context, _ dbbilling.ListPoolsParams) ([]dbbilling.PoolGroup, error) {
			return nil, sentinel
		},
	}
	_, err := PoolListSpec(deps).Run(context.Background(), req(7))
	if !errors.Is(err, sentinel) {
		t.Fatalf("读错误应上抛, got %v", err)
	}
}
