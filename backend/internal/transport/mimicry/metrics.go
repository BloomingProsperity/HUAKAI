package mimicry

import "expvar"

// 出口 sidecar 拨号结果计数(按 result 维度)。这是 A2 指标层——与 A1 出口边界日志
// 是【同一次拨号事件】的两种观测,从 sidecarDialObserver 的同一处发射,因此指标与日志
// 永不背离(看关联产物:指标 result ↔ A1 日志 phase/error_class ↔ Rust sidecar tracing
// 的 phase,三处同口径,运维能把 /metrics 的某个 result 计数直接对到日志/tracing 的同名
// 阶段)。result 取值与 A1 观测器的分层 phase 一一对应:
//
//	ok         隧道建立成功        = established 日志(Debug) / Rust established tracing
//	dial_fail  拨 unix socket 失败  = dial 日志,error_class=sidecar_unavailable
//	write_fail 发控制帧失败         = write_control 日志,error_class=sidecar_unavailable
//	read_fail  收 ACK 帧失败        = read_ack 日志,error_class=sidecar_unavailable
//	rejected   sidecar 显式拒绝     = rejected 日志,error_class=sidecar_profile_unavailable
//
// dial_fail/write_fail/read_fail/rejected 都表示出口拒绝服务；系统不存在
// 标准 TLS 或 Go-native 回退，运维可直接据此计算出口成功率与失败构成。
const (
	egressDialResultOK        = "ok"
	egressDialResultDialFail  = "dial_fail"
	egressDialResultWriteFail = "write_fail"
	egressDialResultReadFail  = "read_fail"
	egressDialResultRejected  = "rejected"
)

// egressDialTotal 在包加载时注册(expvar.NewMap 同名重复调用会 panic,包级 var 只初始化
// 一次,天然安全)。经 otelbridge 桥接进 /metrics,也可在 /debug/vars 直接看。
var egressDialTotal = expvar.NewMap("egress_sidecar_dial_total")

// recordEgressDialResult 递增某一 result 的出口拨号计数。best-effort 可观测,绝不因
// 计数影响拨号可用性(与 newEgressCorrelationID 同原则:观测不得反噬可用性)。
func recordEgressDialResult(result string) {
	egressDialTotal.Add(result, 1)
}

// RecordEgressProbeFailure 记录 transport 层 probe 预检阶段的出口失败,计入与真实拨号
// 同一套 result 桶。probe 是 DialTLS 之前的可达性预检:默认 fail-closed 生产姿态下 sidecar
// 宕机时,请求在 probe 一步即被拒、DialTLS 从不运行——若此处不计数,出口成功率分母会漏掉
// 最主要的宕机情形(看关联产物:指标要覆盖运维真正关心的那类失败,而非只覆盖 DialTLS 内部)。
// 按 error_class 对齐真实拨号的桶:profile 拒绝→rejected,其余(sidecar 不可用)→dial_fail。
func RecordEgressProbeFailure(profileRejected bool) {
	if profileRejected {
		recordEgressDialResult(egressDialResultRejected)
		return
	}
	recordEgressDialResult(egressDialResultDialFail)
}

// egressDialResultForPhase 把 A1 观测器失败时的分层 phase 映射到指标 result 标签,
// 保证"哪一层失败"在日志(phase)与指标(result)两侧用同一套划分。未知 phase 兜底
// 归到 dial_fail(sidecar 不可用类),不静默丢计数。
func egressDialResultForPhase(phase string) string {
	switch phase {
	case sidecarPhaseWriteControl:
		return egressDialResultWriteFail
	case sidecarPhaseReadAck:
		return egressDialResultReadFail
	default: // sidecarPhaseDial 及任何未预期的失败 phase
		return egressDialResultDialFail
	}
}
