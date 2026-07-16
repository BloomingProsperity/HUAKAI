package main

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 本测试防止路由虽然挂载，却漏掉生产 Service 注入而恒返回 503。
// 变异：删除 modelRoutingOverrideRouteDeps 的 Service 赋值，本测试立即转红。
func TestModelRoutingOverrideRouteDepsInjectsService(t *testing.T) {
	got := modelRoutingOverrideRouteDeps(&deps{pgPool: &pgxpool.Pool{}})
	if got.Service == nil {
		t.Fatal("modelroutingadminhttp.Deps.Service 未注入")
	}
	value := reflect.ValueOf(got.Service)
	if value.Kind() == reflect.Ptr && value.IsNil() {
		t.Fatalf("modelroutingadminhttp.Deps.Service 是 typed-nil：%T", got.Service)
	}
}

func TestModelRoutingOverrideRouteDepsNilInputFailsClosed(t *testing.T) {
	got := modelRoutingOverrideRouteDeps(nil)
	if got.Auth != nil || got.Service != nil {
		t.Fatalf("nil deps 应返回全空依赖，得到 %+v", got)
	}
}

func TestModelRoutingOverrideRouteDepsMissingPoolHasNoTypedNil(t *testing.T) {
	got := modelRoutingOverrideRouteDeps(&deps{})
	if got.Service != nil {
		t.Fatalf("缺少 pgPool 时 Service 必须是真 nil，不能是 typed-nil：%T", got.Service)
	}
}
