package main

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

// auth 降级车道默认开启;只有显式 false 关闭;无法解析的值不得静默关掉保护。
func TestAuthCooldownEnabledFromEnvDefaultsOn(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", true},
		{"   ", true},
		{"true", true},
		{"1", true},
		{"false", false},
		{"0", false},
		{"FALSE", false},
		{"nope", true},
	}
	for _, tc := range cases {
		t.Setenv("HUAKAI_AUTH_COOLDOWN_ENABLED", tc.value)
		if got := authCooldownEnabledFromEnv(); got != tc.want {
			t.Fatalf("HUAKAI_AUTH_COOLDOWN_ENABLED=%q: got %v want %v", tc.value, got, tc.want)
		}
	}
}

// 装配入口与分类规则同源:默认(未设 env)构造车道且规则开;显式关闭时不构造车道且规则关;
// 开启但无数据库时退化为进程内车道。判别:漏调 SetAuthLaneRulesEnabled 时规则断言变红。
func TestConfigureAuthCooldownLaneKeepsRulesInSync(t *testing.T) {
	t.Cleanup(func() { gateway.SetAuthLaneRulesEnabled(false) })
	t.Setenv("HUAKAI_AUTH_COOLDOWN_ENABLED", "false")
	gateway.SetAuthLaneRulesEnabled(true)
	if configureAuthCooldownLane(nil) != nil || gateway.AuthLaneRulesEnabled() {
		t.Fatal("显式关闭时不得构造车道,且车道绑定的分类规则必须同步关闭")
	}
	t.Setenv("HUAKAI_AUTH_COOLDOWN_ENABLED", "")
	local := configureAuthCooldownLane(nil)
	if local == nil || local.PersistenceKind() != "process_local" {
		t.Fatalf("默认开启且无数据库时应退化为进程内车道: %v", local)
	}
	if !gateway.AuthLaneRulesEnabled() {
		t.Fatal("默认开启时车道绑定的分类规则必须同步开启")
	}
}
