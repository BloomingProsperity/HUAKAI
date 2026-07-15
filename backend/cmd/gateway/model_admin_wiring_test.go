package main

import (
	"reflect"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

// 接线必须注入真实的模型主体 service。删掉 modelRegistry 赋值后本测试转红。
func TestModelAdminRouteDepsInjectsService(t *testing.T) {
	deps := &deps{modelRegistry: registry.NewPostgresRegistry(nil, nil)}
	got := modelAdminRouteDeps(deps)
	if got.Service == nil {
		t.Fatal("modeladminhttp.Deps.Service 未注入")
	}
	value := reflect.ValueOf(got.Service)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		t.Fatalf("modeladminhttp.Deps.Service 是 typed-nil：%T", got.Service)
	}
}

// nil 依赖树必须 fail closed，不能制造携带 typed-nil 的接口。
func TestModelAdminRouteDepsNilInputFailsClosed(t *testing.T) {
	got := modelAdminRouteDeps(nil)
	if got.Auth != nil || got.Service != nil {
		t.Fatalf("nil deps 得到非空依赖：%+v", got)
	}
}

// 缺少 registry 时 Service 必须是纯 nil；把 nil 指针直接装进接口会使本测试转红。
func TestModelAdminRouteDepsMissingRegistryHasNoTypedNil(t *testing.T) {
	got := modelAdminRouteDeps(&deps{})
	if got.Service != nil {
		t.Fatalf("缺 registry 时 Service=%T，want nil", got.Service)
	}
}
