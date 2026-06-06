package mediatask

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
)

func TestEstimateCentsUsesPricingEvalPerUnitScaling(t *testing.T) {
	// Mutation: treat cents as micro-USD directly instead of converting cents to
	// micro-USD; this returns 0 cents for the 123-cent fixture and must go red.
	cfg := Config{DefaultEstimatedCents: map[string]int64{"image_generation": 123}}

	got, err := EstimateCents(context.Background(), cfg, "image_generation")
	if err != nil {
		t.Fatalf("EstimateCents: %v", err)
	}
	if got != 123 {
		t.Fatalf("estimated cents=%d want 123", got)
	}
}

func TestEstimateCentsRejectsMissingTaskType(t *testing.T) {
	cfg := Config{DefaultEstimatedCents: map[string]int64{"image_generation": 100}}

	if _, err := EstimateCents(context.Background(), cfg, "video_generation"); err == nil {
		t.Fatal("missing default estimate returned nil error")
	}
}

func TestCentsRoundTripUSD(t *testing.T) {
	got := centsToUSD(123)
	if !got.Equal(decimal.RequireFromString("1.23")) {
		t.Fatalf("centsToUSD(123)=%s want 1.23", got)
	}
	if usdToCents(decimal.RequireFromString("1.239")) != 124 {
		t.Fatal("usdToCents must round half up to the nearest cent")
	}
}
