package cachemetrics

import (
	"expvar"
	"fmt"
	"sync"
)

type L2SizeSample struct {
	Vendor    string
	Model     string
	SizeBytes int64
}

type L2SnapshotRow struct {
	HitTotal  int64 `json:"hit_total"`
	MissTotal int64 `json:"miss_total"`
	SizeBytes int64 `json:"size_bytes"`
}

var (
	l2Once        sync.Once
	l2HitTotal    *expvar.Map
	l2MissTotal   *expvar.Map
	l2SizeBytes   *expvar.Map
	l2SizeMu      sync.Mutex
	l2KnownLabels = map[string]struct{}{}
)

func initL2Counters() {
	l2Once.Do(func() {
		l2HitTotal = expvar.NewMap("huakai_cache_l2_hit_total")
		l2MissTotal = expvar.NewMap("huakai_cache_l2_miss_total")
		l2SizeBytes = expvar.NewMap("huakai_cache_l2_size_bytes")
	})
}

func ObserveL2Hit(vendor, model string) {
	initL2Counters()
	l2HitTotal.Add(l2LabelKey(vendor, model), 1)
}

func ObserveL2Miss(vendor, model string) {
	initL2Counters()
	l2MissTotal.Add(l2LabelKey(vendor, model), 1)
}

func SyncL2SizeBytes(samples []L2SizeSample) {
	initL2Counters()
	l2SizeMu.Lock()
	defer l2SizeMu.Unlock()
	current := make(map[string]int64, len(samples))
	for _, sample := range samples {
		key := l2LabelKey(sample.Vendor, sample.Model)
		current[key] += sample.SizeBytes
		l2KnownLabels[key] = struct{}{}
	}
	for key := range l2KnownLabels {
		value := current[key]
		if v, ok := l2SizeBytes.Get(key).(*expvar.Int); ok {
			v.Set(value)
			continue
		}
		l2SizeBytes.Add(key, value)
	}
}

func SnapshotL2() map[string]L2SnapshotRow {
	initL2Counters()
	out := map[string]L2SnapshotRow{}
	l2HitTotal.Do(func(kv expvar.KeyValue) {
		row := out[kv.Key]
		if v, ok := kv.Value.(*expvar.Int); ok {
			row.HitTotal = v.Value()
		}
		out[kv.Key] = row
	})
	l2MissTotal.Do(func(kv expvar.KeyValue) {
		row := out[kv.Key]
		if v, ok := kv.Value.(*expvar.Int); ok {
			row.MissTotal = v.Value()
		}
		out[kv.Key] = row
	})
	l2SizeBytes.Do(func(kv expvar.KeyValue) {
		row := out[kv.Key]
		if v, ok := kv.Value.(*expvar.Int); ok {
			row.SizeBytes = v.Value()
		}
		out[kv.Key] = row
	})
	return out
}

func l2LabelKey(vendor, model string) string {
	if vendor == "" {
		vendor = "unknown"
	}
	if model == "" {
		model = "unknown"
	}
	return fmt.Sprintf("vendor=%s,model=%s", vendor, model)
}
