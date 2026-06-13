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

func TestSanitizeArgs_RedactsSensitiveKeyInTypedMap(t *testing.T) {
	// Regression (P2): a sensitive key inside a TYPED map (map[string]int64) must
	// be redacted. The old switch only recursed map[string]any, so this collection
	// fell through unredacted. Mutation check: revert sanitizeValue's default
	// branch to `return v` (the old switch) and the api_key value 7 survives as a
	// number under "api_key" instead of "[REDACTED]" — this assertion goes RED.
	input := map[string]any{
		"counts": map[string]int64{"api_key": 7, "requests": 9},
	}

	got := SanitizeArgs(input)

	counts, ok := got["counts"].(map[string]any)
	if !ok {
		t.Fatalf("counts=%T want sanitized map[string]any", got["counts"])
	}
	if counts["api_key"] != "[REDACTED]" {
		t.Fatalf("typed-map api_key=%v want [REDACTED] (sensitive key in map[string]int64 leaked)", counts["api_key"])
	}
	if counts["requests"] != int64(9) {
		t.Fatalf("typed-map non-sensitive value corrupted: requests=%v want 9", counts["requests"])
	}
}

func TestSanitizeArgs_RedactsSensitiveKeyInSliceOfTypedMaps(t *testing.T) {
	// Regression (P2): a secret under a sensitive key inside a []map[string]any
	// element must be redacted. The old switch handled []any but not the concrete
	// []map[string]any element type, so the inner map fell through. Mutation check:
	// revert sanitizeValue to the old two-case switch and the inner secret_token
	// survives verbatim in element 0 — this assertion goes RED.
	input := map[string]any{
		"items": []map[string]any{
			{"secret_token": "sk-typed-slice-leak", "ok": true},
		},
	}

	got := SanitizeArgs(input)

	items, ok := got["items"].([]any)
	if !ok {
		t.Fatalf("items=%T want []any after sanitize", got["items"])
	}
	if len(items) != 1 {
		t.Fatalf("items len=%d want 1", len(items))
	}
	elem, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("items[0]=%T want sanitized map", items[0])
	}
	if elem["secret_token"] != "[REDACTED]" {
		t.Fatalf("[]map secret_token=%v want [REDACTED] (sensitive key in []map[string]any leaked)", elem["secret_token"])
	}
	if elem["ok"] != true {
		t.Fatalf("[]map non-sensitive value corrupted: ok=%v want true", elem["ok"])
	}
}

func TestSanitizeArgs_RedactsCredentialsPayloadButKeepsDiagnosticFields(t *testing.T) {
	// Regression (PRIVACY): the renew_trigger "credentials" payload arg must be
	// fully redacted (even as a raw string, where nested-key recursion cannot
	// help), while the SINGULAR non-secret diagnostic fields (credential_id /
	// credential_version) must SURVIVE so read-only credential diagnostics aren't
	// degraded. Mutation check: broaden the matcher to "credential" (singular) and
	// credential_id is wrongly redacted (the survive-assertion goes RED); remove
	// the "credentials" clause and the raw payload string leaks (the redact
	// assertion goes RED).
	input := map[string]any{
		"credentials":        "raw-secret-blob-not-an-object",
		"credential_id":      int64(3),
		"credential_version": int64(4),
	}

	got := SanitizeArgs(input)

	if got["credentials"] != "[REDACTED]" {
		t.Fatalf("credentials payload=%v want [REDACTED] (rotated material leaked)", got["credentials"])
	}
	if got["credential_id"] != int64(3) || got["credential_version"] != int64(4) {
		t.Fatalf("singular diagnostic fields wrongly redacted: id=%v ver=%v", got["credential_id"], got["credential_version"])
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
