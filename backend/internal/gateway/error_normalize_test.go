package gateway

import (
	"net/http"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
)

// AT-RATE-021 —— 401 响应体中含 invalid_grant -> R-001 -> iron_clad -> disabled。
func TestClassify_R001_InvalidGrant(t *testing.T) {
	c, err := Classify(401, nil, []byte(`{"error":"invalid_grant"}`), "openai")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID != "R-001" || c.Class != ErrorClassOAuthInvalidGrant {
		t.Fatalf("got rule=%s class=%s; want R-001 oauth_invalid_grant", c.RuleID, c.Class)
	}
	if c.Tier != TierIronClad || c.FsmTransition != FsmTransitionDisabled {
		t.Fatalf("iron_clad must reach disabled; got tier=%s fsm=%s", c.Tier, c.FsmTransition)
	}
}

// AT-RATE-022 —— 通用 5xx -> R-015 -> ambiguous -> 降级,而非 disabled。
func TestClassify_R015_5xx_NeverDisabled(t *testing.T) {
	c, err := Classify(503, nil, []byte("internal error"), "openai")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID != "R-015" || c.Class != ErrorClassServerError {
		t.Fatalf("got rule=%s class=%s; want R-015", c.RuleID, c.Class)
	}
	if c.Tier != TierAmbiguous || c.FsmTransition == FsmTransitionDisabled {
		t.Fatalf("ambiguous 5xx must NOT reach disabled; got tier=%s fsm=%s", c.Tier, c.FsmTransition)
	}
}

// enableAuthLaneRules 在测试内临时打开车道绑定规则(R-024/R-025 + xai 归一化),测试结束自动还原。
func enableAuthLaneRules(t *testing.T) {
	t.Helper()
	SetAuthLaneRulesEnabled(true)
	t.Cleanup(func() { SetAuthLaneRulesEnabled(false) })
}

// 缺口①附带修复(knob 开时生效)—— Grok/xAI 坏 key 返 400(非 401)带 invalid_api_key/
// incorrect api key 文本 -> R-024/R-025 -> token_revoked 类。此前无规则命中 → R-016 通配 → unknown →
// upstream_client_4xx 直接透传给客户端(既不换号也不冷却)。判别:删掉 R-024/R-025 → 落回 R-016
// unknown,断言红。
func TestClassify_R024_R025_GrokBadKey400(t *testing.T) {
	enableAuthLaneRules(t)
	cases := []struct {
		name     string
		provider string
		body     string
		wantRule string
	}{
		{"grok_invalid_api_key", "grok", `{"code":"400","error":"invalid_api_key: bad key"}`, "R-024"},
		{"grok_incorrect_api_key", "grok", `{"error":{"message":"Incorrect API key provided"}}`, "R-025"},
		{"xai_alias_invalid_api_key", "xai", `{"error":"invalid_api_key"}`, "R-024"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Classify(400, nil, []byte(c.body), c.provider)
			if err != nil {
				t.Fatalf("classify err: %v", err)
			}
			if got.RuleID != c.wantRule || got.Class != ErrorClassTokenRevoked {
				t.Fatalf("got rule=%s class=%s; want %s token_revoked", got.RuleID, got.Class, c.wantRule)
			}
		})
	}
	// 反例:grok 400 但非认证类文本 → 不应命中 R-024/R-025(保守 keyword,不误禁好号)。
	got, _ := Classify(400, nil, []byte(`{"error":"model not found"}`), "grok")
	if got.RuleID == "R-024" || got.RuleID == "R-025" {
		t.Fatalf("非认证类 grok 400 误命中 auth 规则:rule=%s", got.RuleID)
	}
}

// TestClassify_GrokAuthRulesGatedByKnob(审查 S1):knob 关(默认生产态)时 R-024/R-025 与
// xai→grok 归一化必须不生效——grok/xai 400 坏 key 保持基底行为(R-016 unknown → 400 原样透传
// + error-rate 健康记账),客户端契约与健康记账零变化。判别:去掉 matchRule 的 RequiresAuthLane
// 门控或 normalizeProvider 的 knob 判断 → 命中 R-024,断言红。
func TestClassify_GrokAuthRulesGatedByKnob(t *testing.T) {
	// 不 enable(模拟默认关);防其它测试残留,显式置 false 并还原。
	SetAuthLaneRulesEnabled(false)
	t.Cleanup(func() { SetAuthLaneRulesEnabled(false) })
	for _, provider := range []string{"grok", "xai"} {
		got, err := Classify(400, nil, []byte(`{"error":"invalid_api_key"}`), provider)
		if err != nil {
			t.Fatalf("classify err: %v", err)
		}
		if got.RuleID != "R-016" || got.Class != ErrorClassUnknown {
			t.Fatalf("knob 关时 %s 400 坏 key 必须保持基底分类 R-016 unknown,实际 rule=%s class=%s", provider, got.RuleID, got.Class)
		}
	}
}

// AT-RATE-023 —— 未知错误 -> R-016 兜底规则 -> pass_through。
func TestClassify_R016_Wildcard(t *testing.T) {
	c, err := Classify(418, nil, []byte("teapot"), "fictional_provider")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID != "R-016" || c.RetryAction != RetryActionPassThrough {
		t.Fatalf("got rule=%s action=%s; want R-016 pass_through", c.RuleID, c.RetryAction)
	}
}

func TestStatusCodeRemap(t *testing.T) {
	body := []byte(`{"error":{"message":"bad upstream input"}}`)
	remap := map[int]int{http.StatusBadRequest: http.StatusInternalServerError}
	if got := RemapClientStatus(http.StatusBadRequest, remap); got != http.StatusInternalServerError {
		t.Fatalf("RemapClientStatus(400)=%d want 500; body must remain %s", got, body)
	}
	if string(body) != `{"error":{"message":"bad upstream input"}}` {
		t.Fatalf("status remap must not mutate body, got %s", body)
	}
	if got := RemapClientStatus(http.StatusBadRequest, nil); got != http.StatusBadRequest {
		t.Fatalf("nil mapping changed status to %d; want original 400", got)
	}
	if got := RemapClientStatus(http.StatusNotFound, remap); got != http.StatusNotFound {
		t.Fatalf("unmapped status changed to %d; want original 404", got)
	}
	// 变异:忽略 map 会让这里保留 400,使第一个
	// 断言失败,而 body 保留守卫仍记录了适用范围。
}

// DR-009 6.6 硬底线不变量:任何 ambiguous 规则都不能得出 disabled。
func TestSixSixInvariant_AmbiguousNeverDisables(t *testing.T) {
	for _, r := range errorRules {
		if r.Tier != TierAmbiguous {
			continue
		}
		got := transitionFor(r.Action, r.Tier)
		if got == FsmTransitionDisabled {
			t.Fatalf("rule %s ambiguous->disabled violation", r.RuleID)
		}
	}
}

// IsIronCladKeyword 必须恰好是任务规格里的 5 个关键词。
func TestIsIronCladKeyword_Exactly5(t *testing.T) {
	want := []string{"invalid_grant", "identity verification", "org_disabled", "token_revoked", "deactivated_workspace"}
	for _, k := range want {
		if !IsIronCladKeyword(k) {
			t.Errorf("missing iron_clad keyword %q", k)
		}
	}
	for _, k := range []string{
		"token_invalidated",
		"credit",
		"credit balance",
		"validation",
		"throttling",
		"ThrottlingException",
		"ServiceUnavailableException",
	} {
		if IsIronCladKeyword(k) {
			t.Errorf("keyword %q must NOT be iron_clad (only 5 are)", k)
		}
	}
	if len(IronCladKeywords) != 5 {
		t.Errorf("IronCladKeywords size = %d; want exactly 5", len(IronCladKeywords))
	}
}

// 带整数型 Retry-After 头的 429 -> cooldown + 解析出 retry_after_ms。
func TestClassify_R013_RateLimitedWithRetryAfter(t *testing.T) {
	h := http.Header{"Retry-After": []string{"30"}}
	c, _ := Classify(429, h, nil, "openai")
	if c.RuleID != "R-013" || c.RetryAction != RetryActionCooldown {
		t.Fatalf("got rule=%s action=%s; want R-013 cooldown", c.RuleID, c.RetryAction)
	}
	if c.RetryAfterMs != 30000 {
		t.Fatalf("retry-after-ms = %d; want 30000", c.RetryAfterMs)
	}
}

// Retry-After 为 HTTP-date 格式(RFC 7231)。
func TestRetryAfter_HttpDateFormat(t *testing.T) {
	future := time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)
	h := http.Header{"Retry-After": []string{future}}
	c, _ := Classify(429, h, nil, "openai")
	if c.RetryAfterMs < 60_000 || c.RetryAfterMs > 130_000 {
		t.Fatalf("retry-after-ms (HTTP-date) = %d; want ~120000", c.RetryAfterMs)
	}
}

// Bedrock 429 ThrottlingException 优先用 R-018 而非通用 429。
func TestProviderSpecificity_BedrockThrottling(t *testing.T) {
	c, _ := Classify(429, nil, []byte("ThrottlingException"), "bedrock")
	if c.RuleID != "R-018" {
		t.Fatalf("got rule=%s; want R-018 (bedrock throttling)", c.RuleID)
	}
	if c.Class != ErrorClassRateLimited {
		t.Fatalf("class=%s; want upstream_rate_limited", c.Class)
	}
	// 其他 provider 即便同样是 429 也会落到通用 R-013。
	c2, _ := Classify(429, nil, []byte("ThrottlingException"), "openai")
	if c2.RuleID != "R-013" {
		t.Fatalf("openai 429 got rule=%s; want R-013", c2.RuleID)
	}
}

func TestClassify_OpenAIAndCodex429QuotaExhaustedBeforeRateLimit(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		body     string
		wantRule string
	}{
		{"openai_structured_quota", "openai", `{"error":{"type":"insufficient_quota"}}`, "R-026"},
		{"codex_alias_structured_quota", "openai_codex", `{"error":{"code":"insufficient_quota"}}`, "R-027"},
		{"openai_hard_limit_code", "openai", `{"error":{"code":"billing_hard_limit_reached"}}`, "R-028"},
		{"codex_hard_limit_code", "codex", `{"error":{"code":"billing_hard_limit_reached"}}`, "R-029"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Classify(429, nil, []byte(tt.body), tt.provider)
			if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			if got.RuleID != tt.wantRule || got.Class != ErrorClassCreditExhausted {
				t.Fatalf("rule/class=%s/%s want %s/%s", got.RuleID, got.Class, tt.wantRule, ErrorClassCreditExhausted)
			}
			if got.RetryAction != RetryActionPermanentDisable || got.Tier != TierIronClad || got.FsmTransition != FsmTransitionDisabled {
				t.Fatalf("action/tier/fsm=%s/%s/%s want permanent_disable/iron_clad/disabled", got.RetryAction, got.Tier, got.FsmTransition)
			}
			if sig := SignalFromClassification(429, got); sig != channelhealth.SignalAccountSuspended {
				t.Fatalf("signal=%s want account_suspended", sig)
			}
		})
	}
}

func TestClassify_429QuotaWordsRemainRateLimitedUnlessNarrowEvidence(t *testing.T) {
	got, err := Classify(429, nil, []byte(`{"error":{"message":"quota window rate limit reached"}}`), "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got.RuleID != "R-013" || got.Class != ErrorClassRateLimited {
		t.Fatalf("rule/class=%s/%s want R-013/rate_limited;泛 quota 文案不得整号禁用", got.RuleID, got.Class)
	}
}

func TestR018_BedrockThrottling429(t *testing.T) {
	c, err := Classify(429, nil, []byte(`{"type":"ThrottlingException"}`), "bedrock")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID != "R-018" || c.RuleVersion != 2 {
		t.Fatalf("got rule=%s version=%d; want R-018 version 2", c.RuleID, c.RuleVersion)
	}
	if c.Class != ErrorClassRateLimited || c.RetryAction != RetryActionCooldown || c.Tier != TierAmbiguous {
		t.Fatalf("got class=%s action=%s tier=%s; want rate_limited cooldown ambiguous", c.Class, c.RetryAction, c.Tier)
	}
}

func TestR020_Bedrock503ServiceUnavailable(t *testing.T) {
	c, err := Classify(503, nil, []byte(`{"type":"ServiceUnavailableException"}`), "bedrock")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID != "R-020" {
		t.Fatalf("got rule=%s; want R-020", c.RuleID)
	}
	if c.Class != ErrorClassOverloaded {
		t.Fatalf("class=%s; want upstream_overloaded", c.Class)
	}
	if c.Class == ErrorClassRateLimited {
		t.Fatal("bedrock ServiceUnavailableException must not be rate_limited")
	}
	if c.RetryAction != RetryActionCooldown || c.Tier != TierAmbiguous || c.FsmTransition != FsmTransitionCooling {
		t.Fatalf("got action=%s tier=%s fsm=%s; want cooldown ambiguous cooling", c.RetryAction, c.Tier, c.FsmTransition)
	}
}

// Anthropic 403 且响应体含 "validation" -> 永久禁用(R-011)。
func TestAnthropic403Validation_R011(t *testing.T) {
	c, _ := Classify(403, nil, []byte(`{"error":"validation failed"}`), "anthropic")
	if c.RuleID != "R-011" {
		t.Fatalf("got rule=%s; want R-011", c.RuleID)
	}
	if c.Tier != TierIronClad || c.FsmTransition != FsmTransitionDisabled {
		t.Fatalf("anthropic 403 validation must reach disabled; got tier=%s fsm=%s", c.Tier, c.FsmTransition)
	}
}

func TestAntigravityProjectPermissionDeniedIsRecoverable(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"Permission denied on resource project","status":"PERMISSION_DENIED"}}`,
		`{"error":{"status":"PERMISSION_DENIED"}}`,
	} {
		classification, err := Classify(http.StatusForbidden, nil, []byte(body), "antigravity")
		if err != nil {
			t.Fatal(err)
		}
		if classification.Class != ErrorClassProjectContextRejected || classification.Tier != TierAmbiguous || classification.FsmTransition == FsmTransitionDisabled {
			t.Fatalf("classification=%+v，项目权限拒绝不得永久禁用账号", classification)
		}
		decision, _, err := ClassifyAttemptHTTPError(http.StatusForbidden, nil, []byte(body), "antigravity")
		if err != nil {
			t.Fatal(err)
		}
		if !decision.RetryableBeforeDelivery || !decision.SwitchAccount || decision.RefreshIntent != RefreshOAuthHotPath || decision.ClientStatus != http.StatusServiceUnavailable {
			t.Fatalf("decision=%+v，期望刷新并有界换号", decision)
		}
	}
}

func TestAntigravityUnstructuredForbiddenRemainsTerminal(t *testing.T) {
	classification, err := Classify(http.StatusForbidden, nil, []byte(`{"error":"account blocked"}`), "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	if classification.RuleID != "R-012" || classification.Class != ErrorClassPlatformPolicy {
		t.Fatalf("classification=%+v，非项目权限错误不得伪装成可恢复项目失配", classification)
	}
}

// Provider 别名:"anthropic_messages" 归一化为 "anthropic"。
func TestProviderAlias_AnthropicMessages(t *testing.T) {
	c, _ := Classify(403, nil, []byte("validation failed"), "anthropic_messages")
	if c.RuleID != "R-011" {
		t.Fatalf("alias did not match anthropic rule; got %s", c.RuleID)
	}
}

// 无关键词的通用 401 -> R-009 永久禁用。
func TestClassify_R009_Generic401(t *testing.T) {
	c, _ := Classify(401, nil, []byte(`{"detail":"unauthorized"}`), "openai")
	if c.RuleID != "R-009" {
		t.Fatalf("got rule=%s; want R-009", c.RuleID)
	}
	if c.Tier != TierIronClad {
		t.Fatalf("R-009 must be iron_clad; got %s", c.Tier)
	}
}

// Anthropic 402 且含 credit 关键词 -> R-007。
func TestClassify_R007_CreditExhausted(t *testing.T) {
	c, _ := Classify(402, nil, []byte("Insufficient credit balance"), "anthropic")
	if c.RuleID != "R-007" {
		t.Fatalf("got rule=%s; want R-007", c.RuleID)
	}
	if c.Class != ErrorClassCreditExhausted {
		t.Fatalf("class=%s; want credit_exhausted", c.Class)
	}
}

func TestR005_OpenAIDeactivatedWorkspaceAnyStatus(t *testing.T) {
	c, err := Classify(400, nil, []byte(`{"code":"deactivated_workspace"}`), "openai")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID != "R-005" || c.RuleVersion != 2 {
		t.Fatalf("got rule=%s version=%d; want R-005 version 2", c.RuleID, c.RuleVersion)
	}
	if c.Class != ErrorClassWorkspaceDeactivated || c.Tier != TierIronClad {
		t.Fatalf("got class=%s tier=%s; want workspace_deactivated iron_clad", c.Class, c.Tier)
	}
}

func TestR007_AnthropicCreditExhausted(t *testing.T) {
	c, err := Classify(402, nil, []byte(`{"error":"credit exhausted"}`), "anthropic")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID != "R-007" || c.RuleVersion != 2 {
		t.Fatalf("got rule=%s version=%d; want R-007 version 2", c.RuleID, c.RuleVersion)
	}
	if c.Class != ErrorClassCreditExhausted || c.FsmTransition != FsmTransitionDisabled {
		t.Fatalf("got class=%s fsm=%s; want credit_exhausted disabled", c.Class, c.FsmTransition)
	}
}

func TestR007_NotOpenAI(t *testing.T) {
	c, err := Classify(402, nil, []byte(`{"error":"credit exhausted"}`), "openai")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID == "R-007" {
		t.Fatal("openai 402+credit must not match R-007")
	}
	if c.RuleID != "R-016" || c.Class != ErrorClassUnknown {
		t.Fatalf("got rule=%s class=%s; want R-016 unknown", c.RuleID, c.Class)
	}
}

func TestR008_AnthropicCreditBalance400(t *testing.T) {
	c, err := Classify(400, nil, []byte("credit balance too low"), "anthropic")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID != "R-008" || c.RuleVersion != 2 {
		t.Fatalf("got rule=%s version=%d; want R-008 version 2", c.RuleID, c.RuleVersion)
	}
	if c.Class != ErrorClassCreditExhausted {
		t.Fatalf("class=%s; want credit_exhausted", c.Class)
	}
}

// Iron-clad 的 token_invalidated 别名 -> R-006 -> token_revoked。
func TestClassify_R006_TokenInvalidatedAlias(t *testing.T) {
	c, _ := Classify(401, nil, []byte("token_invalidated"), "openai")
	if c.RuleID != "R-006" || c.Class != ErrorClassTokenRevoked {
		t.Fatalf("got rule=%s class=%s; want R-006 token_revoked", c.RuleID, c.Class)
	}
}

// 合成的网络超时:status 0 + body 含 "timeout" -> R-019。
func TestClassify_R019_SynthesizedTimeout(t *testing.T) {
	c, _ := Classify(0, nil, []byte("upstream connection timeout"), "openai")
	if c.RuleID != "R-019" {
		t.Fatalf("got rule=%s; want R-019", c.RuleID)
	}
	if c.Class != ErrorClassNetworkTimeout {
		t.Fatalf("class=%s; want network_timeout", c.Class)
	}
}

// 负数 status 返回错误。
func TestClassify_NegativeStatus(t *testing.T) {
	if _, err := Classify(-1, nil, nil, "openai"); err == nil {
		t.Fatal("expected error for negative status")
	}
}

// 响应体关键词匹配大小写不敏感。
func TestClassify_BodyMatchCaseInsensitive(t *testing.T) {
	c, _ := Classify(401, nil, []byte("INVALID_GRANT"), "openai")
	if c.RuleID != "R-001" {
		t.Fatalf("uppercase keyword did not match; got %s", c.RuleID)
	}
}

// 所有 ERROR_RULES 的 RuleID 都唯一。
func TestRuleTable_UniqueIDs(t *testing.T) {
	seen := map[string]struct{}{}
	for _, r := range errorRules {
		if _, dup := seen[r.RuleID]; dup {
			t.Fatalf("duplicate RuleID: %s", r.RuleID)
		}
		seen[r.RuleID] = struct{}{}
	}
}

// 所有 ERROR_RULES 都使用非空的 ErrorClass。
func TestRuleTable_AllRulesHaveClass(t *testing.T) {
	for _, r := range errorRules {
		if r.Class == "" {
			t.Errorf("rule %s has empty Class", r.RuleID)
		}
	}
}

// seconds=0 的自定义 RetryAfter 得出 0(无重试提示)。
func TestRetryAfter_ZeroSeconds(t *testing.T) {
	h := http.Header{"Retry-After": []string{"0"}}
	c, _ := Classify(429, h, nil, "openai")
	if c.RetryAfterMs != 0 {
		t.Fatalf("retry-after-ms = %d; want 0", c.RetryAfterMs)
	}
}

// 空响应体 + 429 仍被分类为 rate-limited(不要求响应体含关键词)。
func TestClassify_429_EmptyBody(t *testing.T) {
	c, _ := Classify(429, nil, nil, "openai")
	if c.Class != ErrorClassRateLimited {
		t.Fatalf("class=%s; want upstream_rate_limited", c.Class)
	}
}

// Unicode/locale:关键词匹配容忍首尾空白 + 混合情形。
func TestClassify_BodyWithWhitespace(t *testing.T) {
	c, _ := Classify(401, nil, []byte("  invalid_grant  \n"), "openai")
	if c.RuleID != "R-001" {
		t.Fatalf("got rule=%s; want R-001", c.RuleID)
	}
}

// 健全性:provider="*" 仍是一条正常条目,不是分隔符。
func TestNormalizeProvider_Wildcard(t *testing.T) {
	if normalizeProvider("*") != "*" || normalizeProvider("") != "*" {
		t.Fatal("wildcard normalization broken")
	}
}

// 置信度赋值:iron_clad=high,ambiguous=medium,none=low。
func TestConfidenceForTier(t *testing.T) {
	if confidenceForTier(TierIronClad) != ConfidenceHigh {
		t.Errorf("iron_clad confidence != high")
	}
	if confidenceForTier(TierAmbiguous) != ConfidenceMedium {
		t.Errorf("ambiguous confidence != medium")
	}
	if confidenceForTier(TierNone) != ConfidenceLow {
		t.Errorf("none confidence != low")
	}
}

// nil body 时响应体匹配不会 panic。
func TestClassify_NilBody(t *testing.T) {
	c, err := Classify(429, nil, nil, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if c.Class != ErrorClassRateLimited {
		t.Fatalf("nil body class=%q want %q", c.Class, ErrorClassRateLimited)
	}
}

// ---------------------------------------------------------------------------
// D8 新增测试(5+)
// ---------------------------------------------------------------------------

// TestR021_Anthropic402Billing:anthropic 402 无响应体 -> R-021 -> CreditExhausted iron_clad。
// R-021 是优先级 25 的兜底 billing 规则,排在按关键词命中的 R-007(优先级 20)之后。
func TestR021_Anthropic402Billing(t *testing.T) {
	c, err := Classify(402, nil, []byte(`{"type":"billing_error"}`), "anthropic")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID != "R-021" {
		t.Fatalf("got rule=%s; want R-021 (anthropic 402 catch-all billing)", c.RuleID)
	}
	if c.Class != ErrorClassCreditExhausted {
		t.Fatalf("class=%s; want credit_exhausted", c.Class)
	}
	if c.Tier != TierIronClad {
		t.Fatalf("tier=%s; want iron_clad", c.Tier)
	}
	if c.FsmTransition != FsmTransitionDisabled {
		t.Fatalf("fsm=%s; want disabled (iron_clad permanent_disable)", c.FsmTransition)
	}
	if c.RetryAction != RetryActionPermanentDisable {
		t.Fatalf("action=%s; want permanent_disable", c.RetryAction)
	}
}

// TestR022_Anthropic504Timeout：anthropic 504 -> R-022 -> UpstreamTimeout 模糊冷却。
func TestR022_Anthropic504Timeout(t *testing.T) {
	c, err := Classify(504, nil, []byte(`{"type":"timeout_error"}`), "anthropic")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID != "R-022" {
		t.Fatalf("got rule=%s; want R-022 (anthropic 504 upstream timeout)", c.RuleID)
	}
	if c.Class != ErrorClassUpstreamTimeout {
		t.Fatalf("class=%s; want upstream_timeout", c.Class)
	}
	if c.Tier != TierAmbiguous {
		t.Fatalf("tier=%s; want ambiguous", c.Tier)
	}
	if c.RetryAction != RetryActionCooldown {
		t.Fatalf("action=%s; want cooldown", c.RetryAction)
	}
	if c.FsmTransition != FsmTransitionCooling {
		t.Fatalf("fsm=%s; want cooling_down", c.FsmTransition)
	}
	// 硬底线检查:ambiguous 绝不能到达 disabled。
	if c.FsmTransition == FsmTransitionDisabled {
		t.Fatal("DR-009 6.6 violation: ambiguous R-022 reached disabled")
	}
}

// TestR023_Anthropic413RequestTooLarge:anthropic 413 -> R-023 -> RequestTooLarge
// none 层 pass_through(客户端错误,不触发 FSM 变更)。
func TestR023_Anthropic413RequestTooLarge(t *testing.T) {
	c, err := Classify(413, nil, []byte(`{"type":"request_too_large"}`), "anthropic")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID != "R-023" {
		t.Fatalf("got rule=%s; want R-023 (anthropic 413 request too large)", c.RuleID)
	}
	if c.Class != ErrorClassRequestTooLarge {
		t.Fatalf("class=%s; want request_too_large", c.Class)
	}
	if c.Tier != TierNone {
		t.Fatalf("tier=%s; want none (client error, not provider fault)", c.Tier)
	}
	if c.RetryAction != RetryActionPassThrough {
		t.Fatalf("action=%s; want pass_through (caller must reduce payload)", c.RetryAction)
	}
	if c.FsmTransition != FsmTransitionNoChange {
		t.Fatalf("fsm=%s; want no_transition (413 is client error, no FSM impact)", c.FsmTransition)
	}
}

func TestSignalFromClassification_ClientMalformed4xxDoesNotBecomeChannelError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		class  Classification
		want   channelhealth.SignalClass
	}{
		{
			name:   "typed request too large",
			status: http.StatusRequestEntityTooLarge,
			class:  Classification{Class: ErrorClassRequestTooLarge, Tier: TierNone, RetryAction: RetryActionPassThrough, FsmTransition: FsmTransitionNoChange},
			want:   channelhealth.SignalClientMalformed,
		},
		{
			name:   "unknown bad request passthrough",
			status: http.StatusBadRequest,
			class:  Classification{Class: ErrorClassUnknown, Tier: TierNone, RetryAction: RetryActionPassThrough, FsmTransition: FsmTransitionNoChange},
			want:   channelhealth.SignalClientMalformed,
		},
		{
			name:   "unprocessable request passthrough",
			status: http.StatusUnprocessableEntity,
			class:  Classification{Class: ErrorClassUnknown, Tier: TierNone, RetryAction: RetryActionPassThrough, FsmTransition: FsmTransitionNoChange},
			want:   channelhealth.SignalClientMalformed,
		},
		{
			name:   "account auth stays auth lane",
			status: http.StatusUnauthorized,
			class:  Classification{Class: ErrorClassTokenRevoked, Tier: TierIronClad, RetryAction: RetryActionPermanentDisable, FsmTransition: FsmTransitionDisabled},
			want:   channelhealth.SignalAuthChallenge,
		},
		{
			name:   "rate limit stays rate limit",
			status: http.StatusTooManyRequests,
			class:  Classification{Class: ErrorClassRateLimited, Tier: TierAmbiguous, RetryAction: RetryActionCooldown, FsmTransition: FsmTransitionCooling},
			want:   channelhealth.SignalRateLimit,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SignalFromClassification(tt.status, tt.class); got != tt.want {
				t.Fatalf("SignalFromClassification=%s want %s", got, tt.want)
			}
		})
	}
}

func TestAntigravityCreditsExhaustedUsesQuotaResetDelay(t *testing.T) {
	body := []byte(`{"error":{"code":429,"message":"insufficient_g1_credits_balance","details":[{"metadata":{"quotaResetDelay":"2h15m"}}]}}`)
	classification, err := Classify(http.StatusTooManyRequests, nil, body, "antigravity")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if classification.Class != ErrorClassCreditsRefillable || classification.RuleID != "R-032" ||
		classification.RetryAction != RetryActionCooldown || classification.FsmTransition != FsmTransitionCooling {
		t.Fatalf("classification=%+v，期望可恢复额度冷却", classification)
	}
	if classification.RetryAfterMs != int64((2*time.Hour+15*time.Minute)/time.Millisecond) {
		t.Fatalf("RetryAfterMs=%d，期望 2h15m", classification.RetryAfterMs)
	}
	if signal := SignalFromClassification(http.StatusTooManyRequests, classification); signal != channelhealth.SignalCreditsExhausted {
		t.Fatalf("signal=%s，期望 credits_exhausted", signal)
	}
}

func TestAntigravityOrdinaryRateLimitIsNotCreditsExhausted(t *testing.T) {
	classification, err := Classify(http.StatusTooManyRequests, nil, []byte(`{"error":{"message":"rate limit"}}`), "antigravity")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if classification.Class != ErrorClassRateLimited || classification.RuleID != "R-013" {
		t.Fatalf("classification=%+v，普通 429 不得误判为额度窗口耗尽", classification)
	}
}

// TestR007_StillFiresWithCreditKeyword:anthropic 402 + credit 关键词 -> R-007(优先级 20)
// 胜过 R-021(优先级 25)。优先级数字越小 = 优先级越高。
func TestR007_StillFiresWithCreditKeyword(t *testing.T) {
	c, err := Classify(402, nil, []byte(`{"error":"credit exhausted, billing failed"}`), "anthropic")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID != "R-007" {
		t.Fatalf("got rule=%s; want R-007 (priority 20 wins over R-021 priority 25 when keyword present)", c.RuleID)
	}
	if c.Class != ErrorClassCreditExhausted {
		t.Fatalf("class=%s; want credit_exhausted", c.Class)
	}
	if c.RuleVersion != 2 {
		t.Fatalf("version=%d; want 2 (R-007 version 2)", c.RuleVersion)
	}
}

// TestErrorClass_AllDistinct:14 个 ErrorClass 常量(D8 新增 2 个)全部唯一。
// 取代原来的 TestErrorClass_TwelveDistinct。
func TestErrorClass_AllDistinct(t *testing.T) {
	classes := []ErrorClass{
		ErrorClassOAuthInvalidGrant,
		ErrorClassTokenRevoked,
		ErrorClassKYCRequired,
		ErrorClassOrgDisabled,
		ErrorClassWorkspaceDeactivated,
		ErrorClassCreditExhausted,
		ErrorClassPlatformPolicy,
		ErrorClassRateLimited,
		ErrorClassOverloaded,
		ErrorClassServerError,
		ErrorClassNetworkTimeout,
		ErrorClassUnknown,
		// D8 新增:
		ErrorClassUpstreamTimeout,
		ErrorClassRequestTooLarge,
	}
	if len(classes) != 14 {
		t.Fatalf("class list size = %d; want 14 (12 original + 2 D8 additions)", len(classes))
	}
	seen := map[ErrorClass]struct{}{}
	for _, c := range classes {
		if _, dup := seen[c]; dup {
			t.Fatalf("duplicate ErrorClass: %s", c)
		}
		seen[c] = struct{}{}
	}
}
