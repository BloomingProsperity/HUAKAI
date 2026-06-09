package dlq

import (
	"context"
	"expvar"
	"fmt"
	"sync"
)

// LaneCount holds the pending row count for a single DLQ lane.
type LaneCount struct {
	Lane  Lane
	Count int64
}

// CountPendingByLane returns the number of rows with status='pending' grouped
// by lane. Only pending rows are counted; inflight/delivered/dlq rows are excluded.
// MUTATION guard: removing the status='pending' filter returns inflated counts.
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

// dlqDepthMetrics is the expvar.Map "dlq_depth" used by the OTel bridge and
// alertmetrics overlay. Keys are "depth_HIGH", "depth_MED", "depth_LOW".
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

// UpdateDLQDepthGauge refreshes the dlq_depth expvar map from the database.
// It is called by the DLQ worker ticker so the alert rule engine always has a
// fresh gauge via ExpvarMetricSource.Snapshot().
func (s *Store) UpdateDLQDepthGauge(ctx context.Context) error {
	counts, err := s.CountPendingByLane(ctx)
	if err != nil {
		return err
	}
	m := getDLQDepthMetrics()
	// Reset all lanes to zero before applying the fresh snapshot so that lanes
	// that drain completely do not retain stale nonzero values.
	totals := map[Lane]int64{LaneHigh: 0, LaneMed: 0, LaneLow: 0}
	for _, lc := range counts {
		totals[lc.Lane] += lc.Count
	}
	for lane, count := range totals {
		key := "depth_" + string(lane)
		// expvar.Map.Add is the only safe mutating method on an existing key.
		// We use Set via the underlying *expvar.Int instead.
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
