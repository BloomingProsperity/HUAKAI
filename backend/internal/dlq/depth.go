package dlq

import (
	"context"
	"expvar"
	"fmt"
	"sync"
)

// LaneCount 持有单个 DLQ lane 的 pending 行数。
type LaneCount struct {
	Lane  Lane
	Count int64
}

// CountPendingByLane 返回 status='pending' 的行数，按 lane 分组。
// 仅统计 pending 行；inflight/delivered/dlq 行被排除在外。
// MUTATION 守卫：移除 status='pending' 过滤会返回被夸大的计数。
func (s *Store) CountPendingByLane(ctx context.Context) ([]LaneCount, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT lane, count(*)
FROM usage_record_dlq
WHERE status = 'pending'
GROUP BY lane
ORDER BY lane`)
	if err != nil {
		return nil, fmt.Errorf("dlq: count pending by lane: %w", err)
	}
	defer rows.Close()

	var out []LaneCount
	for rows.Next() {
		var lc LaneCount
		var lane string
		if err := rows.Scan(&lane, &lc.Count); err != nil {
			return nil, fmt.Errorf("dlq: scan pending lane count: %w", err)
		}
		lc.Lane = Lane(lane)
		out = append(out, lc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dlq: iterate pending lane count: %w", err)
	}
	return out, nil
}

// dlqDepthMetrics 是 OTel 桥接与 alertmetrics 叠加层使用的 expvar.Map
// "dlq_depth"。键为 "depth_HIGH"、"depth_MED"、"depth_LOW"。
var (
	dlqDepthOnce    sync.Once
	dlqDepthMetrics *expvar.Map
)

func getDLQDepthMetrics() *expvar.Map {
	dlqDepthOnce.Do(func() {
		m := expvar.NewMap("dlq_depth")
		m.Add("depth_HIGH", 0)
		m.Add("depth_MED", 0)
		m.Add("depth_LOW", 0)
		dlqDepthMetrics = m
	})
	return dlqDepthMetrics
}

// UpdateDLQDepthGauge 从数据库刷新 dlq_depth expvar map。
// 它由 DLQ worker 的 ticker 调用，使告警规则引擎始终能通过
// ExpvarMetricSource.Snapshot() 拿到新鲜的 gauge。
func (s *Store) UpdateDLQDepthGauge(ctx context.Context) error {
	counts, err := s.CountPendingByLane(ctx)
	if err != nil {
		return err
	}
	m := getDLQDepthMetrics()
	// 在应用新快照前先将所有 lane 重置为零，使已完全排空的 lane
	// 不会保留陈旧的非零值。
	totals := map[Lane]int64{LaneHigh: 0, LaneMed: 0, LaneLow: 0}
	for _, lc := range counts {
		totals[lc.Lane] += lc.Count
	}
	for lane, count := range totals {
		key := "depth_" + string(lane)
		// expvar.Map.Add 是对已存在键唯一安全的变更方法。
		// 这里改为通过底层的 *expvar.Int 调用 Set。
		if ev := m.Get(key); ev != nil {
			if iv, ok := ev.(*expvar.Int); ok {
				iv.Set(count)
				continue
			}
		}
		m.Add(key, count)
	}
	return nil
}
