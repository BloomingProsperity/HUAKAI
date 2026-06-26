package hermes

import "testing"

func TestSanitizeArgs_RedactsAPIKey(t *testing.T) {
	// 回归:audit args 绝不能把 API key 泄露进 hermes_audit_events。
	input := map[string]any{"api_key": "sk-secret-123"}

	got := SanitizeArgs(input)

	// Mutation check: 将上面的 SanitizeArgs(input) 改成 input,或删除 sensitiveKey 的 api_key 分支,此断言会看到 sk-secret-123 并失败。
	if got["api_key"] != "[REDACTED]" {
		t.Fatalf("api_key=%q want [REDACTED]", got["api_key"])
	}
}

func TestSanitizeArgs_RedactsTokens(t *testing.T) {
	// 回归:access/refresh token 绝不能通过 audit JSON 泄露。
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
	// 回归:password/secret 字段绝不能被持久化进 audit metadata。
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
	// 回归:嵌套的 tool args 必须在 audit 写入前 sanitize,否则内层 API key 会泄露。
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
	// 回归(P2):typed map(map[string]int64)里的敏感 key 必须
	// 被 redact。旧的 switch 只对 map[string]any 递归,因此这个 collection
	// 会未经 redact 漏过。变异检查:把 sanitizeValue 的 default
	// 分支还原成 `return v`(旧 switch),api_key 的值 7 就会以
	// 数字形式保留在 "api_key" 下而非 "[REDACTED]"——此断言变红。
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
	// 回归(P2):[]map[string]any 元素里某个敏感 key 下的 secret
	// 必须被 redact。旧的 switch 处理了 []any,却没处理具体的
	// []map[string]any 元素类型,因此内层 map 会漏过。变异检查:
	// 把 sanitizeValue 还原成旧的两分支 switch,内层 secret_token
	// 就会原文保留在 element 0 中——此断言变红。
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
	// 回归(隐私):renew_trigger 的 "credentials" payload 参数必须
	// 被完全 redact(即便是原始字符串,此时嵌套 key 的递归也无能
	// 为力),而单数的非密诊断字段(credential_id /
	// credential_version)必须保留,以免只读凭据诊断被
	// 削弱。变异检查:把匹配放宽到 "credential"(单数),
	// credential_id 就会被错误 redact(保留断言变红);移除
	// "credentials" 子句,则原始 payload 字符串会泄露(redact
	// 断言变红)。
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
	// 回归:audit 的 sanitize 绝不能破坏非敏感的 routing/debug 上下文。
	input := map[string]any{"tenant_id": int64(42), "name": "test"}

	got := SanitizeArgs(input)

	// Mutation check: 把 sanitizeValue 默认分支改成 [REDACTED],tenant_id/name 会被破坏并让测试失败。
	if got["tenant_id"] != int64(42) || got["name"] != "test" {
		t.Fatalf("non-sensitive fields changed: tenant_id=%v name=%v", got["tenant_id"], got["name"])
	}
}
