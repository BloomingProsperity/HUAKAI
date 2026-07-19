package clienterr

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const (
	MessageModeFixed        = "fixed"
	MessageModeCustom       = "custom"
	MessageModeUpstreamSafe = "upstream_safe"

	MaxProjectedMessageRunes = 256
	maxUpstreamMessageBody   = 32 * 1024
)

// SafeConfiguredMessage 校验运营者配置的客户端错误文本。
// 文本一旦需要脱敏或命中秘密探针就直接拒绝，避免把看似可控的配置变成泄密入口。
func SafeConfiguredMessage(value string) (string, bool) {
	return safeProjectedMessage(value, true)
}

// SafeUpstreamMessage 只读取已知 JSON message 字段，并在严格清洗后返回。
// 任意非 JSON、超长、秘密命中或未知结构都回退为空，由调用方使用固定错误目录。
func SafeUpstreamMessage(body []byte) string {
	if len(body) == 0 || len(body) > maxUpstreamMessageBody {
		return ""
	}
	var envelope struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	candidate := envelope.Error.Message
	if strings.TrimSpace(candidate) == "" {
		candidate = envelope.Message
	}
	message, ok := safeProjectedMessage(candidate, true)
	if !ok {
		return ""
	}
	return message
}

func safeProjectedMessage(value string, requireUnchanged bool) (string, bool) {
	if !utf8.ValidString(value) {
		return "", false
	}
	message := strings.Join(strings.Fields(value), " ")
	if message == "" || utf8.RuneCountInString(message) > MaxProjectedMessageRunes {
		return "", false
	}
	sanitized := auth.SanitizeOAuthMessage(message)
	if sanitized == "" || (requireUnchanged && sanitized != message) {
		return "", false
	}
	if privacy.ContainsForbiddenRawData([]byte(sanitized)) {
		return "", false
	}
	return sanitized, true
}
