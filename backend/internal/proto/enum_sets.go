package proto

// enum 字符串集合的单一来源；envelope_validate 与 envelope_test 共用。
//
// 每个 set 来自对应 capability_*.go 中已声明的 string 常量，避免出现两套真相源。
// 通过 init() 校验 set 长度与"已知 vocab 数"一致，加新 vocab 时另一处必须同步。

var streamReadinessSet = map[StreamReadiness]struct{}{
	StreamReadyYes:     {},
	StreamReadyNo:      {},
	StreamReadyPartial: {},
}

// toolUseStatusSet 覆盖 ToolUse.Status 4 vocab；与 ToolNodeStatus 全集相同。
var toolUseStatusSet = map[ToolNodeStatus]struct{}{
	ToolNodePending:  {},
	ToolNodePartial:  {},
	ToolNodeComplete: {},
	ToolNodeError:    {},
}

// toolResultStatusSet 覆盖 ToolResult.Status 2 vocab（complete/error）；
// ToolUseNode.Status 与 ToolResultNode.Status 共享 ToolNodeStatus 类型，但
// ToolResult 不允许 pending/partial（语义上必须已得结果）。
var toolResultStatusSet = map[ToolNodeStatus]struct{}{
	ToolNodeComplete: {},
	ToolNodeError:    {},
}

var redactionClassSet = map[RedactionClass]struct{}{
	RedactionPublic:       {},
	RedactionRedacted:     {},
	RedactionHidden:       {},
	RedactionProviderOnly: {},
}

var mediaTransportSet = map[MediaTransport]struct{}{
	MediaTransportInline: {},
	MediaTransportFile:   {},
	MediaTransportURL:    {},
	MediaTransportStream: {},
}

var liveTransportSet = map[LiveTransport]struct{}{
	LiveTransportWSS: {},
	LiveTransportSSE: {},
}

// ---- P-1 D2 扩展集合 ----

// textRoleSet 是 CanonicalMessage.Role 的合法取值（TextNode Role 与 CanonicalMessage 共用）。
//
// HUAKAI canonical role 4 vocab；与 OpenAI/Anthropic/Gemini 各 provider 的 role 集合都不重合，
// 在 ClientAdapter (P-2) 处做映射。"tool" role 用于 Anthropic 风格 tool_result 块（Anthropic
// 把 tool_result 仍嵌在 user message 里，但 HUAKAI canonical 抽出为独立 role）。
var textRoleSet = map[string]struct{}{
	"user":      {},
	"assistant": {},
	"system":    {},
	"tool":      {},
}

// cacheScopeSet 覆盖 CacheControlNode.Scope 5 vocab。
var cacheScopeSet = map[CacheScope]struct{}{
	CacheScopeRequest: {},
	CacheScopeMessage: {},
	CacheScopeBlock:   {},
	CacheScopeSession: {},
	CacheScopeVendor:  {},
}

// cacheLocalityHintSet 限定 CacheControlNode.LocalityHint 取值（PASR cache-aware 留位）。
//
// 与 capability_cache.go 注释中给出的建议白名单一致；P-0 仅记录、P-1 守门、PASR-cache D 切片
// 真正消费 selector。Hint 非空时必须落表内；空字符串合法（表示未声明 hint，selector 不参与）。
var cacheLocalityHintSet = map[string]struct{}{
	"account_pin":    {},
	"account_recent": {},
	"global":         {},
}

// structuredOutputModeSet 覆盖 StructuredOutputNode.Mode 4 vocab。
var structuredOutputModeSet = map[StructuredOutputMode]struct{}{
	StructuredOutputJSONMode:       {},
	StructuredOutputJSONSchema:     {},
	StructuredOutputToolStrategy:   {},
	StructuredOutputProviderNative: {},
}

// approvalStateSet 覆盖 ComputerUseNode.Approval 4 vocab。
var approvalStateSet = map[ApprovalState]struct{}{
	ApprovalRequired:    {},
	ApprovalGranted:     {},
	ApprovalDenied:      {},
	ApprovalNotRequired: {},
}

// computerEnvironmentSet 是 ComputerUseNode.Environment 白名单（P-1 锁定 5 vocab；只增不删）。
//
// 与 capability_computer_use.go 注释 "browser/desktop/shell/mobile/other" 对齐。
// "other" 是兜底；新 environment 应推 P-1.1 minor bump。
var computerEnvironmentSet = map[string]struct{}{
	"browser": {},
	"desktop": {},
	"shell":   {},
	"mobile":  {},
	"other":   {},
}

// batchStatusSet 覆盖 BatchNode.Validation 4 vocab。
var batchStatusSet = map[BatchStatus]struct{}{
	BatchPending:   {},
	BatchValidated: {},
	BatchFailed:    {},
	BatchComplete:  {},
}

// retryBackoffSet 限定 RetryPolicy.Backoff 白名单（P-1 锁定 3 vocab；只增不删）。
//
// 与 capability_batch.go 注释 "fixed/exponential/provider_default" 对齐。空字符串合法
// （表示未声明，由 provider 默认）。
var retryBackoffSet = map[string]struct{}{
	"fixed":            {},
	"exponential":      {},
	"provider_default": {},
}

// liveModalitySet 是 LiveSessionNode.Modalities 的合法取值（P-1 锁定 3 vocab）。
var liveModalitySet = map[string]struct{}{
	"text":  {},
	"audio": {},
	"video": {},
}

// ---- P-1 D4 扩展集合 ----

// protocolLossSeveritySet 覆盖 ProtocolLossSeverity 3 vocab（info/warning/error）。
//
// 用于 INV-45：所有 ProtocolLossEntry（node/edge/graph/projection 四处）的 Severity 字段
// 若非空必须落在该集合内。
var protocolLossSeveritySet = map[ProtocolLossSeverity]struct{}{
	ProtocolLossInfo:    {},
	ProtocolLossWarning: {},
	ProtocolLossError:   {},
}

// ---- P-1 D5 扩展集合 ----

// audioFormatSet 是 AudioNode.Format 的 P-1 白名单（只增不删）。
//
// 7 vocab 覆盖主流 provider 用法：wav (PCM unbox) / mp3 / opus (low-latency) /
// pcm16 (real-time)  / flac (lossless) / m4a (AAC) / webm (containerized opus)。
// 新格式应推 P-1.x minor bump。
var audioFormatSet = map[string]struct{}{
	"wav":   {},
	"mp3":   {},
	"opus":  {},
	"pcm16": {},
	"flac":  {},
	"m4a":   {},
	"webm":  {},
}
