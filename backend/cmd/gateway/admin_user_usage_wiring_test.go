package main

import (
	"testing"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// 本测试守住管理端用户用量的生产接线，而不只验证 Deps 能编译。
// 变异：删除 routes.go 中 UsageStore 的赋值、赋 nil 或接到别的实例，
// 非 nil 与同实例断言至少一条会变红。
func TestAdminUserRouteDepsInjectsUsageStore(t *testing.T) {
	store := &dbbilling.Queries{}
	got := adminUserRouteDeps(&deps{billingQueries: store})
	if got.UsageStore == nil {
		t.Fatal("adminuserhttp.Deps.UsageStore 未注入，管理端用户用量端点会成为 503 死路由")
	}
	if got.UsageStore != store {
		t.Fatalf("UsageStore=%T，期望注入现有 billingQueries 实例", got.UsageStore)
	}
}
