// Package redact 实现 HUAKAI 信任链 T0 的第一片：系统日志字段 allowlist +
// redactor，保证 SRE 用的 system_log 永远不含 prompt / completion / tool 内容 /
// PII 等用户数据。
//
// "无用户数据保留日志/日志只做系统报错" —— 现有
// gateway / proto / handler 暂时只用 stdlib，但等接 zap/zerolog/slog 时所有
// 入口必须经过本包 IsSafeField + Redact 过滤。
//
// 与 internal/auth/audit.go 的关系：
//   - internal/auth/audit RefreshAuditEntry 是凭据 refresh 子系统的小 audit
//     记录器（不归本包管）。
//   - 本包专为通用 system_log 边界服务，单独 import path。
//
// 与 trust-chain T4（signed AuditLedger）的关系：
//   - 本包只解决"日志不能含 body"边界；不写签名。
//   - T4 落 audit_ledger 独立模块，与 system_log 物理分离。
package redact

// systemLogSafeFields 是经过验证可安全写入 system_log 的字段名 allowlist。
//
// 规则：
//   - 字段值不允许携带用户 prompt / completion / tool 内容 / metadata 中含 PII 的部分。
//   - 数值类（token count / latency / status）自由通过。
//   - hash 类（hash 后的 ID）通过 — hash 前的明文 ID 禁。
//   - 任何新增字段必须 review 后 append；不允许默认 fallthrough。
//
// 来源参考 Claude trust-chain plan §4：
//   - request 追踪：request_id / trace_id
//   - 身份（hash 后）：tenant_id / key_id_hash / account_id_hash
//   - 模型校验：model_requested / model_actual / upstream_model_reported
//   - 用量与计费：token_count_input / token_count_output / cache_hit_ratio /
//     cost_usd_cents
//   - 性能：latency_ms_total / latency_ms_first_token / latency_ms_tta
//   - 错误：status_code / error_class / error_code
//   - 路由：pool_id / route_id / provider
//   - 协议元数据：ingress_path / client_protocol / protocol_family / evidence_label
var systemLogSafeFields = map[string]struct{}{
	"request_id":               {},
	"trace_id":                 {},
	"tenant_id":                {},
	"key_id_hash":              {},
	"account_id_hash":          {},
	"model_requested":          {},
	"model_actual":             {},
	"upstream_model_reported":  {},
	"token_count_input":        {},
	"token_count_output":       {},
	"token_count_total":        {},
	"cache_hit_ratio":          {},
	"cache_creation_tokens":    {},
	"cache_read_tokens":        {},
	"cost_usd_cents":           {},
	"latency_ms_total":         {},
	"latency_ms_first_token":   {},
	"latency_ms_tta":           {},
	"latency_ms_upstream":      {},
	"status_code":              {},
	"error_class":              {},
	"error_code":               {},
	"pool_id":                  {},
	"route_id":                 {},
	"provider":                 {},
	"ingress_path":             {},
	"client_protocol":          {},
	"protocol_family":          {},
	"evidence_label":           {},
	"upstream_protocol":        {},
	"attempt":                  {},
	"retry_reason":             {},
}

// IsSafeField 判断字段名是否在 allowlist 中。
//
// 调用方应在 log entry 构造每个 field 前调用本函数；返回 false 时**禁止**写入
// system_log（应改写入 audit_ledger，或经 hash 后才能记）。
func IsSafeField(name string) bool {
	if name == "" {
		return false
	}
	_, ok := systemLogSafeFields[name]
	return ok
}

// SystemLogSafeFieldsSnapshot 返回 allowlist 的浅拷贝，仅用于测试或诊断
// （CI 字段漂移检查 / dashboard 显示当前生效字段集 等）。
func SystemLogSafeFieldsSnapshot() map[string]struct{} {
	out := make(map[string]struct{}, len(systemLogSafeFields))
	for k := range systemLogSafeFields {
		out[k] = struct{}{}
	}
	return out
}
