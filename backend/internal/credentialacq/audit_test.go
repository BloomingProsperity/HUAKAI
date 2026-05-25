package credentialacq

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

var secretKeyPattern = regexp.MustCompile(`(?i)(access[_-]?token|refresh[_-]?token|session[_-]?token|api[_-]?key|private[_-]?key|authorization|cookie|secret|pkce)`)

func sanitizeAuditPayload(input map[string]any) map[string]any {
	out := map[string]any{}
	credentialsPresent := false
	for key, value := range input {
		if secretKeyPattern.MatchString(key) {
			credentialsPresent = true
			continue
		}
		out[key] = sanitizeAuditValue(value, &credentialsPresent)
	}
	if credentialsPresent {
		out["credentials_present"] = true
	}
	return out
}

func sanitizeAuditValue(value any, credentialsPresent *bool) any {
	switch v := value.(type) {
	case string:
		if looksLikeSecretValue(v) {
			*credentialsPresent = true
			return "[REDACTED]"
		}
		return v
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, sanitizeAuditValue(item, credentialsPresent))
		}
		return out
	case map[string]any:
		return sanitizeAuditPayload(v)
	default:
		return v
	}
}

func looksLikeSecretValue(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "bearer ") ||
		strings.HasPrefix(lower, "sk-") ||
		strings.Contains(lower, "refresh") && strings.Contains(lower, "token") ||
		strings.Contains(lower, "session") && strings.Contains(lower, "value") ||
		strings.Contains(lower, "private key")
}

func TestAuditPayloadContainsNoTokenShapedSubstring(t *testing.T) {
	payload := map[string]any{
		"tenant_id":        int64(1),
		"vendor":           "openai",
		"auth_mode":        "chatgpt_oauth",
		"flow_kind":        "oauth",
		"access_token":     "sk-test-secret-value",
		"refresh_token":    "refresh token should not appear",
		"authorization":    "Bearer session-secret-value",
		"safe_context":     map[string]any{"account_email_hash": "sha256:example"},
		"operator_message": "exchange failed after upstream denial",
	}
	sanitized := sanitizeAuditPayload(payload)
	raw, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"access_token", "refresh_token", "authorization", "sk-test", "Bearer", "session-secret"} {
		if strings.Contains(string(raw), banned) {
			t.Fatalf("audit payload leaked %q: %s", banned, raw)
		}
	}
	if sanitized["credentials_present"] != true {
		t.Fatalf("credentials_present=%v want true", sanitized["credentials_present"])
	}
	if !strings.Contains(string(raw), "account_email_hash") {
		t.Fatalf("safe redacted context missing: %s", raw)
	}
}
