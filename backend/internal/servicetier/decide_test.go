package servicetier

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestSettlePriorityActualChargesDouble(t *testing.T) {
	got := Settle("fast", "priority")
	if got.Billed != "priority" || !got.Multiplier.Equal(two) || got.Pending {
		t.Fatalf("Settle(fast,priority)=%+v want billed=priority 2× settled", got)
	}
}

func TestSettleDowngradeDoesNotChargeFast(t *testing.T) {
	got := Settle("priority", "default")
	if got.Billed != "default" || !got.Multiplier.Equal(one) || got.Pending {
		t.Fatalf("Settle(priority,default)=%+v want 1× default", got)
	}
}

func TestSettleFlexDoesNotUpgradeToFast(t *testing.T) {
	got := Settle("flex", "priority")
	if got.Billed != "flex" || !got.Multiplier.Equal(half) || got.Pending {
		t.Fatalf("Settle(flex,priority)=%+v want 0.5× flex cap", got)
	}
}

func TestSettleAutoFollowsActualFast(t *testing.T) {
	got := Settle("", "priority")
	if got.Billed != "priority" || !got.Multiplier.Equal(two) {
		t.Fatalf("Settle(auto,priority)=%+v want 2× actual", got)
	}
	got = Settle("auto", "priority")
	if got.Billed != "priority" || !got.Multiplier.Equal(two) {
		t.Fatalf("Settle(auto literal,priority)=%+v want 2× actual", got)
	}
}

func TestSettleUnpublishedActualIsPendingNotInvented(t *testing.T) {
	got := Settle("auto", "ultrafast")
	if !got.Pending || !got.Multiplier.Equal(one) || got.Billed != "ultrafast" {
		t.Fatalf("Settle(auto,ultrafast)=%+v want pending 1× without invented premium", got)
	}
	if got.Reason != "unpublished_actual" {
		t.Fatalf("reason=%q want unpublished_actual", got.Reason)
	}
}

func TestSettleUnpublishedActualStillRespectsFlexCap(t *testing.T) {
	got := Settle("flex", "ultrafast")
	if !got.Pending || !got.Multiplier.Equal(half) || got.Billed != "flex" {
		t.Fatalf("Settle(flex,ultrafast)=%+v want pending flex cap", got)
	}
}

func TestReserveKnownFastIsDoubleAutoIsOne(t *testing.T) {
	fast := Reserve("priority")
	if !fast.Multiplier.Equal(two) || fast.Pending {
		t.Fatalf("Reserve(priority)=%+v want 2× hold", fast)
	}
	auto := Reserve("")
	if !auto.Multiplier.Equal(one) || auto.Pending {
		t.Fatalf("Reserve(auto)=%+v want 1× hold", auto)
	}
	unknown := Reserve("ultrafast")
	if !unknown.Multiplier.Equal(one) || unknown.Pending {
		t.Fatalf("Reserve(ultrafast)=%+v want 1× hold without inventing a rate", unknown)
	}
}

func TestSettleMissingActualOnFastIsPendingAtRequested(t *testing.T) {
	got := Settle("fast", "")
	if !got.Pending || !got.Multiplier.Equal(two) {
		t.Fatalf("Settle(fast,missing)=%+v want pending requested ceiling", got)
	}
}

func TestClassifyAliases(t *testing.T) {
	if class, lit := classify(" PRIORITY "); class != laneFast || lit != "priority" {
		t.Fatalf("classify(PRIORITY)=%v %q", class, lit)
	}
	if class, lit := classify("standard"); class != laneStandard || lit != "standard" {
		t.Fatalf("classify(standard)=%v %q", class, lit)
	}
	if !publishedRate(laneFlex).Equal(decimal.RequireFromString("0.5")) {
		t.Fatal("flex published rate must stay 0.5")
	}
}
