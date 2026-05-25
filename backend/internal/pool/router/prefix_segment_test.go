// prefix_segment_test.go — PASR-lite A2 PrefixSegment + SegmentTable 单测。
package router

import (
	"sync"
	"testing"
	"time"
)

func TestPrefixSegment_BitmapBasic(t *testing.T) {
	s := &PrefixSegment{}
	if s.HasCache(0) || s.HasCache(1) || s.HasCache(2) {
		t.Error("初始 bitmap 应全 0")
	}
	s.MarkCacheSeen(1)
	if !s.HasCache(1) {
		t.Error("bit 1 应被 set")
	}
	if s.HasCache(0) || s.HasCache(2) {
		t.Error("仅 bit 1 应被 set")
	}
	if s.CountCached() != 1 {
		t.Errorf("CountCached=%d want 1", s.CountCached())
	}
	s.MarkCacheSeen(0)
	s.MarkCacheSeen(2)
	if s.CountCached() != 3 {
		t.Errorf("3 个 bit set 后 CountCached=%d want 3", s.CountCached())
	}
}

func TestPrefixSegment_BitmapBoundary(t *testing.T) {
	s := &PrefixSegment{}
	s.MarkCacheSeen(-1) // 越界 no-op
	s.MarkCacheSeen(3)  // 越界 no-op
	s.MarkCacheSeen(99) // 越界 no-op
	if s.CountCached() != 0 {
		t.Error("越界 MarkCacheSeen 不应改变 bitmap")
	}
	if s.HasCache(-1) || s.HasCache(3) || s.HasCache(99) {
		t.Error("越界 HasCache 应返 false")
	}
}

func TestPrefixSegment_BitmapIdempotent(t *testing.T) {
	s := &PrefixSegment{}
	for i := 0; i < 10; i++ {
		s.MarkCacheSeen(1)
	}
	if s.CountCached() != 1 {
		t.Errorf("重复 MarkCacheSeen(1) bitmap 应稳定为 0b010, 但 CountCached=%d", s.CountCached())
	}
}

func TestPrefixSegment_IndexOf(t *testing.T) {
	s := &PrefixSegment{Members: [3]int64{42, 99, 100}}
	if s.IndexOf(42) != 0 {
		t.Error("42 应在 idx 0")
	}
	if s.IndexOf(99) != 1 {
		t.Error("99 应在 idx 1")
	}
	if s.IndexOf(100) != 2 {
		t.Error("100 应在 idx 2")
	}
	if s.IndexOf(123) != -1 {
		t.Error("不存在应返 -1")
	}
}

// SegmentTable
// ─────────────────────────────────────────────────────────────────────────

func newTestRing() *AccountRing {
	accs := make([]int64, 50)
	for i := range accs {
		accs[i] = int64(i + 1)
	}
	return NewAccountRing(accs, 0xDEADBEEF)
}

func TestSegmentTable_LookupOrCreate_Fresh(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	ring := newTestRing()
	prefix := []byte("test-prefix-1")

	seg := tbl.LookupOrCreate(1, prefix, ring)
	if seg == nil {
		t.Fatal("应创建新段")
	}
	if seg.Members[0] == 0 {
		t.Error("段成员应被 HRW 填充")
	}
	if tbl.Size() != 1 {
		t.Errorf("size=%d want 1", tbl.Size())
	}
}

func TestSegmentTable_LookupOrCreate_Idempotent(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	ring := newTestRing()
	prefix := []byte("idempotent-test")

	seg1 := tbl.LookupOrCreate(1, prefix, ring)
	seg2 := tbl.LookupOrCreate(1, prefix, ring)
	if seg1 != seg2 {
		t.Error("同 prefix 重复 LookupOrCreate 应返同一指针")
	}
	if tbl.Size() != 1 {
		t.Errorf("size=%d want 1", tbl.Size())
	}
}

func TestSegmentTable_Lookup_OnlyExisting(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	prefix := []byte("never-created")
	if got := tbl.Lookup(1, prefix); got != nil {
		t.Error("Lookup 不应创建; 应返 nil")
	}
	if tbl.Size() != 0 {
		t.Error("Lookup 不应增加 size")
	}
}

func TestSegmentTable_LRU_EvictBack(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{MaxSegments: 3})
	ring := newTestRing()

	for i := 0; i < 5; i++ {
		tbl.LookupOrCreate(1, []byte{byte(i)}, ring)
	}
	if tbl.Size() != 3 {
		t.Errorf("超 cap 后 size=%d want 3", tbl.Size())
	}
	// 前 2 个 prefix (idx 0, 1) 应被 evict
	if got := tbl.Lookup(1, []byte{0}); got != nil {
		t.Error("最老段应被 evict")
	}
	if got := tbl.Lookup(1, []byte{1}); got != nil {
		t.Error("第二老段应被 evict")
	}
	// 后 3 个 prefix (idx 2, 3, 4) 仍在
	for i := 2; i <= 4; i++ {
		if got := tbl.Lookup(1, []byte{byte(i)}); got == nil {
			t.Errorf("prefix %d 不应被 evict", i)
		}
	}
}

func TestSegmentTable_LRU_TouchOnLookup(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{MaxSegments: 3})
	ring := newTestRing()

	// 创建 3 段
	tbl.LookupOrCreate(1, []byte("a"), ring)
	tbl.LookupOrCreate(1, []byte("b"), ring)
	tbl.LookupOrCreate(1, []byte("c"), ring)
	// touch a → a 变最新
	tbl.Lookup(1, []byte("a"))
	// 创建 d → 应 evict b (最老)
	tbl.LookupOrCreate(1, []byte("d"), ring)

	if tbl.Lookup(1, []byte("a")) == nil {
		t.Error("touch 过的 a 不应被 evict")
	}
	if tbl.Lookup(1, []byte("b")) != nil {
		t.Error("最老的 b 应被 evict")
	}
	if tbl.Lookup(1, []byte("c")) == nil || tbl.Lookup(1, []byte("d")) == nil {
		t.Error("c 和 d 都应在")
	}
}

func TestSegmentTable_EvictExpired(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	clock := now
	tbl := NewSegmentTable(SegmentTableConfig{
		MaxAge: 30 * time.Minute,
		Now:    func() time.Time { return clock },
	})
	ring := newTestRing()

	tbl.LookupOrCreate(1, []byte("old"), ring)
	clock = clock.Add(20 * time.Minute) // 段 LastReadAt 是 12:00
	tbl.LookupOrCreate(1, []byte("medium"), ring)
	clock = clock.Add(20 * time.Minute) // 12:40
	tbl.LookupOrCreate(1, []byte("new"), ring)

	// now = 12:40, 老化 cutoff = 12:10
	// "old"=12:00 < 12:10 → 应 evict
	// "medium"=12:20 > 12:10 → 应保留
	// "new"=12:40 > 12:10 → 应保留
	evicted := tbl.EvictExpired(clock)
	if evicted != 1 {
		t.Errorf("evict 数 %d want 1", evicted)
	}
	if tbl.Lookup(1, []byte("old")) != nil {
		t.Error("old 段应被 evict")
	}
	if tbl.Lookup(1, []byte("medium")) == nil {
		t.Error("medium 段应保留")
	}
	if tbl.Lookup(1, []byte("new")) == nil {
		t.Error("new 段应保留")
	}
}

func TestSegmentTable_MarkRead_RefreshesAge(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	clock := now
	tbl := NewSegmentTable(SegmentTableConfig{
		MaxAge: 30 * time.Minute,
		Now:    func() time.Time { return clock },
	})
	ring := newTestRing()

	seg := tbl.LookupOrCreate(1, []byte("p"), ring) // LastReadAt=12:00
	clock = clock.Add(20 * time.Minute)             // now=12:20
	tbl.MarkRead(seg, clock)                        // LastReadAt=12:20
	clock = clock.Add(15 * time.Minute)             // now=12:35

	// cutoff = 12:35 - 30min = 12:05
	// LastReadAt=12:20 > 12:05 → 不应 evict
	evicted := tbl.EvictExpired(clock)
	if evicted != 0 {
		t.Errorf("MarkRead 后不应过期, 但 evict=%d", evicted)
	}
}

func TestSegmentTable_Delete(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	ring := newTestRing()
	tbl.LookupOrCreate(1, []byte("a"), ring)
	tbl.LookupOrCreate(1, []byte("b"), ring)

	if !tbl.Delete(1, []byte("a")) {
		t.Error("Delete a 应返 true")
	}
	if tbl.Delete(1, []byte("a")) {
		t.Error("第二次 Delete 应返 false")
	}
	if tbl.Size() != 1 {
		t.Errorf("size=%d want 1", tbl.Size())
	}
}

func TestSegmentTable_PrefixHashes(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	ring := newTestRing()
	tbl.LookupOrCreate(1, []byte("a"), ring)
	tbl.LookupOrCreate(1, []byte("b"), ring)
	tbl.LookupOrCreate(1, []byte("c"), ring)

	hashes := tbl.PrefixHashes()
	if len(hashes) != 3 {
		t.Errorf("PrefixHashes len=%d want 3", len(hashes))
	}
	// M5b: PrefixHashes 现在返 segmentKey 编码后的 bytes (含 tenant 前缀);
	// 不能直接拆开喂回 Lookup, 因此只验证返回数量。 详细 lookup 验证由
	// 单独的 LookupOrCreate / Lookup 测试覆盖。
}

// 并发安全测试: 100 goroutine 各自 LookupOrCreate 同/不同 prefix +
// MarkRead, race detector 不 fire。
func TestSegmentTable_Concurrent_Race(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{MaxSegments: 1000})
	ring := newTestRing()

	var wg sync.WaitGroup
	for w := 0; w < 100; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				prefix := []byte{byte(workerID % 10), byte(i % 50)}
				seg := tbl.LookupOrCreate(1, prefix, ring)
				idx := i % 3
				seg.MarkCacheSeen(idx)
				if i%5 == 0 {
					tbl.MarkRead(seg, time.Now())
				}
			}
		}(w)
	}
	wg.Wait()

	// 完成后段表应有 (10 workerID-mod) × (50 i-mod) = 500 个唯一 prefix
	// 之内 (实际可能更少因 mod 重叠), 不超 1000 cap
	if got := tbl.Size(); got > 1000 || got == 0 {
		t.Errorf("Concurrent 后 Size=%d 异常 (期望 1..1000)", got)
	}
}

// 段成员从 ring 真选: 验证 LookupOrCreate 调 HRW.Top3 而不是返空 members
func TestSegmentTable_MembersFromHRW(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	ring := newTestRing()
	prefix := []byte("members-test")
	seg := tbl.LookupOrCreate(1, prefix, ring)

	// 验证: 段成员应是 HRW 选出的 top-3 (与 ring.Top3 一致)
	want := ring.Top3(prefix)
	for i, m := range seg.Members {
		if i >= len(want) {
			break
		}
		if m != want[i] {
			t.Errorf("Members[%d]=%d want %d (HRW Top3)", i, m, want[i])
		}
	}
}

// 段表为 0 ring + 任意 prefix → 段成员仍 [3]int64{0,0,0} (HRW.Top3 nil)
func TestSegmentTable_EmptyRing(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	emptyRing := NewAccountRing(nil, 1)
	seg := tbl.LookupOrCreate(1, []byte("p"), emptyRing)
	if seg == nil {
		t.Fatal("空 ring 也应创建段, 段 members 全 0 表示需 fallback")
	}
	for i, m := range seg.Members {
		if m != 0 {
			t.Errorf("空 ring 段成员 [%d]=%d 应为 0", i, m)
		}
	}
}

// =====================================================================
// cache-aware A1: aging 5min default + ExtendedCacheTTL 1h
// =====================================================================

// TestA1_DefaultSegmentMaxAge_Is5Min 锁定常量值, 防回归到 30min。
func TestA1_DefaultSegmentMaxAge_Is5Min(t *testing.T) {
	if DefaultSegmentMaxAge != 5*time.Minute {
		t.Fatalf("DefaultSegmentMaxAge = %v want 5min (Anthropic default cache TTL 对齐)",
			DefaultSegmentMaxAge)
	}
	if DefaultExtendedCacheTTL != 1*time.Hour {
		t.Fatalf("DefaultExtendedCacheTTL = %v want 1h (Anthropic extended cache TTL 对齐)",
			DefaultExtendedCacheTTL)
	}
}

// TestA1_DefaultMaxAge_Evicts5MinOldSegment 不传 MaxAge 用默认 (5min),
// 验证段在 6min 后会被 evict (5min cutoff)。
func TestA1_DefaultMaxAge_Evicts5MinOldSegment(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	clock := now
	tbl := NewSegmentTable(SegmentTableConfig{
		// 不指定 MaxAge → 用 DefaultSegmentMaxAge = 5min
		Now: func() time.Time { return clock },
	})
	ring := newTestRing()

	tbl.LookupOrCreate(1, []byte("p"), ring) // LastReadAt = 12:00

	// 推进 6min: cutoff = 12:06 - 5min = 12:01, LastReadAt=12:00 < 12:01 → evict
	clock = clock.Add(6 * time.Minute)
	evicted := tbl.EvictExpired(clock)
	if evicted != 1 {
		t.Fatalf("默认 5min 老化, 6min 后应 evict 1 段, 实 %d", evicted)
	}
	if tbl.Lookup(1, []byte("p")) != nil {
		t.Error("段应被 evict")
	}
}

// TestA1_ExtendedCacheTTL_KeepsBeyondDefault 标 ExtendedCacheTTL=1h 的段
// 在 30min 后不被 evict (>= 默认 5min, 但 < 1h extended TTL)。
func TestA1_ExtendedCacheTTL_KeepsBeyondDefault(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	clock := now
	tbl := NewSegmentTable(SegmentTableConfig{
		Now: func() time.Time { return clock },
	})
	ring := newTestRing()

	seg := tbl.LookupOrCreate(1, []byte("ext"), ring) // LastReadAt = 12:00
	seg.SetExtendedCacheTTL(1 * time.Hour)            // 标 extended

	// 推进 30min: 默认 5min 早过, 但 extended 1h 还没到
	clock = clock.Add(30 * time.Minute)
	evicted := tbl.EvictExpired(clock)
	if evicted != 0 {
		t.Fatalf("ExtendedCacheTTL=1h 段在 30min 不应 evict, 实 %d", evicted)
	}
	if tbl.Lookup(1, []byte("ext")) == nil {
		t.Error("extended 段不应被 evict")
	}
}

// TestA1_ExtendedCacheTTL_EvictsAfterTTL 标 ExtendedCacheTTL=1h 的段
// 在 65min (> 1h) 后被 evict。
func TestA1_ExtendedCacheTTL_EvictsAfterTTL(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	clock := now
	tbl := NewSegmentTable(SegmentTableConfig{
		Now: func() time.Time { return clock },
	})
	ring := newTestRing()

	seg := tbl.LookupOrCreate(1, []byte("ext"), ring)
	seg.SetExtendedCacheTTL(1 * time.Hour)

	// 推进 65min: 已超 1h extended TTL → evict
	clock = clock.Add(65 * time.Minute)
	evicted := tbl.EvictExpired(clock)
	if evicted != 1 {
		t.Fatalf("ExtendedCacheTTL=1h 段在 65min 应 evict 1 段, 实 %d", evicted)
	}
	if tbl.Lookup(1, []byte("ext")) != nil {
		t.Error("extended 段过期应被清")
	}
}

// TestA1_MixedTTL_PerSegmentEffective 同表内 default + extended 两段,
// 验证 EvictExpired 按段独立 TTL 判。
func TestA1_MixedTTL_PerSegmentEffective(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	clock := now
	tbl := NewSegmentTable(SegmentTableConfig{
		Now: func() time.Time { return clock },
	})
	ring := newTestRing()

	// 段 default: 用默认 5min
	tbl.LookupOrCreate(1, []byte("default"), ring)
	// 段 extended: 标 1h
	segExt := tbl.LookupOrCreate(1, []byte("ext"), ring)
	segExt.SetExtendedCacheTTL(1 * time.Hour)

	// 推进 30min: default 段过期 (5min cutoff), ext 段还活 (1h cutoff)
	clock = clock.Add(30 * time.Minute)
	evicted := tbl.EvictExpired(clock)
	if evicted != 1 {
		t.Fatalf("混合 TTL 30min 后应 evict 1 段 (default), 实 %d", evicted)
	}
	if tbl.Lookup(1, []byte("default")) != nil {
		t.Error("default 段 (5min TTL) 30min 后应被 evict")
	}
	if tbl.Lookup(1, []byte("ext")) == nil {
		t.Error("extended 段 (1h TTL) 30min 后应保留")
	}

	// 推进到 70min (从原始时刻): ext 段也过期
	clock = clock.Add(40 * time.Minute) // total now = 70min after start
	evicted2 := tbl.EvictExpired(clock)
	if evicted2 != 1 {
		t.Fatalf("70min 后 ext 段也应 evict, 实 %d", evicted2)
	}
}

// TestA1_SetExtendedCacheTTL_ZeroResetsToDefault 显式传 0 / 负值 → 还原 default。
func TestA1_SetExtendedCacheTTL_ZeroResetsToDefault(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	clock := now
	tbl := NewSegmentTable(SegmentTableConfig{
		Now: func() time.Time { return clock },
	})
	ring := newTestRing()

	seg := tbl.LookupOrCreate(1, []byte("p"), ring)
	seg.SetExtendedCacheTTL(1 * time.Hour) // 先设 extended
	seg.SetExtendedCacheTTL(0)             // 还原 default
	if seg.ExtendedCacheTTL.Load() != 0 {
		t.Errorf("SetExtendedCacheTTL(0) 应还原 0, 实 %d", seg.ExtendedCacheTTL.Load())
	}
	seg.SetExtendedCacheTTL(-1) // 负值也 reset
	if seg.ExtendedCacheTTL.Load() != 0 {
		t.Errorf("SetExtendedCacheTTL(<0) 应还原 0, 实 %d", seg.ExtendedCacheTTL.Load())
	}

	// 推进 6min → default (5min) 老化生效, evict
	clock = clock.Add(6 * time.Minute)
	if tbl.EvictExpired(clock) != 1 {
		t.Error("ExtendedCacheTTL=0 后应回归 default 5min 老化")
	}
}
