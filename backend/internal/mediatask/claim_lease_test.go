package mediatask

import (
	"testing"
	"time"
)

// TestResolveClaimLeaseWindow 锁定 billing claim 孤儿回收租约窗口的安全下限:
// 缺省(<=0)必须回退到一个 > 默认 TaskTimeout(15min)的值,绝不回到 90s 那种
// 短于任务生命周期的窗口(否则 billing LeaseSweeper 会提前 abort 合法长任务的
// claim 致亏钱)。
// Mutation:把回退值改成 90 * time.Second,第一条断言(>= 15min)转红。
func TestResolveClaimLeaseWindow(t *testing.T) {
	// 缺省回退必须覆盖默认 TaskTimeout。
	if got := resolveClaimLeaseWindow(0); got < 15*time.Minute {
		t.Fatalf("缺省 claim lease 窗口=%s 必须 >= 默认 TaskTimeout 15min", got)
	}
	if got := resolveClaimLeaseWindow(-time.Second); got != defaultMediaClaimLeaseWindow {
		t.Fatalf("非正值应回退 defaultMediaClaimLeaseWindow=%s, got %s", defaultMediaClaimLeaseWindow, got)
	}
	// 调用方传入的有效窗口原样透传(已由 service 保证 > TaskTimeout)。
	if got := resolveClaimLeaseWindow(22 * time.Minute); got != 22*time.Minute {
		t.Fatalf("正窗口应透传, got %s", got)
	}
	// 防回归:默认回退窗口本身必须 > 默认 TaskTimeout。
	if defaultMediaClaimLeaseWindow <= 15*time.Minute {
		t.Fatalf("defaultMediaClaimLeaseWindow=%s 必须 > 默认 TaskTimeout 15min", defaultMediaClaimLeaseWindow)
	}
}
