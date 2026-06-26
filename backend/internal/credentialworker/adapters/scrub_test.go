package adapters

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// R2 S2 (Owner):
// mergeTokenResponse 写回 store 前必须主动删 hostile credential 字段,
// 防止 cred 残留攻击面被未来 refresh / ingest path 意外读取。
//
// 判别 mutation: 删 mergeTokenResponse 中 delete(cred, "oauth_token_endpoint")
// 等任一行 → 对应 sub-test 立即变红 — store 里仍能取出 attacker 值。
func TestMergeTokenResponseScrubsHostileCredentialFields(t *testing.T) {
	// caller 构造的 cred 含 attacker plant 的 hostile 字段 (模拟历史 ingest 时
	// 写入, 或 cred payload 篡改后留下的残留)。
	cred := map[string]any{
		"access_token":           "old-at",
		"refresh_token":          "rt-old",
		"oauth_token_endpoint":   "http://attacker.test/v1/oauth/token",
		"client_secret":          "leaked-secret",
		"fallback_client_id":     "attacker-fallback-cid",
		"setup_token":            "attacker-setup",
		"long_lived_setup_token": "attacker-long-lived",
		// 合法 metadata, 必须保留:
		"client_id":         "operator-cid",
		"scope_metadata":    "user-scope",
		"keep_this_field":   "yes",
		"cross_client_fallback_attempted": true,
	}
	resp := tokenResponse{
		AccessToken: "new-at", RefreshToken: "new-rt",
		ExpiresIn: 3600, TokenType: "bearer",
	}

	raw, _, err := mergeTokenResponse(cred, resp)
	if err != nil {
		t.Fatalf("mergeTokenResponse: %v", err)
	}

	var merged map[string]any
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}

	// hostile 字段必须全 scrub
	for _, key := range []string{
		"oauth_token_endpoint",
		"client_secret",
		"fallback_client_id",
		"setup_token",
		"long_lived_setup_token",
	} {
		if v, present := merged[key]; present {
			t.Fatalf("hostile key %q 未被 scrub, 残留 value=%v", key, v)
		}
	}

	// 合法字段必须保留
	for _, key := range []string{
		"client_id", "scope_metadata", "keep_this_field",
		"cross_client_fallback_attempted",
	} {
		if _, present := merged[key]; !present {
			t.Fatalf("合法字段 %q 被错误 scrub", key)
		}
	}

	// 上游响应字段必须写入
	if merged["access_token"] != "new-at" {
		t.Fatalf("access_token=%v want new-at", merged["access_token"])
	}
	if merged["refresh_token"] != "new-rt" {
		t.Fatalf("refresh_token=%v want new-rt", merged["refresh_token"])
	}

	// 防退化 sanity: marshal output 不含 hostile 字段子串 (额外 belt-and-suspenders)
	rawStr := string(raw)
	for _, hostileSubstring := range []string{
		"attacker.test",
		"leaked-secret",
		"attacker-fallback-cid",
		"attacker-setup",
		"attacker-long-lived",
	} {
		if strings.Contains(rawStr, hostileSubstring) {
			t.Fatalf("scrub 后 marshal 仍含 %q: %s", hostileSubstring, rawStr)
		}
	}
	_ = time.Now // 保留 time import（resp.ExpiresIn 间接用到）
}
