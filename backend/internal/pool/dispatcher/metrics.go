// pasr_dispatch_metrics.go — PASR-lite 主接线 M2 原子：SelectorDispatcher 的
// expvar metrics，eager init 启动期 /debug/vars 立即可见。
//
// 与已有 `pasr` map (pasr_metrics.go) 并列 — 不嵌套, 不污染。 dispatcher 关心
// 跨 selector 比对 (shadow match/diff)、canary 分桶占比、shadow 异步生命周期
// (drop / panic / timeout); pasr map 关心段表 + 调度行为 (HRW first/failover、
// segment_count、cache_obs)。 两 map 各司其职, 同一 ops dashboard 可分别拉。
//
// 设计要点 (synthesis 盲点 B1):
//   - eager init 在 package init() 里调一次 — 启动期 /debug/vars 立即返完整 map,
//     不依赖第一次请求才注册 (sync.Once 懒模式启动期 panel 是空的)
//   - 所有计数器 Add(key, 0) 预填 → 即使从未触发, /debug/vars 仍含 0 而不是 missing
//   - mode_<name>_total 5 mode 各一份: 每次 dispatcher Select 时 hot path 累 1,
//     可观察实际流量 mode 分布
//
// 不做的:
//   - 不做 histogram (P99 latency 等); 那归 M7 集成测试 + ops dashboard 外部聚合
//   - 不接 prometheus; 项目当前用 expvar /debug/vars 兜底, prometheus 是后续 atom
package dispatcher

import (
	"expvar"
)

const (
	pasrDispatchMapName = "pasr_dispatch"

	// shadow 路径生命周期
	pasrDispKeyShadowSampled = "shadow_sampled_total"
	pasrDispKeyShadowMatch   = "shadow_match_total"
	pasrDispKeyShadowDiff    = "shadow_diff_total"
	pasrDispKeyShadowDrop    = "shadow_drop_total"
	pasrDispKeyShadowPanic   = "shadow_panic_total"
	pasrDispKeyShadowTimeout = "shadow_timeout_total"
	pasrDispKeyShadowPASRErr = "shadow_pasr_err_total"

	// canary 路径分桶 + fallback 语义 (D4)
	pasrDispKeyCanaryPASRUsed            = "canary_pasr_used_total"
	pasrDispKeyCanaryDefaultUsed         = "canary_default_used_total"
	pasrDispKeyCanaryPreMutationFallback = "canary_pre_mutation_fail_fallback_total"
	pasrDispKeyCanaryPostMutationRelease = "canary_post_mutation_fail_release_total"

	// 5 mode 实际命中分布 — Owner 切流量后能从 /debug/vars 看到效果
	pasrDispKeyModeDefault     = "mode_default_total"
	pasrDispKeyModeShadow      = "mode_shadow_total"
	pasrDispKeyModeCanary      = "mode_canary_total"
	pasrDispKeyModePASRPrimary = "mode_pasr_primary_total"
	pasrDispKeyModePASRStrict  = "mode_pasr_strict_total"
)

// pasrDispatchMetrics 是 dispatcher 计数器汇总, eager 初始化, 不可重入。
var pasrDispatchMetrics *expvar.Map

// pasrDispatchByVendor 是 per-vendor 切片维度 (D follow-up: shadow/canary 决策
// 必须按 vendor 切片读不跨平均, memory: project_real_vendor_account_scope)。
// expvar 嵌套 map: 顶层 key = vendor (anthropic/openai/gemini/codex), 子 map
// 含 dispatcher 维度计数器 (shadow_match / shadow_diff / mode_<x>_total 等)。
//
// 4 vendor × ~6 子 metric (M-rust-5 + project memory 锁定): 启动期 eager
// init 24 sub-counter 全 0 预填, /debug/vars 启动即可见 dashboard panel。
var pasrDispatchByVendor *expvar.Map

// PASRDispatchVendors 是真实账号测试范围 (Owner 2026-05-09 锁定);
// metric eager init 用此列表, caller 也用同 string 调 IncDispatchVendor。
var PASRDispatchVendors = []string{"anthropic", "openai", "gemini", "codex"}

// pasrDispatchVendorKeys 是每个 vendor sub-map 的 key 列表 (子 metric 集)。
// 加新维度同步改这里 + IncDispatchVendor switch + 每 vendor sub-map 重 init。
//
// D2 (2026-05-09): 加 5 mode_<x>_total — dispatcher.Select 调 IncDispatchVendorMode
// 时按 mode 写 vendor sub-counter, 让 dashboard 看 per-(vendor, mode) 切片。
var pasrDispatchVendorKeys = []string{
	pasrDispKeyShadowSampled,
	pasrDispKeyShadowMatch,
	pasrDispKeyShadowDiff,
	pasrDispKeyShadowTimeout,
	pasrDispKeyShadowPASRErr,
	pasrDispKeyCanaryPASRUsed,
	pasrDispKeyModeDefault,
	pasrDispKeyModeShadow,
	pasrDispKeyModeCanary,
	pasrDispKeyModePASRPrimary,
	pasrDispKeyModePASRStrict,
}

func init() {
	pasrDispatchMetrics = expvar.NewMap(pasrDispatchMapName)
	for _, k := range pasrDispatchKeys() {
		pasrDispatchMetrics.Add(k, 0)
	}
	pasrDispatchByVendor = expvar.NewMap(pasrDispatchMapName + "_by_vendor")
	for _, vendor := range PASRDispatchVendors {
		sub := new(expvar.Map).Init()
		for _, mk := range pasrDispatchVendorKeys {
			sub.Add(mk, 0)
		}
		pasrDispatchByVendor.Set(vendor, sub)
	}
}

// IncDispatchVendor 按 vendor 切片累计某 dispatcher 子 metric。 vendor 必须
// 在 PASRDispatchVendors 集合内, 否则静默丢弃 (caller 上游 chat_completions_
// handler 应保证仅传 PASRDispatchVendors 中的字面量)。 metric key 必须在
// pasrDispatchVendorKeys 里, 否则也静默 (子 sub.Add 不会 panic, 但 Snapshot
// 不能可见 — 加新 metric 要更 pasrDispatchVendorKeys)。
//
// 用法: chat_completions_handler 在 dispatcher.Select 返成功后调:
//
//	IncDispatchVendor(pasrDispKeyShadowSampled, "anthropic")
//
// 由于 vendor 字面量与 PASRDispatchVendors 一致 (Owner 锁定 4 vendor),
// dashboard 可以按 vendor 维度独立读 PASR shadow/diff 决策信号。
func IncDispatchVendor(metricKey, vendor string) {
	if vendor == "" {
		return
	}
	subVar := pasrDispatchByVendor.Get(vendor)
	if subVar == nil {
		return
	}
	sub, ok := subVar.(*expvar.Map)
	if !ok {
		return
	}
	sub.Add(metricKey, 1)
}

// pasrDispatchKeys 返完整 key 列表 — eager init + Snapshot 共用。 加新指标只需
// 改这一处 + Inc helper, 避免遗漏导致 dashboard panel 缺数据。
func pasrDispatchKeys() []string {
	return []string{
		pasrDispKeyShadowSampled,
		pasrDispKeyShadowMatch,
		pasrDispKeyShadowDiff,
		pasrDispKeyShadowDrop,
		pasrDispKeyShadowPanic,
		pasrDispKeyShadowTimeout,
		pasrDispKeyShadowPASRErr,
		pasrDispKeyCanaryPASRUsed,
		pasrDispKeyCanaryDefaultUsed,
		pasrDispKeyCanaryPreMutationFallback,
		pasrDispKeyCanaryPostMutationRelease,
		pasrDispKeyModeDefault,
		pasrDispKeyModeShadow,
		pasrDispKeyModeCanary,
		pasrDispKeyModePASRPrimary,
		pasrDispKeyModePASRStrict,
	}
}

// IncDispatchShadowSampled shadow 模式: 一次请求被采样 (按 ShadowPercent 命中)。
func IncDispatchShadowSampled() { pasrDispatchMetrics.Add(pasrDispKeyShadowSampled, 1) }

// IncDispatchShadowMatch shadow PASR 选的 account 与 default 一致。
func IncDispatchShadowMatch() { pasrDispatchMetrics.Add(pasrDispKeyShadowMatch, 1) }

// IncDispatchShadowDiff shadow PASR 选的 account 与 default 不一致 (Owner 关心
// 的核心信号 — diff/sampled = PASR vs default 的不一致率)。
func IncDispatchShadowDiff() { pasrDispatchMetrics.Add(pasrDispKeyShadowDiff, 1) }

// IncDispatchShadowDrop shadow 异步队列 (buffered chan) 满时累 1, 表示 dispatcher
// 主路径不愿再排队 → 直接丢弃以保护 P99。
func IncDispatchShadowDrop() { pasrDispatchMetrics.Add(pasrDispKeyShadowDrop, 1) }

// IncDispatchShadowPanic shadow goroutine recover 触发 — 必须 0 是健康指标,
// 任何 +1 都触发 ops 告警 (PASR shadow 实例 bug)。
func IncDispatchShadowPanic() { pasrDispatchMetrics.Add(pasrDispKeyShadowPanic, 1) }

// IncDispatchShadowTimeout shadow ctx (500ms) 超时, PASR Select 没在窗内回。
// 高 timeout 率 = PASR 实际比 default 慢, 不能直接 canary。
func IncDispatchShadowTimeout() { pasrDispatchMetrics.Add(pasrDispKeyShadowTimeout, 1) }

// IncDispatchShadowPASRErr shadow PASR Select 返非 nil error (非 panic、非
// timeout, 比如 ErrNoEligibleAccount)。 与 default 同源问题不算 PASR 缺陷;
// 持续高 = PASR 段表 cold-miss 太频繁需要预热。
func IncDispatchShadowPASRErr() { pasrDispatchMetrics.Add(pasrDispKeyShadowPASRErr, 1) }

// IncDispatchCanaryPASRUsed canary 命中桶走 PASR actual 路径 (写 slot + claim)。
func IncDispatchCanaryPASRUsed() { pasrDispatchMetrics.Add(pasrDispKeyCanaryPASRUsed, 1) }

// IncDispatchCanaryDefaultUsed canary miss 桶走 DefaultSelector 路径。
// PASR 桶 + default 桶之和应等于总请求数 (除 fallback)。
func IncDispatchCanaryDefaultUsed() { pasrDispatchMetrics.Add(pasrDispKeyCanaryDefaultUsed, 1) }

// IncDispatchCanaryPreMutationFallback canary PASR 在写 slot/claim **之前** 失败
// (D4 选项 A): dispatcher fallback 到 default。 这是良性回退, 流量没丢。
func IncDispatchCanaryPreMutationFallback() {
	pasrDispatchMetrics.Add(pasrDispKeyCanaryPreMutationFallback, 1)
}

// IncDispatchCanaryPostMutationRelease canary PASR 已 mutate (slot 已 acquire)
// 后失败 → fail closed + release slot。 不能 fallback default 否则双 claim race。
// 持续高 = M3 release 路径异常或 DB 抖动, ops 必须 page。
func IncDispatchCanaryPostMutationRelease() {
	pasrDispatchMetrics.Add(pasrDispKeyCanaryPostMutationRelease, 1)
}

// IncDispatchMode 按当前模式名累 1, 用 string 而非 enum 让 caller (dispatcher)
// 直接传 cfg.Mode 字符串, 避免 import config 反向依赖。 unknown mode 静默丢弃
// (LoadPoolSelector + Validate 已守门, 此处仅做兜底)。
func IncDispatchMode(mode string) {
	switch mode {
	case "default":
		pasrDispatchMetrics.Add(pasrDispKeyModeDefault, 1)
	case "shadow":
		pasrDispatchMetrics.Add(pasrDispKeyModeShadow, 1)
	case "canary":
		pasrDispatchMetrics.Add(pasrDispKeyModeCanary, 1)
	case "pasr-primary":
		pasrDispatchMetrics.Add(pasrDispKeyModePASRPrimary, 1)
	case "pasr-strict":
		pasrDispatchMetrics.Add(pasrDispKeyModePASRStrict, 1)
	}
}

// PASRDispatchSnapshot 给测试 + introspection 用, 读所有 dispatcher 指标快照。
type PASRDispatchSnapshot struct {
	ShadowSampled             int64
	ShadowMatch               int64
	ShadowDiff                int64
	ShadowDrop                int64
	ShadowPanic               int64
	ShadowTimeout             int64
	ShadowPASRErr             int64
	CanaryPASRUsed            int64
	CanaryDefaultUsed         int64
	CanaryPreMutationFallback int64
	CanaryPostMutationRelease int64
	ModeDefault               int64
	ModeShadow                int64
	ModeCanary                int64
	ModePASRPrimary           int64
	ModePASRStrict            int64
}

// IncDispatchVendorMode 把 (mode, vendor) 二维写到 pasr_dispatch_by_vendor。
// dispatcher.Select 在 IncDispatchMode (全局维度) 之后调本 helper, vendor
// 由 SelectionRequest.Vendor 透传 (chat handler 从 ResolvedModel.ProtocolFamily
// 派生)。 vendor 空 / 非法 / mode 非法均静默丢弃 (caller 上游已校验)。
//
// 支持 mode: default / shadow / canary / pasr-primary / pasr-strict (与
// IncDispatchMode 一致)。
func IncDispatchVendorMode(mode, vendor string) {
	if vendor == "" {
		return
	}
	var key string
	switch mode {
	case DispatchModeDefault:
		key = pasrDispKeyModeDefault
	case DispatchModeShadow:
		key = pasrDispKeyModeShadow
	case DispatchModeCanary:
		key = pasrDispKeyModeCanary
	case DispatchModePASRPrimary:
		key = pasrDispKeyModePASRPrimary
	case DispatchModePASRStrict:
		key = pasrDispKeyModePASRStrict
	default:
		return
	}
	IncDispatchVendor(key, vendor)
}

// SnapshotPASRDispatchVendor 读某 vendor 的 sub-counter 快照。 不在
// PASRDispatchVendors 集合内 / 子 map 不存在时返全 0。 给测试 +
// introspection 用; caller 不应在 hot path 调 (Snapshot 涉及 expvar 锁)。
func SnapshotPASRDispatchVendor(vendor string) map[string]int64 {
	out := make(map[string]int64, len(pasrDispatchVendorKeys))
	for _, k := range pasrDispatchVendorKeys {
		out[k] = 0
	}
	subVar := pasrDispatchByVendor.Get(vendor)
	if subVar == nil {
		return out
	}
	sub, ok := subVar.(*expvar.Map)
	if !ok {
		return out
	}
	for _, k := range pasrDispatchVendorKeys {
		if v, ok := sub.Get(k).(*expvar.Int); ok {
			out[k] = v.Value()
		}
	}
	return out
}

// SnapshotPASRDispatchMetrics 读 dispatcher 全部计数器当前值。
func SnapshotPASRDispatchMetrics() PASRDispatchSnapshot {
	get := func(key string) int64 {
		if v, ok := pasrDispatchMetrics.Get(key).(*expvar.Int); ok {
			return v.Value()
		}
		return 0
	}
	return PASRDispatchSnapshot{
		ShadowSampled:             get(pasrDispKeyShadowSampled),
		ShadowMatch:               get(pasrDispKeyShadowMatch),
		ShadowDiff:                get(pasrDispKeyShadowDiff),
		ShadowDrop:                get(pasrDispKeyShadowDrop),
		ShadowPanic:               get(pasrDispKeyShadowPanic),
		ShadowTimeout:             get(pasrDispKeyShadowTimeout),
		ShadowPASRErr:             get(pasrDispKeyShadowPASRErr),
		CanaryPASRUsed:            get(pasrDispKeyCanaryPASRUsed),
		CanaryDefaultUsed:         get(pasrDispKeyCanaryDefaultUsed),
		CanaryPreMutationFallback: get(pasrDispKeyCanaryPreMutationFallback),
		CanaryPostMutationRelease: get(pasrDispKeyCanaryPostMutationRelease),
		ModeDefault:               get(pasrDispKeyModeDefault),
		ModeShadow:                get(pasrDispKeyModeShadow),
		ModeCanary:                get(pasrDispKeyModeCanary),
		ModePASRPrimary:           get(pasrDispKeyModePASRPrimary),
		ModePASRStrict:            get(pasrDispKeyModePASRStrict),
	}
}
