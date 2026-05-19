// pasr_metrics_test.go — PASR-lite A8 metrics 单测。
package router

import (
	"context"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
)

func TestPASRMetrics_FirstPickAndFailover_Wired(t *testing.T) {
	preFirst := SnapshotPASRMetrics().FirstPickTotal
	preFailover := SnapshotPASRMetrics().FailoverTotal

	// 走 PASR Select happy path → first-pick 应 +1
	accs := []int64{10, 20, 30, 40, 50}
	sel, _, _, _, _ := newPASRTestRig(t, accs)
	req := SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "metrics-fp",
	}
	if _, err := sel.Select(context.Background(), req); err != nil {
		t.Fatalf("Select err=%v", err)
	}
	postFirst := SnapshotPASRMetrics().FirstPickTotal
	postFailover := SnapshotPASRMetrics().FailoverTotal

	// 至少 first-pick OR failover 增加 1 (取决于 cache 状态; 新段无 cache,
	// 走"无 cached 则 candidates 内 RR by load", load 全相等取 idx[0])
	if postFirst+postFailover-preFirst-preFailover != 1 {
		t.Errorf("一次 Select 应 first_pick+failover 总增 1, 实增 %d",
			postFirst+postFailover-preFirst-preFailover)
	}
}

func TestPASRMetrics_FullRingFallback_Wired(t *testing.T) {
	pre := SnapshotPASRMetrics().FullRingFallback
	accs := []int64{10, 20, 30}
	sel, _, ring, src, _ := newPASRTestRig(t, accs)

	// 把段成员都超载触发 full-ring fallback
	prefix := "ring-fallback"
	top3 := ring.Top3([]byte(prefix))
	for _, m := range top3 {
		for _, s := range src.snapshots {
			if s.ID == m {
				s.LoadRate = 0.99
			}
		}
	}

	req := SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: prefix,
	}
	_, _ = sel.Select(context.Background(), req)
	post := SnapshotPASRMetrics().FullRingFallback

	if post-pre != 1 {
		t.Errorf("全段 unhealthy 应触发 full_ring_fallback +1, 实增 %d", post-pre)
	}
}

func TestPASRMetrics_SegmentCreates_Wired(t *testing.T) {
	pre := SnapshotPASRMetrics().SegmentCreatesTotal
	tbl := NewSegmentTable(SegmentTableConfig{})
	ring := NewAccountRing([]int64{1, 2, 3}, 1)

	tbl.LookupOrCreate(1, []byte("create-test-1"), ring)
	tbl.LookupOrCreate(1, []byte("create-test-2"), ring)
	tbl.LookupOrCreate(1, []byte("create-test-1"), ring) // 重复, 不应创建

	post := SnapshotPASRMetrics().SegmentCreatesTotal
	if post-pre != 2 {
		t.Errorf("应新建 2 段 (重复不计), 实 %d", post-pre)
	}
}

func TestPASRMetrics_CacheObs_Wired(t *testing.T) {
	preCreate := SnapshotPASRMetrics().CacheCreationObs
	preHit := SnapshotPASRMetrics().CacheHitObs

	tbl := NewSegmentTable(SegmentTableConfig{})
	ring := NewAccountRing([]int64{42, 99, 100}, 1)
	prefix := "metrics-feedback"
	seg := tbl.LookupOrCreate(1, []byte(prefix), ring)
	chosenAcc := seg.Members[0]

	fb := NewPASRCacheFeedback(tbl, time.Now)
	fb.handle(cachemetrics.CacheObservation{
		TenantID:      1,
		AccountID:     chosenAcc,
		PrefixHash:    prefix,
		CacheCreation: 100,
	})
	fb.handle(cachemetrics.CacheObservation{
		TenantID:   1,
		AccountID:  chosenAcc,
		PrefixHash: prefix,
		CacheRead:  50,
	})

	postCreate := SnapshotPASRMetrics().CacheCreationObs
	postHit := SnapshotPASRMetrics().CacheHitObs

	if postCreate-preCreate != 1 {
		t.Errorf("cache_creation_obs 应 +1, 实增 %d", postCreate-preCreate)
	}
	if postHit-preHit != 1 {
		t.Errorf("cache_hit_obs 应 +1, 实增 %d", postHit-preHit)
	}
}

func TestPASRMetrics_Evictions_Wired(t *testing.T) {
	preEvict := SnapshotPASRMetrics().EvictionsTotal
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	clock := now
	tbl := NewSegmentTable(SegmentTableConfig{
		MaxAge: 30 * time.Minute,
		Now:    func() time.Time { return clock },
	})
	ring := NewAccountRing([]int64{1, 2, 3}, 1)

	tbl.LookupOrCreate(1, []byte("expire-test"), ring)
	clock = clock.Add(40 * time.Minute)

	w := NewPASRAgingWorker(PASRAgingWorkerConfig{
		Segments: tbl, Now: func() time.Time { return clock },
	})
	w.TickOnce()

	postEvict := SnapshotPASRMetrics().EvictionsTotal
	if postEvict-preEvict < 1 {
		t.Errorf("aging worker 应触发至少 1 次 eviction metric, 实增 %d", postEvict-preEvict)
	}
}

func TestPASRMetrics_SegmentCount_Snapshot(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	ring := NewAccountRing([]int64{1, 2, 3}, 1)
	tbl.LookupOrCreate(1, []byte("c1"), ring)
	tbl.LookupOrCreate(1, []byte("c2"), ring)
	tbl.LookupOrCreate(1, []byte("c3"), ring)

	w := NewPASRAgingWorker(PASRAgingWorkerConfig{Segments: tbl})
	w.TickOnce()
	if SnapshotPASRMetrics().SegmentCount != 3 {
		t.Errorf("SegmentCount=%d want 3", SnapshotPASRMetrics().SegmentCount)
	}
}
