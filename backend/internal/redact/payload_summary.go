package redact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	payloadLogSnippetMaxBytes = 160
	payloadLogInspectMaxBytes = 4096
)

type PayloadLogSummary struct {
	PayloadBytes               int
	PayloadSummarySHA256Prefix string
	PayloadSnippet             string
}

// SafePayloadLogSummary 返回用于关联分析的数据,且不会写入原始 payload 字节。
func SafePayloadLogSummary(payload []byte) string {
	summary := SafePayloadLogSummaryFields(payload)
	return fmt.Sprintf(
		"payload_bytes=%d payload_summary_sha256_prefix=%s payload_snippet=%q",
		summary.PayloadBytes,
		summary.PayloadSummarySHA256Prefix,
		summary.PayloadSnippet,
	)
}

// SafePayloadLogSummaryFields 返回已脱敏且有长度边界的结构化字段,供隐私日志逐字段校验。
func SafePayloadLogSummaryFields(payload []byte) PayloadLogSummary {
	snippet := redactedPayloadSnippet(payload, payloadLogSnippetMaxBytes)
	sum := sha256.Sum256([]byte(snippet))
	return PayloadLogSummary{
		PayloadBytes:               len(payload),
		PayloadSummarySHA256Prefix: hex.EncodeToString(sum[:8]),
		PayloadSnippet:             snippet,
	}
}

// SafePayloadLogAttrs 返回隐私 allowlist 可接受的字段,避免整段摘要超过单字段长度上限。
func SafePayloadLogAttrs(payload []byte) map[string]any {
	summary := SafePayloadLogSummaryFields(payload)
	return map[string]any{
		"payload_bytes":                 summary.PayloadBytes,
		"payload_summary_sha256_prefix": summary.PayloadSummarySHA256Prefix,
		"payload_snippet":               summary.PayloadSnippet,
	}
}

func redactedPayloadSnippet(payload []byte, maxBytes int) string {
	if len(payload) > payloadLogInspectMaxBytes {
		return "[payload_too_large_redacted]"
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return "[non_json_payload_redacted]"
	}
	raw, err := json.Marshal(redactPayloadJSONValue(value))
	if err != nil {
		return "[json_payload_redacted]"
	}
	return capSnippet(strings.ToValidUTF8(string(raw), "?"), maxBytes)
}

func redactPayloadJSONValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		redactedKeys := 0
		for _, k := range keys {
			outKey, ok := redactedPayloadJSONKey(k)
			if !ok {
				redactedKeys++
				outKey = fmt.Sprintf("field_redacted_%d", redactedKeys)
			}
			out[outKey] = redactPayloadJSONValue(v[k])
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = redactPayloadJSONValue(child)
		}
		return out
	case string:
		return "[REDACTED]"
	case float64:
		return "[REDACTED_NUMBER]"
	case bool:
		return "[REDACTED_BOOL]"
	default:
		return v
	}
}

func redactedPayloadJSONKey(key string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "code", "detail", "details", "error", "message", "reason", "retryable", "status", "type":
		return normalized, true
	}
	return "", false
}

func capSnippet(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	var b strings.Builder
	for _, r := range value {
		runeLen := utf8.RuneLen(r)
		if runeLen < 0 || b.Len()+runeLen > maxBytes {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "...[truncated]"
}
