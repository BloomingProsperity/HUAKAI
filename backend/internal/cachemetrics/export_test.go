// export_test.go — 仅测试 binary 可见的 helper, 防 production 误调清零。
package cachemetrics

import "expvar"

// resetForTesting 把 counter 清零供 tests 用。
// 不导出, 不暴露到 production 二进制。
func resetForTesting() {
	initCounters()
	initL2Counters()
	for _, key := range []string{keyCreationTotal, keyReadTotal, keyRequestCount} {
		if v, ok := counters.Get(key).(*expvar.Int); ok {
			v.Set(0)
		}
	}
	// 清 per-account map (Track P)
	if countersByAccount != nil {
		countersByAccount.Do(func(kv expvar.KeyValue) {
			if sub, ok := kv.Value.(*expvar.Map); ok {
				sub.Do(func(subkv expvar.KeyValue) {
					if iv, ok := subkv.Value.(*expvar.Int); ok {
						iv.Set(0)
					}
				})
			}
		})
	}
	for _, m := range []*expvar.Map{l2HitTotal, l2MissTotal, l2SizeBytes} {
		m.Do(func(kv expvar.KeyValue) {
			if iv, ok := kv.Value.(*expvar.Int); ok {
				iv.Set(0)
			}
		})
	}
	l2SizeMu.Lock()
	l2KnownLabels = map[string]struct{}{}
	l2SizeMu.Unlock()
}
