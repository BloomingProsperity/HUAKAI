package main

import "testing"

// TestChatHandlerDeps_ToolPricingWiredWhenEnabled 是【漏装配回归守卫(最重要)】。
//
// 它走真实生产装配函数 chatHandlerDeps(*deps),断言:当 deps.toolPriceSource 被
// 开关链路填充(非 nil)时,chatHandlerDeps 必须把它接进 ChatHandlerDeps.ToolPricingTable
// (非 nil)。这正是原 bug 的反面——原 bug 是 chatHandlerDeps 从不给 ToolPricingTable
// 赋值,导致生产恒 nil、工具调用加 $0 漏钱。
//
// 装配输入用真实 env 开关链路 buildToolPriceSource()(开关开 → 非 nil),而非手搓
// source,确保「开关 → source → chatHandlerDeps」整条生产路径都被覆盖。
//
// 变异:把 routes.go 的 chatHandlerDeps 里 `ToolPricingTable: d.toolPriceSource`
// 那行删掉(或改回不赋值 / 赋 nil)→ chatDeps.ToolPricingTable 变 nil,本测试 RED。
// 这条能抓住「字段从没被赋值」这个原始缺陷。
func TestChatHandlerDeps_ToolPricingWiredWhenEnabled(t *testing.T) {
	// 走真实开关链路:开关开 → buildToolPriceSource 返回非 nil 的 platformSource。
	t.Setenv(toolSurchargeEnabledEnv, "true")

	d := &deps{
		cfg:             &Config{BillingPolicyVersion: "1.0", RequestClass: "standard"},
		toolPriceSource: buildToolPriceSource(),
	}
	if d.toolPriceSource == nil {
		t.Fatal("前置条件:开关开时 buildToolPriceSource() 必须非 nil")
	}

	chatDeps := chatHandlerDeps(d)
	if chatDeps.ToolPricingTable == nil {
		t.Fatal("漏装配回归:chatHandlerDeps 未把 toolPriceSource 接进 ToolPricingTable(工具调用加 $0 漏钱)")
	}

	// 判别性校验:接进来的 source 必须真能查出非零默认价(否则即便非 nil 也漏钱)。
	prices := chatDeps.ToolPricingTable.Lookup(7, "gpt-4o")
	if prices.IsZero() {
		t.Fatal("接入的 ToolPricingTable 查出全零价 → 仍漏钱(platformSource 默认价未生效)")
	}
}

// TestChatHandlerDeps_ToolPricingNilWhenDisabled 守护运维退路:开关关时
// deps.toolPriceSource 为 nil,chatHandlerDeps 接进的 ToolPricingTable 也必须是 nil
// (退回旧 $0 行为)。这条与上面一条一起把「开关两态 → 装配两态」都钉死。
//
// 变异:把 chatHandlerDeps 里 ToolPricingTable 改成无条件接一个非 nil source →
// 关闭语义失效,本测试 RED。
func TestChatHandlerDeps_ToolPricingNilWhenDisabled(t *testing.T) {
	t.Setenv(toolSurchargeEnabledEnv, "false")

	d := &deps{
		cfg:             &Config{BillingPolicyVersion: "1.0", RequestClass: "standard"},
		toolPriceSource: buildToolPriceSource(),
	}
	if d.toolPriceSource != nil {
		t.Fatalf("前置条件:开关关时 buildToolPriceSource() 必须 nil,得到 %T", d.toolPriceSource)
	}

	chatDeps := chatHandlerDeps(d)
	if chatDeps.ToolPricingTable != nil {
		t.Fatalf("开关关时 ToolPricingTable 必须 nil(退回 $0),得到 %T", chatDeps.ToolPricingTable)
	}
}
