package userkeycontrols

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

// fakeProgressReader is a minimal quota.ProgressReadStore for unit testing KEY-007.
type fakeProgressReader struct {
	windows []quota.CurrentWindowRead
}

func (f *fakeProgressReader) ListCurrentWindowsForScope(_ context.Context, _ int64, _ quota.ScopeKind, _ string, _ time.Time) ([]quota.CurrentWindowRead, error) {
	return f.windows, nil
}

// TestKeyQuotaUsed is the discriminating test for KEY-007.
//
// MUTATION: reader sums only SettledValue (drops ReservedValue) -> used_usd would
// be 2.5 instead of 3.5 -> RED.
func TestKeyQuotaUsed(t *testing.T) {
	// fakeStore.GetAPIKeyQuotaPolicy returns LimitUSD=1 by default; we'll
	// use settled=0.5 + reserved=0.5 = 1.0 used so remaining = 0.
	// To test with meaningful numbers, drive settled=0.25 + reserved=0.25 = 0.5 used.
	settledDec := decimal.RequireFromString("0.25")
	reservedDec := decimal.RequireFromString("0.25")
	wantUsed := decimal.RequireFromString("0.5")
	// fakeStore returns LimitUSD=1
	wantRemaining := decimal.RequireFromString("0.5")

	store := newFakeStore()
	reader := &fakeProgressReader{
		windows: []quota.CurrentWindowRead{
			{
				TenantID:      1,
				Scope:         quota.Scope{Kind: quota.ScopeAPIKey, ID: "3"},
				SettledValue:  settledDec,
				ReservedValue: reservedDec,
			},
		},
	}
	svc := newServiceForTest(store, nil)
	svc.progressRead = reader

	view, err := svc.GetKeyQuota(context.Background(), 11, 22, 33)
	if err != nil {
		t.Fatalf("GetKeyQuota: %v", err)
	}

	if !view.UsedUSD.Equal(wantUsed) {
		t.Errorf("MUTATION: used_usd = %s, want %s (settled+reserved must both be summed)", view.UsedUSD, wantUsed)
	}
	if view.RemainingUSD == nil {
		t.Fatal("remaining_usd must be set when LimitUSD > 0")
	}
	if !view.RemainingUSD.Equal(wantRemaining) {
		t.Errorf("remaining_usd = %s, want %s", *view.RemainingUSD, wantRemaining)
	}

	// No window rows -> used = 0
	svc2 := newServiceForTest(store, nil)
	svc2.progressRead = &fakeProgressReader{windows: nil}
	view2, err := svc2.GetKeyQuota(context.Background(), 11, 22, 33)
	if err != nil {
		t.Fatalf("GetKeyQuota no-window: %v", err)
	}
	if !view2.UsedUSD.IsZero() {
		t.Errorf("no-window: used_usd should be 0, got %s", view2.UsedUSD)
	}
	if view2.RemainingUSD == nil || !view2.RemainingUSD.Equal(decimal.NewFromInt(1)) {
		t.Errorf("no-window: remaining_usd should equal limit=1, got %v", view2.RemainingUSD)
	}
}
