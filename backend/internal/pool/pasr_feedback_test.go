// pasr_feedback_test.go — PASR-lite A4 反馈闭环测试 (cache → segment 状态)。
package pool

import (
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
)

func TestPASRCacheFeedback_HandlesCreation_SetsBitmap(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	ring := NewAccountRing([]int64{10, 20, 30, 40, 50}, 0xDEADBEEF)
	prefix := "test-prefix-creation"

	// 创建段, 假设段第一个成员就是被选中的账号
	seg := tbl.LookupOrCreate([]byte(prefix), ring)
	chosenAcc := seg.Members[0]
	if chosenAcc == 0 {
		t.Fatal("段成员应被填充")
	}
	idx := seg.IndexOf(chosenAcc)
	if idx != 0 {
		t.Fatalf("idx 应为 0, 实 %d", idx)
	}

	// bitmap 初始全 0
	if seg.HasCache(0) {
		t.Error("bitmap 应初始为 0")
	}

	// 触发 feedback (cache_creation = 1024)
	now := time.Now()
	fb := NewPASRCacheFeedback(tbl, func() time.Time { return now })
	fb.handle(cachemetrics.CacheObservation{
		AccountID:     chosenAcc,
		PrefixHash:    prefix,
		CacheCreation: 1024,
	})

	if !seg.HasCache(0) {
		t.Error("cache_creation > 0 后 bitmap[0] 应被 set")
	}
	if seg.LastWriteAt.Load() != now.UnixNano() {
		t.Error("LastWriteAt 应被更新")
	}
}

func TestPASRCacheFeedback_HandlesRead_RefreshesAge(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	ring := NewAccountRing([]int64{10, 20, 30}, 1)
	prefix := "test-prefix-read"
	seg := tbl.LookupOrCreate([]byte(prefix), ring)
	chosenAcc := seg.Members[0]

	originalLastRead := seg.LastReadAt.Load()
	time.Sleep(2 * time.Millisecond) // 确保 now 比 originalLastRead 至少晚 1ns

	now := time.Now()
	fb := NewPASRCacheFeedback(tbl, func() time.Time { return now })
	fb.handle(cachemetrics.CacheObservation{
		AccountID:  chosenAcc,
		PrefixHash: prefix,
		CacheRead:  500,
	})

	if seg.LastReadAt.Load() <= originalLastRead {
		t.Errorf("LastReadAt 应被刷新 (orig %d, now %d)", originalLastRead, seg.LastReadAt.Load())
	}
	// 仅 cache_read 不应 set bitmap (创建标记是 cache_creation 的语义)
	if seg.HasCache(0) {
		t.Error("仅 cache_read 不应 set bitmap")
	}
}

func TestPASRCacheFeedback_NoOp_IfPrefixEmpty(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	fb := NewPASRCacheFeedback(tbl, nil)
	// 空 prefix → no-op 不 panic
	fb.handle(cachemetrics.CacheObservation{
		AccountID:     42,
		PrefixHash:    "",
		CacheCreation: 100,
	})
	if tbl.Size() != 0 {
		t.Error("空 prefix 不应改 segment table")
	}
}

func TestPASRCacheFeedback_NoOp_IfAccountZero(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	fb := NewPASRCacheFeedback(tbl, nil)
	fb.handle(cachemetrics.CacheObservation{
		AccountID:  0,
		PrefixHash: "p",
		CacheRead:  100,
	})
	if tbl.Size() != 0 {
		t.Error("AccountID=0 不应创建段")
	}
}

func TestPASRCacheFeedback_NoOp_IfSegmentNotFound(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	fb := NewPASRCacheFeedback(tbl, nil)
	// 段不存在 (从未被 LookupOrCreate) → no-op
	fb.handle(cachemetrics.CacheObservation{
		AccountID:     42,
		PrefixHash:    "ghost-prefix",
		CacheCreation: 100,
	})
	// 不应创建段
	if tbl.Size() != 0 {
		t.Errorf("ghost prefix 不应创建段, size=%d", tbl.Size())
	}
}

func TestPASRCacheFeedback_NoOp_IfAccountNotInSegment(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	ring := NewAccountRing([]int64{10, 20, 30}, 1)
	prefix := "p"
	seg := tbl.LookupOrCreate([]byte(prefix), ring)

	fb := NewPASRCacheFeedback(tbl, nil)
	// 用一个不在段成员里的账号 (rebalance 之前发出的请求 stale 例子)
	fb.handle(cachemetrics.CacheObservation{
		AccountID:     999, // 不在 ring/段里
		PrefixHash:    prefix,
		CacheCreation: 100,
	})
	// bitmap 应保持 0 (不能误标他人段员)
	if seg.HasCache(0) || seg.HasCache(1) || seg.HasCache(2) {
		t.Error("非段成员的反馈不应改任何 bit")
	}
}

// 完整闭环 e2e: 注册 observer → 调 ObserveByAccountWithPrefix → 段被更新
func TestPASRCacheFeedback_E2E_RegisterAndObserve(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	ring := NewAccountRing([]int64{42, 99, 100}, 0xC0FFEE)
	prefix := "e2e-test-prefix"
	seg := tbl.LookupOrCreate([]byte(prefix), ring)
	chosenAcc := seg.Members[0]

	// 注册 observer (生产代码就是 RegisterPASRCacheFeedback 这一行)
	fb := NewPASRCacheFeedback(tbl, time.Now)
	cachemetrics.RegisterCacheObserver(fb.Observer())

	// 模拟 proto adapter 在 message_stop 调
	cachemetrics.ObserveByAccountWithPrefix(2048, 0, chosenAcc, prefix)

	// 等观察者同步触发 (本设计是同步调用)
	if !seg.HasCache(0) {
		t.Error("e2e: cache_creation=2048 后 bitmap[0] 应 set, 但未")
	}

	// 第二次同 prefix + 同 acc 但 cache_read > 0 (vendor 缓存命中)
	cachemetrics.ObserveByAccountWithPrefix(0, 1024, chosenAcc, prefix)

	if seg.LastReadAt.Load() == 0 {
		t.Error("e2e: cache_read 后 LastReadAt 应非 0")
	}
}
