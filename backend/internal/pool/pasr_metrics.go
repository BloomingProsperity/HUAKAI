// pasr_metrics.go — PASR-lite A8 metrics: per-segment + global PASR 行为
// 经 expvar /debug/vars 暴露给运维。
//
// 暴露指标:
//   pasr_segment_count            当前段表大小
//   pasr_evictions_total          累计被 evict 段数 (LRU + 老化合计)
//   pasr_first_pick_total         schedule 时段内 candidates[0] 被选 (cache 命中路径)
//   pasr_failover_total           schedule 时 candidates[0] 不可用回退到段内 [1] [2]
//   pasr_full_ring_fallback_total 段全 unhealthy 走 HRW 全 ring 接力的次数
//   pasr_segment_creates_total    新建段次数 (cold-start cache miss)
//   pasr_cache_hit_observations   观测到的 cache_read > 0 事件数
//   pasr_cache_creation_obs       观测到的 cache_creation > 0 事件数
//
// 与 cachemetrics package 的 cache_token_count 互补: cachemetrics 是
// per-account vendor cache 命中量统计 (Track P); 本文件是 PASR-lite 调度
// 行为 + 段表健康统计 (蓝绿比 / shadow mode 切换前后对比基础)。
package pool

import (
	"expvar"
	"sync"
)

const (
	pasrKeySegmentCount        = "pasr_segment_count"
	pasrKeyEvictionsTotal      = "pasr_evictions_total"
	pasrKeyFirstPickTotal      = "pasr_first_pick_total"
	pasrKeyFailoverTotal       = "pasr_failover_total"
	pasrKeyFullRingFallback    = "pasr_full_ring_fallback_total"
	pasrKeySegmentCreatesTotal = "pasr_segment_creates_total"
	pasrKeyCacheHitObs         = "pasr_cache_hit_observations"
	pasrKeyCacheCreationObs    = "pasr_cache_creation_obs"
)

var (
	pasrMetricsOnce sync.Once
	pasrMetrics     *expvar.Map
)

func initPASRMetrics() {
	pasrMetricsOnce.Do(func() {
		pasrMetrics = expvar.NewMap("pasr")
		pasrMetrics.Add(pasrKeySegmentCount, 0)
		pasrMetrics.Add(pasrKeyEvictionsTotal, 0)
		pasrMetrics.Add(pasrKeyFirstPickTotal, 0)
		pasrMetrics.Add(pasrKeyFailoverTotal, 0)
		pasrMetrics.Add(pasrKeyFullRingFallback, 0)
		pasrMetrics.Add(pasrKeySegmentCreatesTotal, 0)
		pasrMetrics.Add(pasrKeyCacheHitObs, 0)
		pasrMetrics.Add(pasrKeyCacheCreationObs, 0)
	})
}

// IncFirstPick PASRSelector 命中段内首选时累 1。
func IncFirstPick() {
	initPASRMetrics()
	pasrMetrics.Add(pasrKeyFirstPickTotal, 1)
}

// IncFailover PASRSelector 段内首选不可用、回退到 [1] [2] 时累 1。
func IncFailover() {
	initPASRMetrics()
	pasrMetrics.Add(pasrKeyFailoverTotal, 1)
}

// IncFullRingFallback 段全 unhealthy 走 HRW 全 ring 接力时累 1。
func IncFullRingFallback() {
	initPASRMetrics()
	pasrMetrics.Add(pasrKeyFullRingFallback, 1)
}

// IncSegmentCreates LookupOrCreate 真新建段时累 1 (反映 cold-start 频率)。
func IncSegmentCreates() {
	initPASRMetrics()
	pasrMetrics.Add(pasrKeySegmentCreatesTotal, 1)
}

// IncCacheHitObs feedback observer 收到 cache_read > 0 事件时累 1。
func IncCacheHitObs() {
	initPASRMetrics()
	pasrMetrics.Add(pasrKeyCacheHitObs, 1)
}

// IncCacheCreationObs feedback observer 收到 cache_creation > 0 事件时累 1。
func IncCacheCreationObs() {
	initPASRMetrics()
	pasrMetrics.Add(pasrKeyCacheCreationObs, 1)
}

// AddEvictions 老化 / LRU evict 时累加 evict 段数。
func AddEvictions(n int64) {
	if n <= 0 {
		return
	}
	initPASRMetrics()
	pasrMetrics.Add(pasrKeyEvictionsTotal, n)
}

// SetSegmentCount 段表大小快照同步用 (5min ticker 调一次, 不每请求调)。
func SetSegmentCount(n int64) {
	initPASRMetrics()
	if v, ok := pasrMetrics.Get(pasrKeySegmentCount).(*expvar.Int); ok {
		v.Set(n)
	}
}

// PASRMetricsSnapshot 给测试用, 读取所有 PASR 指标当前值。
type PASRMetricsSnapshot struct {
	SegmentCount        int64
	EvictionsTotal      int64
	FirstPickTotal      int64
	FailoverTotal       int64
	FullRingFallback    int64
	SegmentCreatesTotal int64
	CacheHitObs         int64
	CacheCreationObs    int64
}

// SnapshotPASRMetrics 读所有 PASR 指标当前值, 给测试 + introspection 用。
func SnapshotPASRMetrics() PASRMetricsSnapshot {
	initPASRMetrics()
	get := func(key string) int64 {
		if v, ok := pasrMetrics.Get(key).(*expvar.Int); ok {
			return v.Value()
		}
		return 0
	}
	return PASRMetricsSnapshot{
		SegmentCount:        get(pasrKeySegmentCount),
		EvictionsTotal:      get(pasrKeyEvictionsTotal),
		FirstPickTotal:      get(pasrKeyFirstPickTotal),
		FailoverTotal:       get(pasrKeyFailoverTotal),
		FullRingFallback:    get(pasrKeyFullRingFallback),
		SegmentCreatesTotal: get(pasrKeySegmentCreatesTotal),
		CacheHitObs:         get(pasrKeyCacheHitObs),
		CacheCreationObs:    get(pasrKeyCacheCreationObs),
	}
}
