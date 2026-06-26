package main

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

// catalogTestRegistry 构造一个含三类工具的 registry:只读、可提议 mutating、不可提议 mutating。
// ReadOnlyCatalog/ProposableCatalog 只看标志(ReadOnly/Mutating/Proposable),不依赖 Run/Resolve/Mutate,
// 故这里只需注册标志即可驱动目录构建。
func catalogTestRegistry() *hermesops.Registry {
	reg := hermesops.NewRegistry()
	reg.Register(hermesops.ToolSpec{Name: "log_analyze", Category: hermesops.CategoryDiagnostic, ReadOnly: true})
	reg.Register(hermesops.ToolSpec{Name: "account_pause", Category: hermesops.CategoryMutating, Mutating: true, Proposable: true, RequiresConfirmation: true})
	reg.Register(hermesops.ToolSpec{Name: "renew_trigger", Category: hermesops.CategoryMutating, Mutating: true})
	return reg
}

func catalogEntry(cat []map[string]any, name string) map[string]any {
	for _, e := range cat {
		if e["name"] == name {
			return e
		}
	}
	return nil
}

func TestHermesToolCatalogProvider_ProposeDisabledHidesMutating(t *testing.T) {
	// 抓的缺陷(零行为变):proposeEnabled=false 时若误用 ProposableCatalog,mutating 工具名会泄露给
	// LLM。关时只应注入只读目录:只有 log_analyze,且条目不带 mutating / requires_confirmation 键
	//(与提议接入前逐字节一致)。
	// 变异(已验证转红):把 ToolCatalog 的分支改成恒取 ProposableCatalog → account_pause 出现 → 红。
	cat := hermesToolCatalogProvider{reg: catalogTestRegistry(), proposeEnabled: false}.ToolCatalog()

	if catalogEntry(cat, "account_pause") != nil || catalogEntry(cat, "renew_trigger") != nil {
		t.Fatalf("proposeEnabled=false 泄露了 mutating 工具: %+v", cat)
	}
	ro := catalogEntry(cat, "log_analyze")
	if ro == nil {
		t.Fatalf("只读工具 log_analyze 缺失: %+v", cat)
	}
	if _, ok := ro["mutating"]; ok {
		t.Fatalf("只读条目带了 mutating 键: %+v", ro)
	}
	if _, ok := ro["requires_confirmation"]; ok {
		t.Fatalf("只读条目带了 requires_confirmation 键: %+v", ro)
	}
}

func TestHermesToolCatalogProvider_ProposeEnabledShowsProposableMutatingWithFlags(t *testing.T) {
	// 抓的缺陷:proposeEnabled=true 时(a)可提议 mutating 工具必须出现且带 mutating + requires_confirmation
	// 标志(runner 据此走 mode=propose + 渲染确认);(b)不可提议 mutating(renew_trigger)必须仍被结构性
	// 排除,绝不展示给 LLM;(c)只读工具仍在且不带标志。
	// 变异(已验证转红):删掉 entry["mutating"]=true → 标志断言红;若 ProposableCatalog 漏排 renew_trigger
	// → renew_trigger 泄露断言红。
	cat := hermesToolCatalogProvider{reg: catalogTestRegistry(), proposeEnabled: true}.ToolCatalog()

	ap := catalogEntry(cat, "account_pause")
	if ap == nil {
		t.Fatalf("proposeEnabled=true 缺可提议工具 account_pause: %+v", cat)
	}
	if ap["mutating"] != true || ap["requires_confirmation"] != true {
		t.Fatalf("可提议 mutating 工具缺标志: %+v", ap)
	}
	if catalogEntry(cat, "renew_trigger") != nil {
		t.Fatalf("不可提议 mutating 工具 renew_trigger 泄露给 LLM: %+v", cat)
	}
	ro := catalogEntry(cat, "log_analyze")
	if ro == nil {
		t.Fatalf("只读工具仍应在可提议目录中: %+v", cat)
	}
	if _, ok := ro["mutating"]; ok {
		t.Fatalf("只读条目不应带 mutating 键: %+v", ro)
	}
}

func TestHermesToolCatalogProvider_NilRegistryReturnsNil(t *testing.T) {
	// registry 为 nil → 返回 nil(不注入目录),不 panic;两种 KNOB 取值都成立。
	if got := (hermesToolCatalogProvider{reg: nil, proposeEnabled: true}).ToolCatalog(); got != nil {
		t.Fatalf("nil registry 应返回 nil, got %+v", got)
	}
	if got := (hermesToolCatalogProvider{reg: nil, proposeEnabled: false}).ToolCatalog(); got != nil {
		t.Fatalf("nil registry 应返回 nil, got %+v", got)
	}
}
