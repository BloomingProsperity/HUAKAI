package servicetier

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
)

func TestApplyScalesTokenAndCacheAndMarksSnapshot(t *testing.T) {
	base := pricingeval.Result{
		Total:             decimal.RequireFromString("0.008"),
		CacheCreationCost: decimal.RequireFromString("0.001"),
		CacheReadCost:     decimal.RequireFromString("0.002"),
		CostSnapshot:      "flat",
	}
	got := Apply(base, Decision{Requested: "fast", Actual: "priority", Billed: "priority", Multiplier: two, Reason: "actual"})
	if !got.Total.Equal(decimal.RequireFromString("0.016")) {
		t.Fatalf("Total=%s want 0.016", got.Total)
	}
	if !got.CacheCreationCost.Equal(decimal.RequireFromString("0.002")) || !got.CacheReadCost.Equal(decimal.RequireFromString("0.004")) {
		t.Fatalf("cache costs=%s/%s want 0.002/0.004", got.CacheCreationCost, got.CacheReadCost)
	}
	if got.PendingReconciliation {
		t.Fatal("known published lane must not force pending")
	}
	for _, mark := range []string{
		"service_tier_requested=fast",
		"service_tier_actual=priority",
		"service_tier_billed=priority",
		"service_tier_mult=2",
	} {
		if !strings.Contains(got.CostSnapshot, mark) {
			t.Fatalf("CostSnapshot=%q missing %s", got.CostSnapshot, mark)
		}
	}
	// 变异：若只缩放 Total 不缩放缓存，cache_read 会仍是 0.002。
	if got.CacheReadCost.Equal(base.CacheReadCost) {
		t.Fatal("cache read was not scaled")
	}
}

func TestApplyUnpublishedPendingDoesNotInventPremium(t *testing.T) {
	base := pricingeval.Result{Total: decimal.RequireFromString("0.008"), CostSnapshot: "flat"}
	got := Apply(base, Settle("auto", "ultrafast"))
	if !got.PendingReconciliation {
		t.Fatal("unpublished actual must be pending")
	}
	if !got.Total.Equal(base.Total) {
		t.Fatalf("Total=%s want unchanged 1× provisional", got.Total)
	}
	if !strings.Contains(got.CostSnapshot, snapPending) {
		t.Fatalf("CostSnapshot=%q want pending marker", got.CostSnapshot)
	}
}

func TestParseBilledRoundTripAndLegacyIdentity(t *testing.T) {
	d := Settle("flex", "priority")
	snap := AppendSnapshot("flat", d)
	got, ok := ParseBilled(snap)
	if !ok {
		t.Fatal("ParseBilled should see markers")
	}
	if got.Billed != "flex" || !got.Multiplier.Equal(half) {
		t.Fatalf("ParseBilled=%+v want flex 0.5", got)
	}
	legacy, found := ParseBilled("flat")
	if found {
		t.Fatal("legacy snapshot must report not-found")
	}
	if !legacy.Multiplier.Equal(one) {
		t.Fatalf("legacy identity mult=%s want 1", legacy.Multiplier)
	}
}

func TestSanitizeDropsUnsafeSnapshotBytes(t *testing.T) {
	got := AppendSnapshot("", Decision{Requested: "fast;token=sk-secret", Billed: "priority", Multiplier: two, Reason: "actual"})
	if strings.Contains(got, "sk-") || strings.Contains(got, "token=") {
		t.Fatalf("snapshot leaked unsafe bytes: %q", got)
	}
}
