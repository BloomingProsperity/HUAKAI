package registry

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

func TestPlanVendorCatalogDisablesOnlyAutoSyncedMissingAliases(t *testing.T) {
	plan, err := planVendorCatalogApply(modelsync.Catalog{
		Vendor: modelsync.VendorAnthropic,
		Models: []modelsync.Model{{
			ID:             "claude-new",
			ProtocolFamily: "anthropic_messages",
			OwnedBy:        "anthropic",
		}},
	}, []vendorAliasState{
		{AliasNormalized: "claude-old", Source: modelSyncSource, Status: "active"},
		{AliasNormalized: "claude-operator", Source: "operator", Status: "active"},
	})
	if err != nil {
		t.Fatalf("planVendorCatalogApply: %v", err)
	}
	if len(plan.Upserts) != 1 || plan.Upserts[0].ID != "claude-new" {
		t.Fatalf("upserts=%+v want one new model", plan.Upserts)
	}
	if len(plan.DisableAliases) != 1 || plan.DisableAliases[0] != "claude-old" {
		t.Fatalf("disabled=%v want only auto-synced missing alias", plan.DisableAliases)
	}
}

func TestPlanVendorCatalogReactivatesReturnedAutoSyncedAlias(t *testing.T) {
	plan, err := planVendorCatalogApply(modelsync.Catalog{
		Vendor: modelsync.VendorGemini,
		Models: []modelsync.Model{{
			ID:             "gemini-returned",
			ProtocolFamily: "gemini",
			OwnedBy:        "google",
		}},
	}, []vendorAliasState{
		{AliasNormalized: "gemini-returned", Source: modelSyncSource, Status: "disabled"},
	})
	if err != nil {
		t.Fatalf("planVendorCatalogApply: %v", err)
	}
	if len(plan.ReactivateAliases) != 1 || plan.ReactivateAliases[0] != "gemini-returned" {
		t.Fatalf("reactivated=%v want returned alias", plan.ReactivateAliases)
	}
	if len(plan.DisableAliases) != 0 {
		t.Fatalf("disabled=%v want none", plan.DisableAliases)
	}
}

// S0 回归:上游 200 但模型列表为空(或全被过滤),不得把现有 active auto-sync
// alias 全部误禁。守护应拒绝(返回 error → 事务回滚 → 保留现状)。
// Mutation:删掉空目录护栏 → 返回禁用全部的 plan 且 err==nil → 本测试变红。
func TestPlanVendorCatalogRefusesEmptyCatalogMassDisable(t *testing.T) {
	_, err := planVendorCatalogApply(modelsync.Catalog{
		Vendor: modelsync.VendorAnthropic,
		Models: nil,
	}, []vendorAliasState{
		{AliasNormalized: "claude-a", Source: modelSyncSource, Status: "active"},
		{AliasNormalized: "claude-b", Source: modelSyncSource, Status: "active"},
	})
	if err == nil {
		t.Fatalf("want error refusing empty-catalog mass-disable, got nil")
	}
}

// 灾难性收缩:基数 >=4 且本次将禁用 >50% active alias(疑似上游部分截断)→ 拒绝。
func TestPlanVendorCatalogRefusesCatastrophicShrink(t *testing.T) {
	_, err := planVendorCatalogApply(modelsync.Catalog{
		Vendor: modelsync.VendorOpenAI,
		Models: []modelsync.Model{{ID: "gpt-keep", ProtocolFamily: "openai_chat", OwnedBy: "openai"}},
	}, []vendorAliasState{
		{AliasNormalized: "gpt-keep", Source: modelSyncSource, Status: "active"},
		{AliasNormalized: "gpt-1", Source: modelSyncSource, Status: "active"},
		{AliasNormalized: "gpt-2", Source: modelSyncSource, Status: "active"},
		{AliasNormalized: "gpt-3", Source: modelSyncSource, Status: "active"},
		{AliasNormalized: "gpt-4", Source: modelSyncSource, Status: "active"},
	})
	if err == nil {
		t.Fatalf("want error refusing catastrophic >50%% shrink, got nil")
	}
}

// 不可误拦:基数小(<4)且目录非空的正常单模型退役,必须放行。
func TestPlanVendorCatalogAllowsSmallLegitimateRetirement(t *testing.T) {
	plan, err := planVendorCatalogApply(modelsync.Catalog{
		Vendor: modelsync.VendorAnthropic,
		Models: []modelsync.Model{{ID: "claude-keep", ProtocolFamily: "anthropic_messages", OwnedBy: "anthropic"}},
	}, []vendorAliasState{
		{AliasNormalized: "claude-keep", Source: modelSyncSource, Status: "active"},
		{AliasNormalized: "claude-retired", Source: modelSyncSource, Status: "active"},
	})
	if err != nil {
		t.Fatalf("small retirement must not be blocked: %v", err)
	}
	if len(plan.DisableAliases) != 1 || plan.DisableAliases[0] != "claude-retired" {
		t.Fatalf("disabled=%v want only claude-retired", plan.DisableAliases)
	}
}

// TestModelSyncProtocolFamilyUsesRegistryFamily 守 model sync 写 models.protocol_family
// 时不得再使用旧客户端协议名。变异证明:把 Gemini 默认值改回 "gemini" 或删掉
// normalizeSyncedProtocolFamily 的兼容折叠 → 本测试红，实际同步会被 CHECK 拒绝。
func TestModelSyncProtocolFamilyUsesRegistryFamily(t *testing.T) {
	if got := defaultProtocolForVendor(modelsync.VendorGemini); got != registrydefault.ProtocolGeminiMessages {
		t.Fatalf("gemini default protocol=%q want %q", got, registrydefault.ProtocolGeminiMessages)
	}
	if got := normalizeSyncedProtocolFamily("gemini"); got != registrydefault.ProtocolGeminiMessages {
		t.Fatalf("normalized protocol=%q want %q", got, registrydefault.ProtocolGeminiMessages)
	}
	if got := defaultProtocolForVendor(modelsync.VendorOpenAI); got != registrydefault.ProtocolOpenAIChat {
		t.Fatalf("openai default protocol=%q want %q", got, registrydefault.ProtocolOpenAIChat)
	}
}
