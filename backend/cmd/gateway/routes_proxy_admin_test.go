package main

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestProxyAdminRouteDepsInjectsTenantDefaultStore 防止新增 store 只出现在 handler
// 定义里却漏接 production deps。变异：删除 proxyAdminRouteDeps 中 TenantDefaults
// 赋值行，本测试立即转红，而不是等线上请求恒 503 才发现。
func TestProxyAdminRouteDepsInjectsTenantDefaultStore(t *testing.T) {
	got := proxyAdminRouteDeps(&deps{pgPool: &pgxpool.Pool{}})
	if got.TenantDefaults == nil {
		t.Fatal("TenantDefaults 未注入 proxyadminhttp.Deps")
	}
}

func TestProxyAdminRouteDepsNilInputStaysFailClosed(t *testing.T) {
	got := proxyAdminRouteDeps(nil)
	if got.Auth != nil || got.Service != nil || got.Prober != nil || got.TenantDefaults != nil {
		t.Fatalf("nil deps 应返回全空依赖，得到 %+v", got)
	}
}
