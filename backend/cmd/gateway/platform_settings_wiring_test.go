package main

import (
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// 守:chatHandlerDeps 必须把 deps.platformSettings 注入 ChatHandlerDeps.PlatformSettings。
// 此前该字段从不赋值 → 热路径恒 nil getter → warmup_intercept 与 codex_client_access.* 键
// 「可写、有校验、落库,运行时永不被读」= 死开关(审查 CONFIRMED S1)。变异:删掉 routes.go
// 里 PlatformSettings 那行注入 → 本测试红。
func TestChatHandlerDepsInjectsPlatformSettings(t *testing.T) {
	svc := &platformsettings.Service{}
	d := &deps{cfg: &Config{}, platformSettings: svc}
	got := chatHandlerDeps(d)
	if got.PlatformSettings == nil {
		t.Fatal("ChatHandlerDeps.PlatformSettings 未注入:平台设置键(warmup_intercept/codex_client_access.*)在热路径成死开关")
	}
}

// 守:chatHandlerDeps 必须把 HUAKAI_NONSTREAM_KEEPALIVE_INTERVAL 注入 NonStreamKeepAliveInterval。
// 变异:删掉 routes.go 里 NonStreamKeepAliveInterval 那行注入 → 字段恒 0 → 本测试红。
func TestChatHandlerDepsInjectsNonStreamKeepAlive(t *testing.T) {
	t.Setenv("HUAKAI_NONSTREAM_KEEPALIVE_INTERVAL", "85s")
	d := &deps{cfg: &Config{}, platformSettings: &platformsettings.Service{}}
	got := chatHandlerDeps(d)
	if got.NonStreamKeepAliveInterval != 85*time.Second {
		t.Fatalf("NonStreamKeepAliveInterval 未从 env 注入:got %v want 85s(反代前长 buffered 保活失效)", got.NonStreamKeepAliveInterval)
	}
}
