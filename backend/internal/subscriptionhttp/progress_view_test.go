// HUAKAI · iKun

package subscriptionhttp

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

// fixedNow is an injected clock so resets_in_seconds is deterministic (no real time.Now).
var fixedNow = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

// windowRead builds a CurrentWindowRead from decimal strings, splitting consumed into
// settled+reserved the same way the production projection sums them (so the derived
// fields are exercised over the real consumed = settled+reserved path).
func windowRead(limit, settled, reserved, overage string, end time.Time) quota.CurrentWindowRead {
	return quota.CurrentWindowRead{
		LimitValue:    decimal.RequireFromString(limit),
		SettledValue:  decimal.RequireFromString(settled),
		ReservedValue: decimal.RequireFromString(reserved),
		OverageValue:  decimal.RequireFromString(overage),
		RequestCount:  3,
		Window: quota.Window{
			Kind:  quota.WindowCalendarDay,
			Start: fixedNow.Add(-time.Hour),
			End:   end,
		},
	}
}

// REGRESSION: a window under cap must report usage_percent=consumed/cap*100,
// over_limit=false, over_limit_amount="0", and a positive resets countdown.
func TestProgressView_UnderCap(t *testing.T) {
	// consumed = 200 settled + 50 reserved = 250 of a 1000 cap → 25%.
	end := fixedNow.Add(time.Hour)
	got := toSubscriptionProgressView(windowRead("1000", "200", "50", "0", end), fixedNow)

	if got.UsagePercent != 25.0 {
		t.Fatalf("usage_percent = %v, want 25.0 (250/1000*100)", got.UsagePercent)
	}
	if got.OverLimit {
		t.Fatalf("over_limit = true, want false for consumed<cap")
	}
	if got.OverLimitAmount != "0" {
		t.Fatalf("over_limit_amount = %q, want \"0\" when under cap", got.OverLimitAmount)
	}
	if got.ResetsInSeconds != 3600 {
		t.Fatalf("resets_in_seconds = %d, want 3600 (window_end is 1h ahead of injected now)", got.ResetsInSeconds)
	}
}

// REGRESSION: a window past cap must report usage_percent>100, over_limit=true, and
// over_limit_amount = consumed − cap (same USD decimal unit as consumed).
func TestProgressView_OverCap(t *testing.T) {
	// consumed = 1100 settled + 100 reserved = 1200 of a 1000 cap → 120%, overage 200.
	end := fixedNow.Add(time.Hour)
	got := toSubscriptionProgressView(windowRead("1000", "1100", "100", "0", end), fixedNow)

	if got.UsagePercent != 120.0 {
		t.Fatalf("usage_percent = %v, want 120.0 (1200/1000*100, not clamped at 100)", got.UsagePercent)
	}
	if !got.OverLimit {
		t.Fatalf("over_limit = false, want true for consumed>cap")
	}
	if got.OverLimitAmount != "200" {
		t.Fatalf("over_limit_amount = %q, want \"200\" (consumed-cap = 1200-1000)", got.OverLimitAmount)
	}
}

// REGRESSION: cap==0 must take the documented guard (usage_percent=0, no divide-by-zero
// panic / Inf / NaN). Any nonzero consumption still flags over_limit.
func TestProgressView_ZeroCapGuard(t *testing.T) {
	end := fixedNow.Add(time.Hour)
	got := toSubscriptionProgressView(windowRead("0", "5", "0", "0", end), fixedNow)

	if got.UsagePercent != 0.0 {
		t.Fatalf("usage_percent = %v, want documented 0 guard when cap==0 (no NaN/Inf)", got.UsagePercent)
	}
	// consumed 5 > cap 0 → still over_limit with overage 5.
	if !got.OverLimit || got.OverLimitAmount != "5" {
		t.Fatalf("over_limit/amount = %v/%q, want true/\"5\" (cap 0, consumed 5)", got.OverLimit, got.OverLimitAmount)
	}
}

// REGRESSION: resets_in_seconds clamps to 0 (never negative) when window_end is already
// in the past relative to the injected clock.
func TestProgressView_ResetsClampsToZeroWhenWindowElapsed(t *testing.T) {
	end := fixedNow.Add(-time.Minute) // window already ended a minute ago
	got := toSubscriptionProgressView(windowRead("1000", "100", "0", "0", end), fixedNow)

	if got.ResetsInSeconds != 0 {
		t.Fatalf("resets_in_seconds = %d, want 0 for an elapsed window (no negative countdown)", got.ResetsInSeconds)
	}
}

// REGRESSION: the pre-existing fields (cap/consumed/remaining/overage/request_count/
// window_start/window_end) are unchanged by the additive derived fields — consumed still
// = settled+reserved, remaining still floors at 0, and overage still mirrors the ledger
// OverageValue (NOT the derived over_limit_amount).
func TestProgressView_ExistingFieldsUnchanged(t *testing.T) {
	end := fixedNow.Add(time.Hour)
	w := windowRead("1000", "1100", "100", "0.25", end) // over cap: consumed 1200, ledger overage 0.25
	got := toSubscriptionProgressView(w, fixedNow)

	if got.Cap != "1000" {
		t.Fatalf("cap = %q, want \"1000\" (unchanged)", got.Cap)
	}
	if got.Consumed != "1200" {
		t.Fatalf("consumed = %q, want \"1200\" (settled+reserved, unchanged)", got.Consumed)
	}
	if got.Remaining != "0" {
		t.Fatalf("remaining = %q, want \"0\" (floored at 0, unchanged)", got.Remaining)
	}
	// Persisted ledger Overage must stay sourced from OverageValue (0.25), NOT replaced by
	// the derived over_limit_amount (200). This guards against the two being conflated.
	if got.Overage != "0.25" {
		t.Fatalf("overage = %q, want \"0.25\" (persisted ledger OverageValue, unchanged)", got.Overage)
	}
	if got.OverLimitAmount != "200" {
		t.Fatalf("over_limit_amount = %q, want \"200\" (derived, distinct from ledger overage)", got.OverLimitAmount)
	}
	if got.RequestCount != 3 {
		t.Fatalf("request_count = %d, want 3 (unchanged)", got.RequestCount)
	}
	if !got.WindowStart.Equal(w.Window.Start) || !got.WindowEnd.Equal(w.Window.End) {
		t.Fatalf("window_start/end mutated: got %v/%v, want %v/%v", got.WindowStart, got.WindowEnd, w.Window.Start, w.Window.End)
	}
}
