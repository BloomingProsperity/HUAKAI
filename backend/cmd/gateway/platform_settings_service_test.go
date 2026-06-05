package main

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// 守 BUG3:typed-nil *platformsettings.Service 不能被当成已配置(否则 pgPool 兜底永不触发,
// 平台设置读全部走空接口)。Mutation: 改回 service = d.platformSettings 直接赋值 → typed-nil
// 接口 != nil → 本断言红。
func TestPlatformSettingsService_TypedNilNotTreatedAsConfigured(t *testing.T) {
	var nilService *platformsettings.Service // typed nil
	d := &deps{platformSettings: nilService}
	if svc := platformSettingsService(d); svc != nil {
		t.Fatalf("typed-nil platformSettings must not become a non-nil broken service, got %T", svc)
	}
}
