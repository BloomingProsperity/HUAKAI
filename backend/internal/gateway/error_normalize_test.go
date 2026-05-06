package gateway

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// AT-RATE-021 - invalid_grant in 401 body -> R-001 -> iron_clad -> disabled.
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

// AT-RATE-022 - generic 5xx -> R-015 -> ambiguous -> degraded, NOT disabled.
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

// AT-RATE-023 - unknown error -> R-016 catch-all -> pass_through.
func TestClassify_R016_Wildcard(t *testing.T) {
	c, err := Classify(418, nil, []byte("teapot"), "fictional_provider")
	if err != nil {
		t.Fatalf("classify err: %v", err)
	}
	if c.RuleID != "R-016" || c.RetryAction != RetryActionPassThrough {
		t.Fatalf("got rule=%s action=%s; want R-016 pass_through", c.RuleID, c.RetryAction)
	}
}

// DR-009 6.6 hard-floor invariant: no ambiguous rule can yield disabled.
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

// IsIronCladKeyword must be exactly the 5 task-spec keywords.
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

// 429 with Retry-After integer header -> cooldown + retry_after_ms parsed.
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

// Retry-After HTTP-date format (RFC 7231) - Codex side strength preserved.
func TestRetryAfter_HttpDateFormat(t *testing.T) {
	future := time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)
	h := http.Header{"Retry-After": []string{future}}
	c, _ := Classify(429, h, nil, "openai")
	if c.RetryAfterMs < 60_000 || c.RetryAfterMs > 130_000 {
		t.Fatalf("retry-after-ms (HTTP-date) = %d; want ~120000", c.RetryAfterMs)
	}
}

// Bedrock 429 ThrottlingException uses R-018 ahead of generic 429.
func TestProviderSpecificity_BedrockThrottling(t *testing.T) {
	c, _ := Classify(429, nil, []byte("ThrottlingException"), "bedrock")
	if c.RuleID != "R-018" {
		t.Fatalf("got rule=%s; want R-018 (bedrock throttling)", c.RuleID)
	}
	if c.Class != ErrorClassRateLimited {
		t.Fatalf("class=%s; want upstream_rate_limited", c.Class)
	}
	// Other provider with the same 429 falls through to generic R-013.
	c2, _ := Classify(429, nil, []byte("ThrottlingException"), "openai")
	if c2.RuleID != "R-013" {
		t.Fatalf("openai 429 got rule=%s; want R-013", c2.RuleID)
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

// Anthropic 403 with "validation" body -> permanent disable (R-011).
func TestAnthropic403Validation_R011(t *testing.T) {
	c, _ := Classify(403, nil, []byte(`{"error":"validation failed"}`), "anthropic")
	if c.RuleID != "R-011" {
		t.Fatalf("got rule=%s; want R-011", c.RuleID)
	}
	if c.Tier != TierIronClad || c.FsmTransition != FsmTransitionDisabled {
		t.Fatalf("anthropic 403 validation must reach disabled; got tier=%s fsm=%s", c.Tier, c.FsmTransition)
	}
}

// Provider alias: "anthropic_messages" normalizes to "anthropic".
func TestProviderAlias_AnthropicMessages(t *testing.T) {
	c, _ := Classify(403, nil, []byte("validation failed"), "anthropic_messages")
	if c.RuleID != "R-011" {
		t.Fatalf("alias did not match anthropic rule; got %s", c.RuleID)
	}
}

// Generic 401 without keyword -> R-009 permanent disable.
func TestClassify_R009_Generic401(t *testing.T) {
	c, _ := Classify(401, nil, []byte(`{"detail":"unauthorized"}`), "openai")
	if c.RuleID != "R-009" {
		t.Fatalf("got rule=%s; want R-009", c.RuleID)
	}
	if c.Tier != TierIronClad {
		t.Fatalf("R-009 must be iron_clad; got %s", c.Tier)
	}
}

// Anthropic 402 with credit keyword -> R-007.
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

// Iron-clad token_invalidated alias -> R-006 -> token_revoked.
func TestClassify_R006_TokenInvalidatedAlias(t *testing.T) {
	c, _ := Classify(401, nil, []byte("token_invalidated"), "openai")
	if c.RuleID != "R-006" || c.Class != ErrorClassTokenRevoked {
		t.Fatalf("got rule=%s class=%s; want R-006 token_revoked", c.RuleID, c.Class)
	}
}

// Synthesized network timeout: status 0 + body "timeout" -> R-019.
func TestClassify_R019_SynthesizedTimeout(t *testing.T) {
	c, _ := Classify(0, nil, []byte("upstream connection timeout"), "openai")
	if c.RuleID != "R-019" {
		t.Fatalf("got rule=%s; want R-019", c.RuleID)
	}
	if c.Class != ErrorClassNetworkTimeout {
		t.Fatalf("class=%s; want network_timeout", c.Class)
	}
}

// Negative status returns error.
func TestClassify_NegativeStatus(t *testing.T) {
	if _, err := Classify(-1, nil, nil, "openai"); err == nil {
		t.Fatal("expected error for negative status")
	}
}

// Body keyword matching is case-insensitive.
func TestClassify_BodyMatchCaseInsensitive(t *testing.T) {
	c, _ := Classify(401, nil, []byte("INVALID_GRANT"), "openai")
	if c.RuleID != "R-001" {
		t.Fatalf("uppercase keyword did not match; got %s", c.RuleID)
	}
}

// All ERROR_RULES have unique RuleID.
func TestRuleTable_UniqueIDs(t *testing.T) {
	seen := map[string]struct{}{}
	for _, r := range errorRules {
		if _, dup := seen[r.RuleID]; dup {
			t.Fatalf("duplicate RuleID: %s", r.RuleID)
		}
		seen[r.RuleID] = struct{}{}
	}
}

// All ERROR_RULES use a non-empty ErrorClass.
func TestRuleTable_AllRulesHaveClass(t *testing.T) {
	for _, r := range errorRules {
		if r.Class == "" {
			t.Errorf("rule %s has empty Class", r.RuleID)
		}
	}
}

// Custom RetryAfter with seconds=0 yields 0 (no retry hint).
func TestRetryAfter_ZeroSeconds(t *testing.T) {
	h := http.Header{"Retry-After": []string{"0"}}
	c, _ := Classify(429, h, nil, "openai")
	if c.RetryAfterMs != 0 {
		t.Fatalf("retry-after-ms = %d; want 0", c.RetryAfterMs)
	}
}

// Empty body + 429 still classifies as rate-limited (no body keyword required).
func TestClassify_429_EmptyBody(t *testing.T) {
	c, _ := Classify(429, nil, nil, "openai")
	if c.Class != ErrorClassRateLimited {
		t.Fatalf("class=%s; want upstream_rate_limited", c.Class)
	}
}

// Unicode/locale: keyword matching tolerates leading/trailing whitespace + mixed.
func TestClassify_BodyWithWhitespace(t *testing.T) {
	c, _ := Classify(401, nil, []byte("  invalid_grant  \n"), "openai")
	if c.RuleID != "R-001" {
		t.Fatalf("got rule=%s; want R-001", c.RuleID)
	}
}

// Sanity: provider="*" is still a normal entry, not a separator.
func TestNormalizeProvider_Wildcard(t *testing.T) {
	if normalizeProvider("*") != "*" || normalizeProvider("") != "*" {
		t.Fatal("wildcard normalization broken")
	}
}

// Confidence assignment: iron_clad=high, ambiguous=medium, none=low.
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

// 12 ErrorClass constants distinct + accounted for.
func TestErrorClass_TwelveDistinct(t *testing.T) {
	classes := []ErrorClass{
		ErrorClassOAuthInvalidGrant, ErrorClassTokenRevoked, ErrorClassKYCRequired,
		ErrorClassOrgDisabled, ErrorClassWorkspaceDeactivated, ErrorClassCreditExhausted,
		ErrorClassPlatformPolicy, ErrorClassRateLimited, ErrorClassOverloaded,
		ErrorClassServerError, ErrorClassNetworkTimeout, ErrorClassUnknown,
	}
	if len(classes) != 12 {
		t.Fatalf("class list size = %d; want 12", len(classes))
	}
	seen := map[ErrorClass]struct{}{}
	for _, c := range classes {
		if _, dup := seen[c]; dup {
			t.Fatalf("duplicate ErrorClass: %s", c)
		}
		seen[c] = struct{}{}
	}
}

// Body matching does not panic on nil body.
func TestClassify_NilBody(t *testing.T) {
	c, err := Classify(429, nil, nil, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(c.Class), "upstream_") {
		// Acceptable result is rate_limited.
	}
}
