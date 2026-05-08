// export_test.go — 仅测试 binary 可见的 helper, 防 production 误调清零。
package cachemetrics

import "expvar"

// resetForTesting 把 counter 清零供 tests 用。
// 不导出, 不暴露到 production 二进制 (sonnet U6-C F1 模式)。
func resetForTesting() {
	initCounters()
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
}
