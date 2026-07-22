package cachemetrics

import (
	"context"

	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
)

// SyncL2StoreSize 将缓存存储的当前容量同步到按厂商和模型聚合的指标。
func SyncL2StoreSize(store l2cache.Store) {
	if store == nil {
		SyncL2SizeBytes(nil)
		return
	}
	stats := store.Stats(context.Background())
	samples := make([]L2SizeSample, 0, len(stats.ByLabel))
	for _, row := range stats.ByLabel {
		samples = append(samples, L2SizeSample{
			Vendor:    row.Vendor,
			Model:     row.Model,
			SizeBytes: row.SizeBytes,
		})
	}
	SyncL2SizeBytes(samples)
}
