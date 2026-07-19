package dlq

import (
	"regexp"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

// redactedMarker 是脱敏后落库的占位符,operator 仍能看到"失败已发生"但看不到敏感明文。
const redactedMarker = "[REDACTED]"

// secretKeyReasonPattern 与 obs/dlq.RedactString 使用的模式保持一致:命中凭证/密钥/prompt/completion
// 等敏感关键字即整体脱敏。money-path DLQ 的 failure reason 若嵌入 payload/凭证派生文本,必须挡在落库前。
var secretKeyReasonPattern = regexp.MustCompile(`(?i)(access[_-]?token|refresh[_-]?token|id[_-]?token|cookie|credential|secret|password|prompt|completion|api[_-]?key|authorization|bearer\s+[a-z0-9._-]+)`)

// redactFailureReason 在把 handler 错误串写入 usage_record_dlq.replay_failure_reason(经 operator
// List/GetByID API 外泄)之前对其脱敏,与 obs outbox DLQ(RedactString)对齐,消除 money-path DLQ
// 相较告警 DLQ 的 log-privacy 弱化。命中敏感关键字或 privacy 禁止的原始数据标记即整体替换为占位符;
// 否则原样返回,保留 uuid.Parse/json/pgx 等良性诊断信息。
func redactFailureReason(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return ""
	}
	if secretKeyReasonPattern.MatchString(trimmed) || privacy.ContainsForbiddenRawData([]byte(trimmed)) {
		return redactedMarker
	}
	return trimmed
}
