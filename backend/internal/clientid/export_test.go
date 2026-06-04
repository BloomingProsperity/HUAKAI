// export_test.go — 测试专用 helper. _test.go 后缀让本文件**仅**在
// `go test` 时编译，生产 binary 看不到这些 symbol——天然防"误调"。
//
// 原 ResetMetricsForTesting
// 暴露在 metrics.go (production binary 可见)，可被外部 package 误调清零
// 计数。改放此文件后，外部 production 代码无法引用，仅 clientid_test 可用。
package clientid

import "expvar"

// resetMetricsForTesting 把所有 identity counter 清零。
//
// **不要** t.Parallel() 调用: expvar.Map 内部
// 是并发安全的，但 iv.Set(0) 与 counters.Add(...) 之间无 happens-before，
// 并发场景下读 counter 会拿到中间值。当前所有 metrics_test.go 用例都是
// 顺序执行——若加 t.Parallel() 必须先重写本 helper 用 mutex。
func resetMetricsForTesting() {
	initCounters()
	for _, id := range allKnownIdentities() {
		v := counters.Get(string(id))
		if iv, ok := v.(*expvar.Int); ok {
			iv.Set(0)
		}
	}
}
