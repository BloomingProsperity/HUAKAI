package hermes

import "testing"

func TestSanitizeArgs_RedactsAPIKey(t *testing.T) {
	// Regression: audit args must not disclose API keys into hermes_audit_events.
	input := map[string]any{"api_key": "sk-secret-123"}

	got := SanitizeArgs(input)

	// Mutation check: 将上面的 SanitizeArgs(input) 改成 input,或删除 sensitiveKey 的 api_key 分支,此断言会看到 sk-secret-123 并失败。
	if got["api_key"] != "[REDACTED]" {
		t.Fatalf("api_key=%q want [REDACTED]", got["api_key"])
	}
}

func TestSanitizeArgs_RedactsTokens(t *testing.T) {
	// Regression: access/refresh tokens must not leak through audit JSON.
	input := map[string]any{
		"access_token":  "access-secret",
		"refresh_token": "refresh-secret",
	}

	got := SanitizeArgs(input)

	// Mutation check: 翻转 sensitiveKey 的 token condition 为 false,这两个字段会保留原 secret 并让测试失败。
	if got["access_token"] != "[REDACTED]" || got["refresh_token"] != "[REDACTED]" {
		t.Fatalf("tokens not redacted: access=%q refresh=%q", got["access_token"], got["refresh_token"])
	}
}

func TestSanitizeArgs_RedactsPasswordAndSecret(t *testing.T) {
	// Regression: password/secret fields must not be persisted in audit metadata.
	input := map[string]any{
		"password": "p@ssw0rd",
		"secret":   "runner-secret",
	}

	got := SanitizeArgs(input)

	// Mutation check: 删除 password/secret 任一 sensitiveKey 分支,对应字段会等于原文并触发此断言。
	if got["password"] != "[REDACTED]" || got["secret"] != "[REDACTED]" {
		t.Fatalf("password/secret not redacted: password=%q secret=%q", got["password"], got["secret"])
	}
}

func TestSanitizeArgs_RecursesNestedMaps(t *testing.T) {
	// Regression: nested tool args must be sanitized before audit write, or inner API keys leak.
	input := map[string]any{"outer": map[string]any{"api_key": "sk-nested-secret"}}

	got := SanitizeArgs(input)

	outer, ok := got["outer"].(map[string]any)
	if !ok {
		t.Fatalf("outer=%T want nested map", got["outer"])
	}
	// Mutation check: 删除 sanitizeValue 对 map[string]any 的递归分支,inner api_key 会保持 sk-nested-secret 并失败。
	if outer["api_key"] != "[REDACTED]" {
		t.Fatalf("nested api_key=%q want [REDACTED]", outer["api_key"])
	}
}

func TestSanitizeArgs_PreservesNonSensitive(t *testing.T) {
	// Regression: audit sanitization must not corrupt non-sensitive routing/debug context.
	input := map[string]any{"tenant_id": int64(42), "name": "test"}

	got := SanitizeArgs(input)

	// Mutation check: 把 sanitizeValue 默认分支改成 [REDACTED],tenant_id/name 会被破坏并让测试失败。
	if got["tenant_id"] != int64(42) || got["name"] != "test" {
		t.Fatalf("non-sensitive fields changed: tenant_id=%v name=%v", got["tenant_id"], got["name"])
	}
}
