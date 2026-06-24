package credentialstore

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Pure-helper unit tests (no DB required)
// ---------------------------------------------------------------------------

// TestIneffectiveRefreshNextAttemptEffective verifies the DEFAULT/SAFE path:
// when refreshBeforeAt is in the future the helper returns normalNext unchanged.
// Mutation self-check: if the helper returns now+backoff even in the effective
// case this test goes RED.
func TestIneffectiveRefreshNextAttemptEffective(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	refreshBeforeAt := now.Add(5 * time.Minute) // in the future -> effective
	normalNext := time.Time{}                   // NULL / zero sentinel
	got := ineffectiveRefreshNextAttempt(refreshBeforeAt, now, normalNext)
	if got != normalNext {
		t.Fatalf("effective refresh: got next_attempt_at=%v, want normalNext=%v (MUST be unchanged)", got, normalNext)
	}
}

// TestIneffectiveRefreshNextAttemptIneffectiveExact verifies refreshBeforeAt == now
// (boundary: exactly now) is treated as ineffective.
func TestIneffectiveRefreshNextAttemptIneffectiveExact(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	refreshBeforeAt := now // == now -> immediately due -> ineffective
	got := ineffectiveRefreshNextAttempt(refreshBeforeAt, now, time.Time{})
	want := now.Add(IneffectiveRefreshBackoff)
	if got != want {
		t.Fatalf("ineffective (exact now): got=%v want=%v", got, want)
	}
}

// TestIneffectiveRefreshNextAttemptIneffectivePast verifies refreshBeforeAt in
// the past is treated as ineffective.
func TestIneffectiveRefreshNextAttemptIneffectivePast(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	refreshBeforeAt := now.Add(-1 * time.Minute) // past -> ineffective
	got := ineffectiveRefreshNextAttempt(refreshBeforeAt, now, time.Time{})
	want := now.Add(IneffectiveRefreshBackoff)
	if got != want {
		t.Fatalf("ineffective (past): got=%v want=%v", got, want)
	}
}

// TestIneffectiveRefreshBackoffValue asserts the const is exactly 30s so a
// reviewer immediately sees a drift if changed carelessly.
func TestIneffectiveRefreshBackoffValue(t *testing.T) {
	if IneffectiveRefreshBackoff != 30*time.Second {
		t.Fatalf("IneffectiveRefreshBackoff=%v want 30s", IneffectiveRefreshBackoff)
	}
}
