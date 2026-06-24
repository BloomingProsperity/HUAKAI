package main

import "testing"

// TestToolSurchargeRuntimeEnabled 守护 HUAKAI_TOOL_SURCHARGE_ENABLED 语义:
// 默认【开】(unset / 空 → 启用,修复工具调用漏钱);显式 "false" / "0" → 关闭
// (退回旧 $0 行为);非法值 fail-safe 回落启用(朝「不漏钱」一侧)。
//
// 变异检查:把默认从 enabled 翻成 disabled(例如把 raw=="" 分支的 return true
// 改成 return false,或整体反转布尔)→ unset / 空 / "true" / 非法值 用例由
// true 变 false,本测试 RED。这条直接守护「计费默认翻转 = 默认开」不被悄悄翻回。
func TestToolSurchargeRuntimeEnabled(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{val: "", want: true},      // 未设 → 默认开(止漏)
		{val: "true", want: true},  // 显式开
		{val: "1", want: true},     // 显式开
		{val: "TRUE", want: true},  // 大小写不敏感
		{val: "false", want: false}, // 显式关 → 退回 $0
		{val: "0", want: false},    // 显式关 → 退回 $0
		{val: "garbage", want: true}, // 非法值 → fail-safe 回落启用(不漏钱一侧)
	}
	for _, c := range cases {
		c := c
		t.Run("val="+c.val, func(t *testing.T) {
			t.Setenv(toolSurchargeEnabledEnv, c.val)
			if got := toolSurchargeRuntimeEnabled(); got != c.want {
				t.Fatalf("toolSurchargeRuntimeEnabled() for %q = %v, want %v", c.val, got, c.want)
			}
		})
	}
}

// TestBuildToolPriceSource_EnabledNonNil 是【漏装配回归守卫】的下游单元:开关开
// 时 buildToolPriceSource() 必须返回非 nil 的 Source,关时必须返回 nil。
//
// 这条与生产 wiring 的真实装配测试(TestChatHandlerDeps_ToolPricingWiredWhenEnabled,
// 在 wiring 测试里)互补:此处单独锁住「开关 → source」这一段;那条锁住
// 「source → chatHandlerDeps.ToolPricingTable」整条真实生产路径。
//
// 变异:把 buildToolPriceSource 启用分支改成 return nil(回到原 bug 价表恒 nil)→
// 开关开时 source 变 nil,本测试 RED。
func TestBuildToolPriceSource_EnabledNonNil(t *testing.T) {
	t.Setenv(toolSurchargeEnabledEnv, "true")
	if src := buildToolPriceSource(); src == nil {
		t.Fatal("开关开时 buildToolPriceSource() 必须返回非 nil(否则工具调用漏钱)")
	}

	t.Setenv(toolSurchargeEnabledEnv, "false")
	if src := buildToolPriceSource(); src != nil {
		t.Fatalf("开关关时 buildToolPriceSource() 必须返回 nil(退回 $0 旧行为),得到 %T", src)
	}
}
