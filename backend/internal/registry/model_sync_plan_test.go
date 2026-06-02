package registry

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
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
