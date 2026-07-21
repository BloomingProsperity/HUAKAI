package main

import (
	"os"
	"strings"
	"testing"

	auditreceipt "github.com/BloomingProsperity/HUAKAI/internal/audit"
)

// 变异：删除 routes.go 中 Resolver 的注入行。
// 路由仍会存在，但每次裁决都只返回 503，形成“已实现但生产死接线”。
func TestDisputeAdminRouteDepsInjectsRefundResolver(t *testing.T) {
	store := &auditreceipt.CostDisputeStore{}
	resolver := &auditreceipt.CostDisputeResolver{}
	got := disputeAdminRouteDeps(&deps{disputeStore: store, disputeResolver: resolver})
	if got.Store != store {
		t.Fatal("DisputeAdminDeps.Store 未注入生产 dispute store")
	}
	if got.Resolver != resolver {
		t.Fatal("DisputeAdminDeps.Resolver 未注入原子退款组合器")
	}
}

// 变异：删除 wiring.go 中 disputeResolver 的构造或 deps 赋值。
// 该源码门与上面的真实 route-deps 测试分别钉住 runtime 构造和路由传递两站。
func TestGatewayRuntimeBuildsAndStoresDisputeRefundResolver(t *testing.T) {
	raw, err := os.ReadFile("wiring.go")
	if err != nil {
		t.Fatalf("读取 wiring.go: %v", err)
	}
	source := string(raw)
	normalized := strings.Join(strings.Fields(source), " ")
	for _, required := range []string{
		"auditreceipt.WithDisputeQuotaReverser(quotaReverser)",
		"auditreceipt.NewCostDisputeResolver(pgPool, disputeRefundSettler, disputeResolverOpts...)",
		"disputeResolver: disputeResolver",
	} {
		if !strings.Contains(normalized, required) {
			t.Fatalf("生产 runtime 缺少争议退款接线 %q", required)
		}
	}
}
