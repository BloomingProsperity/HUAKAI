package transport

import "os"

// mimicryEnvVar 是全局出站伪装运维开关的环境变量名。
const mimicryEnvVar = "HUAKAI_TRANSPORT_MIMICRY"

// MimicryEnabled 报告是否启用出站 TLS 指纹伪装。读运维开关
// HUAKAI_TRANSPORT_MIMICRY:空或任意非 "false" 值一律视为开(默认伪装),
// 仅显式设为 "false" 时全局降级到标准 net/http transport。
//
// 默认开保证不改现有生产行为;关闭用于排障(隔离"是不是伪装本身导致的
// 连接异常"),或伪装实现出故障时一键回退到标准出站。读取惯例与 utls_dialer
// 的 forceH1Enabled 一致——就地读 env,避免 transport/gateway 反向 import
// config 包。gateway 的 DB profile 旁路也复用本函数,保证两处判定一致。
func MimicryEnabled() bool {
	return os.Getenv(mimicryEnvVar) != "false"
}

// isMimicry 判断 mode 是否为某种 vendor 伪装(8 个 mimicry_* 之一)。
// standard / diagnostics_only 返回 false。全局开关关闭时,只有 isMimicry
// 为真的 mode 才被降级为 standard。
func (m TransportMode) isMimicry() bool {
	switch m {
	case TransportModeMimicryClaudeCode,
		TransportModeMimicryChatGPT,
		TransportModeMimicryGeminiAdvanced,
		TransportModeMimicryAntigravity,
		TransportModeMimicryCursor,
		TransportModeMimicryCopilot,
		TransportModeMimicryKiro,
		TransportModeMimicryWindsurf:
		return true
	}
	return false
}
