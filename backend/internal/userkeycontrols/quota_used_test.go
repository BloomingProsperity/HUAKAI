package userkeycontrols

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

// fakeProgressReader 是用于 KEY-007 单元测试的最小 quota.ProgressReadStore。
type fakeProgressReader struct {
	windows []quota.CurrentWindowRead
}

func (f *fakeProgressReader) ListCurrentWindowsForScope(_ context.Context, _ int64, _ quota.ScopeKind, _ string, _ time.Time) ([]quota.CurrentWindowRead, error) {
	return f.windows, nil
}

// TestKeyQuotaUsed 是 KEY-007 的判别性测试。
//
// 变异:reader 只求和 SettledValue(丢掉 ReservedValue)-> used_usd 会变成
// 2.5 而非 3.5 -> 变红。
func TestKeyQuotaUsed(t *testing.T) {
	// fakeStore.GetAPIKeyQuotaPolicy 默认返回 LimitUSD=1;我们将用
	// settled=0.5 + reserved=0.5 = 1.0 used 使 remaining = 0。
	// 为用有意义的数字测试,设 settled=0.25 + reserved=0.25 = 0.5 used。
	settledDec := decimal.RequireFromString("0.25")
	reservedDec := decimal.RequireFromString("0.25")
	wantUsed := decimal.RequireFromString("0.5")
	// fakeStore 返回 LimitUSD=1
	wantRemaining := decimal.RequireFromString("0.5")
	// 两个成本窗口:window_end 必须呈现「最近」的重置边界,而较早的 end 落在
	// 「第二个」窗口上,因此该断言既能让朴素的 windows[0] 取法失败(顺序无关),
	// 也能让取错方向(取最晚)的取法失败。
	wantWindowEnd := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	laterWindowEnd := wantWindowEnd.Add(48 * time.Hour)

	store := newFakeStore()
	reader := &fakeProgressReader{
		windows: []quota.CurrentWindowRead{
			{
				TenantID:      1,
				Scope:         quota.Scope{Kind: quota.ScopeAPIKey, ID: "3"},
				SettledValue:  settledDec,
				ReservedValue: reservedDec,
				Window:        quota.Window{End: laterWindowEnd},
			},
			{
				TenantID:      1,
				Scope:         quota.Scope{Kind: quota.ScopeAPIKey, ID: "3"},
				SettledValue:  decimal.Zero,
				ReservedValue: decimal.Zero,
				Window:        quota.Window{End: wantWindowEnd},
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
	// window_end 必须呈现各窗口中最近的重置边界。
	// 变异:不填充 WindowEnd -> nil -> 变红;取最晚/第一个窗口而非最早的
	// -> laterWindowEnd != wantWindowEnd -> 变红。
	if view.WindowEnd == nil {
		t.Fatal("window_end must be set when a quota window exists")
	}
	if !view.WindowEnd.Equal(wantWindowEnd) {
		t.Errorf("window_end = %s, want %s (soonest reset across windows, order-independent)", view.WindowEnd, wantWindowEnd)
	}

	// 无窗口行 -> used = 0
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
	if view2.WindowEnd != nil {
		t.Errorf("no-window: window_end should be nil, got %v", view2.WindowEnd)
	}
}
