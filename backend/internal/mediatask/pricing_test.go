package mediatask

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
)

func TestEstimateCentsUsesPricingEvalPerUnitScaling(t *testing.T) {
	// 变异:把 cents 直接当作 micro-USD,而不是把 cents 转换成 micro-USD;
	// 这会让 123 分的样例返回 0 cents,必须变红。
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
