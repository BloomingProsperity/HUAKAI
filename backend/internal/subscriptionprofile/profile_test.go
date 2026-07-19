package subscriptionprofile

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestVendorScopedLabelsDoNotCollide(t *testing.T) {
	openAI := FromRaw(VendorOpenAI, "Pro", SourceOAuthResponse, TrustIssuerResponse, VerificationIssuerResponse, "user-1", "")
	anthropic := FromRaw(VendorAnthropic, "Pro", SourceProviderAPI, TrustVerifiedAPI, VerificationVerified, "user-2", "")
	if openAI.Label() != "openai:pro" || anthropic.Label() != "anthropic:pro" {
		t.Fatalf("系统标签未按厂商隔离：openai=%q anthropic=%q", openAI.Label(), anthropic.Label())
	}
	if openAI.Label() == anthropic.Label() {
		t.Fatal("删除厂商前缀后本测试必须失败，避免不同厂商的 Pro 被合并")
	}
}

func TestOpenAIKnownAliasesAndUnknownRaw(t *testing.T) {
	tests := []struct {
		raw, plan, scope string
	}{
		{"Plus", "plus", ScopePersonal},
		{"prolite", "pro_lite", ScopePersonal},
		{"hc", "enterprise", ScopeWorkspace},
		{"education", "education", ScopeWorkspace},
		{"self_serve_business_usage_based", "business_usage_based", ScopeWorkspace},
	}
	for _, test := range tests {
		got := FromRaw(VendorOpenAI, test.raw, SourceIDTokenClaim, TrustUnverifiedJWT, VerificationUnverified, "u", "w")
		if got.Plan != test.plan || got.Scope != test.scope || got.Status != StatusObserved {
			t.Fatalf("raw=%q 得到 %+v", test.raw, got)
		}
	}
	unknown := FromRaw(VendorOpenAI, "future-premium", SourceIDTokenClaim, TrustUnverifiedJWT, VerificationUnverified, "u", "")
	if unknown.Plan != PlanUnknown || unknown.RawPlan != "future-premium" || unknown.Status != StatusUnknownValue || unknown.Label() != "openai:unknown" {
		t.Fatalf("未知套餐必须保留原值且不能伪装成 Free：%+v", unknown)
	}
}

func TestDetectPayloadPrefersTokenClaimOverStalePayloadField(t *testing.T) {
	idToken := jwt(t, map[string]any{
		"sub": "user-1",
		openAIAuthClaimKey: map[string]any{
			"chatgpt_plan_type":  "Pro",
			"chatgpt_account_id": "workspace-1",
		},
	})
	raw, err := json.Marshal(map[string]any{
		"id_token": idToken, "chatgpt_plan_type": "Free",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := DetectPayload(VendorOpenAI, "codex_cli_oauth", raw)
	if got.Plan != "pro" || got.Source != SourceIDTokenClaim || got.SubjectRef != "user-1" || got.WorkspaceRef != "" {
		t.Fatalf("新令牌声明必须胜过载荷中的旧套餐字段：%+v", got)
	}
}

func TestMissingAndMalformedRemainDifferentFromFree(t *testing.T) {
	missing := DetectPayload(VendorAnthropic, "claude_setup_token", []byte(`{"setup_token":"secret"}`))
	if missing.Status != StatusMissing || missing.Plan != PlanUnknown || missing.Label() != "anthropic:unknown" {
		t.Fatalf("缺字段结果错误：%+v", missing)
	}
	malformed := DetectPayload(VendorOpenAI, "codex_cli_oauth", []byte(`{"id_token":"broken"}`))
	if malformed.Status != StatusParseFailed || malformed.Plan == "free" {
		t.Fatalf("坏令牌不能伪装成免费套餐：%+v", malformed)
	}
}

func TestAntigravityTierAliases(t *testing.T) {
	pro := FromRaw(VendorAntigravity, "g1-pro-tier", SourceProviderAPI, TrustVerifiedAPI, VerificationVerified, "u", "")
	ultra := FromRaw(VendorAntigravity, "g1-ultra-tier", SourceProviderAPI, TrustVerifiedAPI, VerificationVerified, "u", "")
	if pro.Label() != "antigravity:pro" || ultra.Label() != "antigravity:ultra" {
		t.Fatalf("Antigravity 套餐映射错误：pro=%+v ultra=%+v", pro, ultra)
	}
}

func TestAntigravityPayloadCannotSelfAssertProviderTrust(t *testing.T) {
	claimed := DetectPayload("gemini", "antigravity", []byte(`{
		"subscription_tier_raw":"g1-pro-tier",
		"subscription_metadata_status":"resolved"
	}`))
	if claimed.Label() != "antigravity:pro" || claimed.Source != SourceImportPayload ||
		claimed.Trust != TrustImported || claimed.Verification != VerificationUnverified {
		t.Fatalf("凭据 JSON 不得靠自报状态获得上游已验证等级：%+v", claimed)
	}
	claimedConflict := DetectPayload("gemini", "antigravity", []byte(`{
		"subscription_metadata_status":"conflict"
	}`))
	if claimedConflict.Status != StatusMissing || claimedConflict.Trust != TrustImported {
		t.Fatalf("凭据 JSON 不得自行铸造已验证冲突：%+v", claimedConflict)
	}
}

func jwt(t *testing.T, claims map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(raw) + ".signature"
}
