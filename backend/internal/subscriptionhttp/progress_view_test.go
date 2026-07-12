// HUAKAI · iKun

package subscriptionhttp

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

// fixedNow 是一个注入的时钟,使 resets_in_seconds 具有确定性(不用真实的 time.Now)。
var fixedNow = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

// windowRead 从十进制字符串构建一个 CurrentWindowRead,按生产投影求和的相同方式
// 把 consumed 拆为 settled+reserved(这样派生字段就是在真实的
// consumed = settled+reserved 路径上被测到的)。
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

// 回归:一个未超上限的窗口必须报告 usage_percent=consumed/cap*100、
// over_limit=false、over_limit_amount="0",以及一个正的重置倒计时。
func TestProgressView_UnderCap(t *testing.T) {
	// consumed = 200 settled + 50 reserved = 250,上限 1000 → 25%。
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

// 回归:一个超过上限的窗口必须报告 usage_percent>100、over_limit=true,以及
// over_limit_amount = consumed − cap(与 consumed 同为 USD 十进制单位)。
func TestProgressView_OverCap(t *testing.T) {
	// consumed = 1100 settled + 100 reserved = 1200,上限 1000 → 120%,超额 200。
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

// 回归:cap==0 必须走文档化的守卫(usage_percent=0,不出现除零 panic / Inf / NaN)。
// 任何非零的消耗仍然标记 over_limit。
func TestProgressView_ZeroCapGuard(t *testing.T) {
	end := fixedNow.Add(time.Hour)
	got := toSubscriptionProgressView(windowRead("0", "5", "0", "0", end), fixedNow)

	if got.UsagePercent != 0.0 {
		t.Fatalf("usage_percent = %v, want documented 0 guard when cap==0 (no NaN/Inf)", got.UsagePercent)
	}
	// consumed 5 > cap 0 → 仍然 over_limit,超额 5。
	if !got.OverLimit || got.OverLimitAmount != "5" {
		t.Fatalf("over_limit/amount = %v/%q, want true/\"5\" (cap 0, consumed 5)", got.OverLimit, got.OverLimitAmount)
	}
}

// 回归:当 window_end 相对注入时钟已是过去时,resets_in_seconds 钳到 0
// (绝不为负)。
func TestProgressView_ResetsClampsToZeroWhenWindowElapsed(t *testing.T) {
	end := fixedNow.Add(-time.Minute) // 窗口已在一分钟前结束
	got := toSubscriptionProgressView(windowRead("1000", "100", "0", "0", end), fixedNow)

	if got.ResetsInSeconds != 0 {
		t.Fatalf("resets_in_seconds = %d, want 0 for an elapsed window (no negative countdown)", got.ResetsInSeconds)
	}
}

// 回归:既有字段(cap/consumed/remaining/overage/request_count/window_start/
// window_end)不因新增的派生字段而改变 —— consumed 仍为 settled+reserved,
// remaining 仍以 0 为下限,overage 仍镜像 ledger 的 OverageValue
// (「不是」派生的 over_limit_amount)。
func TestProgressView_ExistingFieldsUnchanged(t *testing.T) {
	end := fixedNow.Add(time.Hour)
	w := windowRead("1000", "1100", "100", "0.25", end) // 超上限:consumed 1200,ledger 超额 0.25
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
	// 持久化的 ledger Overage 必须仍来源于 OverageValue(0.25),「不能」被派生的
	// over_limit_amount(200)替换。这防止二者被混为一谈。
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
