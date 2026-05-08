// Package cachemetrics — vendor prompt cache 命中率观测 (expvar 暴露)。
//
// Anthropic 在 message_delta usage 中携带 cache_creation_input_tokens (新写
// 入缓存的 prompt token) 与 cache_read_input_tokens (从缓存命中的 token)。
// HUAKAI proto adapter 解析后调本包累计到全局计数器, 通过 /debug/vars
// 暴露给运维:
//
//   "cache_token_count": {
//     "creation_total":  N,
//     "read_total":      M,
//     "request_count":   K     // 总观测到 cache fields 的请求数
//   }
//
// 命中率粗算: read_total / (creation_total + read_total)。
//
// 为什么不分 tenant:
//   - 分 tenant cardinality 会爆 (多租户 → 多 expvar key)
//   - 如需 per-tenant, future U6+ 改 prometheus / 自定义 storage
//
// 为什么 stdlib expvar:
//   - 与 clientid/metrics 同模式 (sonnet U6-C SHOULD_FIX 已挂 /debug/vars)
//   - 零新依赖
package cachemetrics

import (
	"expvar"
	"sync"
)

const (
	keyCreationTotal = "creation_total"
	keyReadTotal     = "read_total"
	keyRequestCount  = "request_count"
)

var (
	once     sync.Once
	counters *expvar.Map
)

func initCounters() {
	once.Do(func() {
		counters = expvar.NewMap("cache_token_count")
		counters.Add(keyCreationTotal, 0)
		counters.Add(keyReadTotal, 0)
		counters.Add(keyRequestCount, 0)
	})
}

// Observe 累计一次 cache token 观测。
//
// 调用场景: proto adapter 解析 vendor usage event 时, 如果 cacheCreation
// 或 cacheRead 任一**正数**, 调本函数。
//
// 防御:
//   - 0/0 输入 (vendor 未启用 caching) 不应增 request_count, 避免 inflate 分母
//   - **负数输入** (sonnet F6 LOW): vendor 不应该返回负数 token 但有种边界
//     情况 vendor cached_tokens=null → unmarshal 到 int=0; 但若 caller 把
//     数据通过 int 转换有溢出, 显式 < 0 早返避免 silent-drop 后期歧义
//
// 已知 gap (sonnet F2 HIGH): TCP 连接在 message_start 之后、message_delta
// 之前断开 → AccumulatedUsage cache 字段为 0 → 静默 skip 本次观测。这是
// 设计取舍——partial observation 比无观测更糟; cache 字段必须在 message_delta
// 中累计才被记录, 没收到 message_delta 时无可信值可上报。
func Observe(cacheCreation, cacheRead int64) {
	initCounters()
	// 防御负数 (sonnet F6 LOW)——理论上不应发生但 fail-fast 比 silent-drop 清晰
	if cacheCreation < 0 || cacheRead < 0 {
		return
	}
	if cacheCreation == 0 && cacheRead == 0 {
		return
	}
	if cacheCreation > 0 {
		counters.Add(keyCreationTotal, cacheCreation)
	}
	if cacheRead > 0 {
		counters.Add(keyReadTotal, cacheRead)
	}
	counters.Add(keyRequestCount, 1)
}

// Snapshot 给测试 / introspection 用——返回当前累计值。
func Snapshot() (creation, read, requests int64) {
	initCounters()
	if v, ok := counters.Get(keyCreationTotal).(*expvar.Int); ok {
		creation = v.Value()
	}
	if v, ok := counters.Get(keyReadTotal).(*expvar.Int); ok {
		read = v.Value()
	}
	if v, ok := counters.Get(keyRequestCount).(*expvar.Int); ok {
		requests = v.Value()
	}
	return
}
