package main

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// TestPlatformSettingsRouteServiceFallsBackWhenServicePointerNil 守护
// mountPlatformSettingsRoutes 里的 typed-nil 陷阱。变异检查:先把
// d.platformSettings 赋给 service 接口,再去判断 `service ==
// nil`;此时 nil 的 *platformsettings.Service 会变成一个非 nil 的接口,
// pgPool 回退被跳过,本测试便会看到一个 typed-nil 的 service。
func TestPlatformSettingsRouteServiceFallsBackWhenServicePointerNil(t *testing.T) {
	var typedNil *platformsettings.Service

	got := platformSettingsRouteService(&deps{
		platformSettings: typedNil,
		pgPool:           &pgxpool.Pool{},
	})

	if got == nil {
		t.Fatal("nil platformSettings plus pgPool must build a fallback service")
	}
	value := reflect.ValueOf(got)
	if value.Kind() == reflect.Ptr && value.IsNil() {
		t.Fatalf("fallback returned typed-nil %T; pgPool fallback did not engage", got)
	}
	if _, ok := got.(*platformsettings.Service); !ok {
		t.Fatalf("fallback service type=%T, want *platformsettings.Service", got)
	}
}
