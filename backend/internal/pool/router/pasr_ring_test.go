// pasr_selector_ring_test.go — PASR-lite main-wire M5 atomic 单测。
//
// 覆盖 request-scoped AccountRing (synthesis D3 选项 A):
//
//	T-M5-1 BuildAccountRingFromSnapshots — 单独 helper 行为正确 (去重 + 排序)
//	T-M5-2 PASRSelector RingProvider=nil + Accounts 注入 → 自动 request-scoped ring
//	T-M5-3 不同 tenant 的 ListAccounts 返不同 set → ring 也对应不同, 无跨租户泄漏
//	T-M5-4 ListAccounts 返空 → ErrNoEligibleAccount (不 panic, 不漏数据)
//	T-M5-5 RingSeed 显式注入 → 影响 HRW 排序 (与默认 seed 不同结果)
package router

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestM5_BuildAccountRingFromSnapshots_DedupAndSort(t *testing.T) {
	now := time.Now()
	snaps := []*AccountSnapshot{
		{ID: 30, LastUsedAt: now},
		{ID: 10, LastUsedAt: now},
		{ID: 30, LastUsedAt: now}, // duplicate
		nil,                       // nil 应被忽略
		{ID: 0, LastUsedAt: now},  // 0 应被忽略
		{ID: 20, LastUsedAt: now},
	}
	ring := BuildAccountRingFromSnapshots(snaps, 0xCAFEBABE)
	if ring == nil {
		t.Fatalf("ring nil")
	}
	want := []int64{10, 20, 30}
	if len(ring.Accounts) != len(want) {
		t.Fatalf("len=%d want %d (got %v)", len(ring.Accounts), len(want), ring.Accounts)
	}
	for i, id := range want {
		if ring.Accounts[i] != id {
			t.Errorf("idx %d got %d want %d", i, ring.Accounts[i], id)
		}
	}
}

func TestM5_BuildAccountRingFromSnapshots_EmptyReturnsEmpty(t *testing.T) {
	ring := BuildAccountRingFromSnapshots(nil, 0xCAFEBABE)
	if ring == nil || len(ring.Accounts) != 0 {
		t.Errorf("空 snapshots 应返空 ring (非 nil), got %v", ring)
	}
}

func TestM5_PASRSelector_NoRingProvider_UsesRequestScopedRing(t *testing.T) {
	// RingProvider=nil 触发 request-scoped 路径
	now := time.Now()
	snaps := []*AccountSnapshot{
		{ID: 1, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
		{ID: 2, LoadRate: 0.2, Priority: 1, LastUsedAt: now},
		{ID: 3, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
	}
	src := &fakeAccountSource{snapshots: snaps}
	cg := &fakeClaimGate{}
	tbl := NewSegmentTable(SegmentTableConfig{})
	sel, err := NewPASRSelector(PASRSelectorConfig{
		Accounts: src,
		Claims:   cg,
		Segments: tbl,
		LoadCap:  0.95,
		// RingProvider 故意不注入 — M5 应走 request-scoped path
	})
	if err != nil {
		t.Fatalf("NewPASRSelector 应允许 RingProvider=nil (M5 起非必填), err=%v", err)
	}

	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "m5-no-rp",
	})
	if err != nil {
		t.Fatalf("Select 应成功 (request-scoped ring), err=%v", err)
	}
	if res.AccountID == 0 {
		t.Errorf("AccountID 不应为 0")
	}
	// 选中的 account 必须在原始 snapshot 集合内
	allowed := map[int64]bool{1: true, 2: true, 3: true}
	if !allowed[res.AccountID] {
		t.Errorf("选中 %d 不在 snapshots {1,2,3} 集合内", res.AccountID)
	}
}

func TestM5_PASRSelector_TenantIsolation_NoCrossTenantLeak(t *testing.T) {
	// fakeAccountSource 一直返同一 set; 但我们模拟 per-tenant 过滤通过
	// 切换 source.snapshots — 第二次只返 tenant 2 的 account。
	now := time.Now()
	src := &fakeAccountSource{snapshots: []*AccountSnapshot{
		{ID: 11, TenantID: 1, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
		{ID: 12, TenantID: 1, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
	}}
	cg := &fakeClaimGate{}
	tbl := NewSegmentTable(SegmentTableConfig{})
	sel, err := NewPASRSelector(PASRSelectorConfig{
		Accounts: src,
		Claims:   cg,
		Segments: tbl,
		LoadCap:  0.95,
	})
	if err != nil {
		t.Fatalf("NewPASRSelector: %v", err)
	}

	// tenant 1 路径 — 选中应在 {11, 12}
	res1, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "tenant-1",
	})
	if err != nil {
		t.Fatalf("tenant 1 Select 失败: %v", err)
	}
	if res1.AccountID != 11 && res1.AccountID != 12 {
		t.Errorf("tenant 1 选中 %d 应在 {11,12}", res1.AccountID)
	}

	// 模拟 ListAccounts 切换为 tenant 2 的可见账号集
	src.snapshots = []*AccountSnapshot{
		{ID: 21, TenantID: 2, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
		{ID: 22, TenantID: 2, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
	}
	// 用一个新段 prefix 强制走 LookupOrCreate (避开段 cache 复用之前的 ring)
	res2, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 2, ClaimID: 2, RequestedModel: "m", SessionHash: "tenant-2-fresh-prefix",
	})
	if err != nil {
		t.Fatalf("tenant 2 Select 失败: %v", err)
	}
	if res2.AccountID != 21 && res2.AccountID != 22 {
		t.Errorf("tenant 2 选中 %d 应在 {21,22}, 不应跨租户拿 tenant 1 的 11/12", res2.AccountID)
	}
	if res2.AccountID == 11 || res2.AccountID == 12 {
		t.Errorf("跨租户泄漏: tenant 2 选了 tenant 1 的 account %d", res2.AccountID)
	}
}

func TestM5_PASRSelector_EmptyListAccounts_ReturnsErrNoEligible(t *testing.T) {
	src := &fakeAccountSource{snapshots: nil}
	tbl := NewSegmentTable(SegmentTableConfig{})
	sel, err := NewPASRSelector(PASRSelectorConfig{
		Accounts: src,
		Segments: tbl,
	})
	if err != nil {
		t.Fatalf("NewPASRSelector: %v", err)
	}

	_, err = sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 1, SessionHash: "empty",
	})
	if err == nil {
		t.Fatalf("空 ListAccounts 应返错误, 实际成功")
	}
	if !errors.Is(err, ErrNoEligibleAccount) {
		t.Errorf("err=%v want ErrNoEligibleAccount", err)
	}
}

func TestM5_PASRSelector_RingSeed_AffectsHRWOrder(t *testing.T) {
	// 不同 seed 在同 prefix 下应产生不同 HRW 排序 (高概率)
	now := time.Now()
	snaps := []*AccountSnapshot{
		{ID: 1, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
		{ID: 2, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
		{ID: 3, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
		{ID: 4, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
		{ID: 5, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
	}

	mkSel := func(seed uint64) *PASRSelector {
		src := &fakeAccountSource{snapshots: snaps}
		tbl := NewSegmentTable(SegmentTableConfig{})
		sel, err := NewPASRSelector(PASRSelectorConfig{
			Accounts: src,
			Segments: tbl,
			RingSeed: seed,
		})
		if err != nil {
			t.Fatalf("NewPASRSelector: %v", err)
		}
		return sel
	}

	sel1 := mkSel(0xCAFEBABE)
	sel2 := mkSel(0xDEADBEEF)

	// 用相同 SessionHash 走两个 selector, HRW Top-1 应该高概率不同
	// (shadow 比对本质是 LoadRate 0.1 全相等 → 段内 idx 0 决定 — 段成员
	// 顺序由 HRW seed 决定)
	differCount := 0
	for i := 0; i < 50; i++ {
		req := SelectionRequest{
			TenantID: 1, ClaimID: int64(i + 1), RequestedModel: "m",
			// 不用实时时钟, 避免系统时钟粒度导致 prefix 重复而让本测试 flaky。
			SessionHash: fmt.Sprintf("ring-seed-prefix-%02d", i), // 每次新 prefix 防段 cache
		}
		r1, err1 := sel1.Select(context.Background(), req)
		r2, err2 := sel2.Select(context.Background(), req)
		if err1 != nil || err2 != nil {
			continue
		}
		if r1.AccountID != r2.AccountID {
			differCount++
		}
	}
	// 不同 seed + 5 account, 50 次至少应有 ≥10% 不一致 (5 accounts → 4/5 概率不同)
	if differCount < 5 {
		t.Errorf("不同 seed 应产生不同 HRW 排序, 50 次只有 %d 次差异 (<5)", differCount)
	}
}
