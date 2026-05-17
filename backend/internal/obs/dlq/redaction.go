package dlq

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

const redacted = "[REDACTED]"

var secretKeyPattern = regexp.MustCompile(`(?i)(token|cookie|credential|secret|password|prompt|completion|api[_-]?key|authorization)`)

func RedactString(value string) string {
	value = auth.SanitizeOAuthMessage(value)
	if secretKeyPattern.MatchString(value) {
		return secretKeyPattern.ReplaceAllString(value, redacted)
	}
	return strings.TrimSpace(value)
}

func SanitizePayload(raw json.RawMessage) json.RawMessage {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return json.RawMessage(`{}`)
	}
	clean := sanitizeValue("", v)
	out, err := json.Marshal(clean)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(out)
}

func sanitizeValue(key string, value any) any {
	if sensitiveKey(key) {
		return redacted
	}
	switch t := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			out[k] = sanitizeValue(k, v)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, v := range t {
			out[i] = sanitizeValue(key, v)
		}
		return out
	case string:
		return auth.SanitizeOAuthMessage(t)
	default:
		return value
	}
}

func sensitiveKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return false
	}
	for _, safe := range []string{
		"credential_id", "credential_version", "account_credential_id",
		"token_count", "cache_creation_tokens", "cache_read_tokens",
		"body_envelope",
	} {
		if strings.Contains(k, safe) {
			return false
		}
	}
	for _, marker := range []string{
		"access_token", "refresh_token", "id_token", "bearer",
		"cookie", "password", "secret", "authorization", "api_key",
		"prompt", "completion",
	} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return k == "token" || strings.HasSuffix(k, "_token")
}

func ContainsForbiddenRawData(raw []byte) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		return containsForbiddenValue(v)
	}
	return containsForbiddenString(string(raw))
}

func containsForbiddenValue(value any) bool {
	switch t := value.(type) {
	case map[string]any:
		for _, v := range t {
			if containsForbiddenValue(v) {
				return true
			}
		}
	case []any:
		for _, v := range t {
			if containsForbiddenValue(v) {
				return true
			}
		}
	case string:
		return containsForbiddenString(t)
	}
	return false
}

func containsForbiddenString(value string) bool {
	text := strings.ToLower(value)
	for _, marker := range []string{
		"sk-", "toolu_", "aiv_", "gho_", "ant-", "bearer ",
		"access_token", "refresh_token", "id_token", "cookie",
		"credential", "prompt", "completion", "password",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
