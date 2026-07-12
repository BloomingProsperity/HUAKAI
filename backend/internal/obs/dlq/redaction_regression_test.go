package dlq

import (
	"encoding/json"
	"testing"
)

// 复审 S2 爆炸半径守卫(跨 privacy→dlq):SanitizePayload 是「白名单保留 + 第二道
// ContainsForbiddenRawData 兜底」的双守卫结构。当 payload 含合法 credential_state
// 字段(白名单保留)且另有一个被丢弃的敏感字段(raw_body)时,第二道守卫曾因 map key
// 子串扫把 credential_state 键名误判为秘密,导致整条塌成 privacy_guard_hit sentinel,
// request_id 等诊断字段全丢——两道守卫自相矛盾。收敛 key 扫描后诊断字段必须保住。
//
// 变异靶:privacy/default_redactor.go 的 map key 分支改回 containsForbiddenString(k)
// 宽词子串扫 → credential_state 键名被判 forbidden → 第二道守卫塌成 sentinel → 本测试红。
func TestSanitizePayload_KeepsDiagnosticsWithLegitCredentialField(t *testing.T) {
	raw := json.RawMessage(`{"credential_state":"active","request_id":"r1","raw_body":"topsecret-value"}`)
	out := SanitizePayload(raw)

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("输出非合法 JSON: %v (%s)", err, out)
	}

	// 不得整条塌成 sentinel(sentinel 形态带 error_class)。
	if _, ok := m["error_class"]; ok {
		t.Fatalf("payload 被误塌成 sentinel,诊断字段全丢: %s", out)
	}
	// 合法诊断字段必须保住。
	if m["request_id"] != "r1" {
		t.Errorf("request_id 诊断字段丢失: %s", out)
	}
	if m["credential_state"] != "active" {
		t.Errorf("credential_state 合法字段丢失: %s", out)
	}
	// 真敏感字段仍须被丢弃(不出现明文)。
	if v, ok := m["raw_body"]; ok {
		t.Errorf("敏感字段 raw_body 未被丢弃,明文=%v", v)
	}
}
