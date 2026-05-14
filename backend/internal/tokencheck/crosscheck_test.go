package tokencheck

import (
	"math"
	"testing"
)

func TestCrossCheckOK(t *testing.T) {
	got := CrossCheck(100, 100)
	if got.Verdict != VerdictOK {
		t.Fatalf("verdict %q, want %q", got.Verdict, VerdictOK)
	}
	if math.Abs(got.Ratio-1) > 1e-9 {
		t.Fatalf("ratio %.6f, want 1", got.Ratio)
	}
}

func TestCrossCheckWarn(t *testing.T) {
	got := CrossCheck(110, 100)
	if got.Verdict != VerdictWarn5 {
		t.Fatalf("verdict %q, want %q", got.Verdict, VerdictWarn5)
	}
}

func TestCrossCheckFail(t *testing.T) {
	got := CrossCheck(150, 100)
	if got.Verdict != VerdictFail20 {
		t.Fatalf("verdict %q, want %q", got.Verdict, VerdictFail20)
	}
}

func TestCrossCheckUnknown(t *testing.T) {
	got := CrossCheck(0, 100)
	if got.Verdict != VerdictUnknown || got.Ratio != 0 {
		t.Fatalf("unknown mismatch: %+v", got)
	}
}

func TestCrossCheckWarnBoundary(t *testing.T) {
	got := CrossCheck(105, 100)
	if got.Verdict != VerdictWarn5 {
		t.Fatalf("5%% boundary verdict %q, want %q", got.Verdict, VerdictWarn5)
	}
}

func TestCrossCheckFailBoundary(t *testing.T) {
	got := CrossCheck(120, 100)
	if got.Verdict != VerdictFail20 {
		t.Fatalf("20%% boundary verdict %q, want %q", got.Verdict, VerdictFail20)
	}
}

func TestCrossCheckCustomThresholds(t *testing.T) {
	got := CrossCheckWithThresholds(110, 100, Thresholds{WarnRatio: 0.15, FailRatio: 0.30})
	if got.Verdict != VerdictOK {
		t.Fatalf("custom threshold verdict %q, want %q", got.Verdict, VerdictOK)
	}
}
