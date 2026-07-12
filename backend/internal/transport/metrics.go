package transport

import "expvar"

// egressSidecarFallbackTotal 是出口 sidecar→Go-native 降级事件计数(按 reason_class 维度)。
//
// 这不是新造的平行计数器:Factory 早有 sidecarFallbacks(atomic.Uint64)在数同一件事,但它
// 只经 SidecarFallbackCount() 内存可读、只 slog.Warn,从不进 expvar/Prometheus——是个活账
// 死路,运维在 /metrics 上看不到"出口降级正在发生"。这里把它桥进 expvar(与 recordSidecarFallback
// 同一处递增,两者永不背离),reason_class 用 classifySidecarError 的输出,与 A2 拨号计数
// (egress_sidecar_dial_total 的 error_class)及 A1 日志 reason_class 同一套分类。
//
// 语义与拨号计数互补,不重叠:
//   - SidecarFallbackEnabled=false(默认,生产 fail-closed):sidecar 不可用 → 直接拒服务,
//     计入 egress_sidecar_dial_total{dial_fail/...},本计数器不动(恒 0)。
//   - SidecarFallbackEnabled=true:sidecar 不可用 → 降级 Go-native mimicry 转发,本计数器 +1。
//     降级意味着指纹保真度下降(退回较弱的 Go 侧伪装),对"卖出口质量"是必须可见的告警信号。
//
// 包级 var 只初始化一次(expvar.NewMap 同名重复调用会 panic),经 otelbridge 桥进 /metrics。
var egressSidecarFallbackTotal = expvar.NewMap("egress_sidecar_fallback_total")

// recordEgressFallbackMetric 递增某一 reason_class 的出口降级计数。与 Factory.recordSidecarFallback
// 的内存原子计数同点调用,确保 /metrics 暴露值与 SidecarFallbackCount() 一致。
func recordEgressFallbackMetric(class TransportErrorClass) {
	egressSidecarFallbackTotal.Add(string(class), 1)
}
