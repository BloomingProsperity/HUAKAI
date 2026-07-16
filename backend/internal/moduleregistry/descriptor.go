// Package moduleregistry 持有进程内、运行时的模块知识主干,供(管理员门控的)
// 运维助手查询以快速定位根因:每个 HUAKAI 子系统注册一个 ModuleDescriptor,
// 描述其身份、能力以及一个可选的只读存活探针(liveness probe)。
//
// 边界(刻意设计,由测试强制保证):
//   - 增量式:本包不在任何请求热路径上。探针仅在运维人员请求 Snapshot 时
//     才运行;此处的任何东西都不会被逐请求触及。
//   - 隐私:探针结果只携带系统诊断用的枚举/计数 —— 绝不含密钥、token 或
//     用户数据。ProbeResult.Detail 用于运维分诊,而非回显输入。调用方必须
//     遵守约束网关其余部分的隐私脱敏边界。
//   - 非阻塞:探针绝不能阻塞启动,也绝不能让调用方 panic。Snapshot 以每探针
//     超时并发运行各探针;缓慢或出错的探针只会降级为一个状态,而不会让
//     snapshot 挂起。
package moduleregistry

import "context"

// ProbeStatus 是 HealthProbe 可上报的封闭枚举。它刻意做得很小,以便运维人员
// (或助手)无需解析自由文本即可对其进行推理。探针无法确定的任何情况都归为
// "unknown",这与一个正在失败的 "error" 截然不同。
type ProbeStatus string

const (
	// StatusOK —— 模块已接线且其廉价的只读检查通过。
	StatusOK ProbeStatus = "ok"
	// StatusDegraded —— 已接线,但某个非致命检查不满意(例如池为空)。
	StatusDegraded ProbeStatus = "degraded"
	// StatusUnknown —— 未注册探针,或探针超时 / 被跳过。
	// Unknown 不是错误:它表示「我们无法确定」,这是缓慢探针在超时下的
	// 正确且不引发警报的状态。
	StatusUnknown ProbeStatus = "unknown"
	// StatusError —— 探针已运行并报告了一个实际的失败。
	StatusError ProbeStatus = "error"
)

// ProbeResult 是 HealthProbe 的只读结果。Detail 是一个简短的、面向运维人员的
// 诊断字符串,由枚举/计数构造而成 —— 绝不含密钥或用户数据。
type ProbeResult struct {
	Status ProbeStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

// HealthProbe 是一个可选的只读存活检查。它必须廉价且无副作用,必须尊重 ctx
// 的取消/截止时间,并且绝不能 panic。若某个探针 panic,Snapshot 会恢复它并
// 转为 StatusError 结果,这样一个坏探针就无法拖垮运维视图。
type HealthProbe func(ctx context.Context) ProbeResult

// ActivationEndpoint 描述某个公开能力端点在当前进程中的接线与激活情况。
// 指针字段用于保持 JSON 合同加性:旧模块可以完全不填 activation。
type ActivationEndpoint struct {
	Name     string `json:"name"`
	Injected *bool  `json:"injected,omitempty"`
	Active   *bool  `json:"active,omitempty"`
}

// ActivationSnapshot 是模块能力激活快照。它只表达结构化、隐私安全的运维信号，
// 不回显密钥、盐值、原始配置或任何租户/用户数据。
type ActivationSnapshot struct {
	Declared       *bool                `json:"declared,omitempty"`
	Constructed    *bool                `json:"constructed,omitempty"`
	Injected       *bool                `json:"injected,omitempty"`
	Active         *bool                `json:"active,omitempty"`
	SharedSafe     *bool                `json:"shared_safe,omitempty"`
	Observable     *bool                `json:"observable,omitempty"`
	Verified       *bool                `json:"verified,omitempty"`
	Backend        string               `json:"backend,omitempty"`
	Mode           string               `json:"mode,omitempty"`
	TrafficPercent *int                 `json:"traffic_percent,omitempty"`
	Endpoints      []ActivationEndpoint `json:"endpoints,omitempty"`
}

// ModuleDescriptor 是一个 HUAKAI 子系统的静态身份,外加一个可选的实时探针。
// ID 是稳定的点分字符串(例如 "billing.service"),因此能在重构中存活,并能
// 从文档、静态 catalog 和助手的上下文中被引用。
type ModuleDescriptor struct {
	// ID 是稳定标识符(点分、小写)。必填;Register 会拒绝空 ID。
	ID string `json:"id"`
	// Category 为运维人员的过滤而对模块分组,例如 "money-path"、
	// "routing"、"credentials"、"observability"。
	Category string `json:"category"`
	// Title 是一个简短的、人类可读的名称。
	Title string `json:"title"`
	// Capabilities 用运维人员的术语简短列出该模块做什么。
	Capabilities []string `json:"capabilities,omitempty"`
	// Activation 是模块能力激活快照。nil 表示该模块尚未声明结构化 activation。
	Activation *ActivationSnapshot `json:"activation,omitempty"`
	// HealthProbe 是可选的。为 nil 时,Snapshot 对该模块报告 StatusUnknown
	//(没有探针不算错误)。
	HealthProbe HealthProbe `json:"-"`
}
