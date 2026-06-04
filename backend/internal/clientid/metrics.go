// metrics.go — U6-C atomic：per-client-identity 请求计数 metrics。
//
// 设计:
//   - 用 stdlib expvar (标准库，零新依赖) 暴露 metrics，pull-based
//   - per-Identity atomic 计数器（int64 expvar.Int 内部已 atomic）
//   - 通过 /debug/vars 端点 (chi 默认未挂载，admin 可单独挂) 读取
//   - middleware 在每次 Detect 后 +1
//
// 用途:
//   - 运维实时观察各 identity 流量分布
//   - abuse detection 基线（某 identity 突增 = 异常）
//   - per-client billing rollup 数据源
//
// 不做:
//   - 不引 Prometheus / OpenTelemetry（新依赖；expvar 足够 phase 1）
//   - 不持久化（重启清零；下游 obs 异步 ingest 时持久化）
//   - 不做 per-tenant cardinality（Identity 只 6-7 个值，cardinality 安全）
package clientid

import (
	"expvar"
	"sync"
)

// counters 持有所有 Identity 的请求计数器。
// 用 sync.Once + 全局 expvar.Map 统一注册，防多次 import 重复 publish。
var (
	countersOnce sync.Once
	counters     *expvar.Map
)

// initCounters 初始化全局 expvar map（lazy + 幂等）。
// expvar.NewMap("name") 同名重复调用 panic; 用 sync.Once 防止。
func initCounters() {
	countersOnce.Do(func() {
		counters = expvar.NewMap("clientid_request_count")
		// 预初始化所有已知 identity（让 /debug/vars 列出全集，便于运维）
		for _, id := range allKnownIdentities() {
			counters.Add(string(id), 0)
		}
	})
}

// allKnownIdentities 返回所有已声明的 Identity 常量。
// 加新 Identity 时同步加这里（test invariant 检查）。
func allKnownIdentities() []Identity {
	return []Identity{
		IdentityCursor,
		IdentityClaudeCode,
		IdentityCody,
		IdentityChatUI,
		IdentityCurlScript,
		IdentityUnknown,
	}
}

// IncrementRequestCount 为指定 identity 递增请求计数器。
// 线程安全。Identity 不在已知列表时仍记录（防漏算 future identity）。
func IncrementRequestCount(id Identity) {
	initCounters()
	counters.Add(string(id), 1)
}

// RequestCount 返回 identity 的当前累计请求数。主要给测试 + 运维 introspection 用。
func RequestCount(id Identity) int64 {
	initCounters()
	v := counters.Get(string(id))
	if v == nil {
		return 0
	}
	if iv, ok := v.(*expvar.Int); ok {
		return iv.Value()
	}
	return 0
}

// ResetMetricsForTesting 已移到 export_test.go（仅测试 binary 可见）。
// 生产代码无法调用—
//   - 防止外部包导入 clientid 后误调 ResetMetricsForTesting 清零生产 counter
//   - 名义上"仅测试用"的 export 没有编译时屏障; _test.go 文件天然有屏障
