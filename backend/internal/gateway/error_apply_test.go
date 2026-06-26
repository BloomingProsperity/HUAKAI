package gateway

import (
	"encoding/json"
	"net/http"
	"testing"
)

// 全部 6 个 auth 类 ErrorClass → UpstreamAuthFailure。
func TestStreamEndClass_AuthClassesCollapseToAuthFailure(t *testing.T) {
	authClasses := []ErrorClass{
		ErrorClassOAuthInvalidGrant,
		ErrorClassTokenRevoked,
		ErrorClassKYCRequired,
		ErrorClassOrgDisabled,
		ErrorClassWorkspaceDeactivated,
		ErrorClassCreditExhausted,
	}
	for _, ec := range authClasses {
		got := streamEndClassForErrorClass(ec)
		if got != UpstreamAuthFailure {
			t.Errorf("ErrorClass=%s mapped to %s; want UpstreamAuthFailure", ec, got)
		}
	}
}

// 其它 class → end-class 的映射。
func TestStreamEndClass_OtherMappings(t *testing.T) {
	cases := []struct {
		ec   ErrorClass
		want StreamEndClass
	}{
		{ErrorClassRateLimited, UpstreamRateLimit},
		{ErrorClassServerError, UpstreamError5xx},
		{ErrorClassOverloaded, UpstreamError5xx},
		{ErrorClassPlatformPolicy, UpstreamError4xx},
		{ErrorClassNetworkTimeout, InterEventTimeout},
		{ErrorClassUnknown, UnknownTermination},
	}
	for _, c := range cases {
		got := streamEndClassForErrorClass(c.ec)
		if got != c.want {
			t.Errorf("ErrorClass=%s mapped to %s; want %s", c.ec, got, c.want)
		}
	}
}

// JSON payload 包含全部 8 个字段且取值正确。
func TestApplyClassification_JSONPayload(t *testing.T) {
	d := &UsageRecordDraft{}
	c := Classification{
		Class:         ErrorClassOAuthInvalidGrant,
		Confidence:    ConfidenceHigh,
		RuleID:        "R-001",
		RuleVersion:   1,
		Tier:          TierIronClad,
		RetryAction:   RetryActionPermanentDisable,
		FsmTransition: FsmTransitionDisabled,
		RetryAfterMs:  0,
	}
	ApplyClassificationToDraft(d, c)

	var got RoutingReasonPayload
	if err := json.Unmarshal(d.RoutingReason, &got); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if got.RuleID != "R-001" || got.RuleVersion != 1 ||
		got.ErrorClass != "oauth_invalid_grant" || got.Tier != "iron_clad" ||
		got.RetryAction != "permanent_disable" || got.FsmTransition != "disabled" ||
		got.Confidence != "high" {
		t.Fatalf("payload mismatch: %+v", got)
	}
}

// EndClass 为零值时 Apply 会设置它。
func TestApply_SetsEndClassWhenZero(t *testing.T) {
	d := &UsageRecordDraft{} // EndClass 为零值 ""
	c := Classification{Class: ErrorClassRateLimited, RuleID: "R-013"}
	ApplyClassificationToDraft(d, c)
	if d.EndClass != UpstreamRateLimit {
		t.Fatalf("EndClass=%s; want UpstreamRateLimit", d.EndClass)
	}
}

// 当前为 UnknownTermination 时 Apply 会设置 EndClass。
func TestApply_OverwritesUnknown(t *testing.T) {
	d := &UsageRecordDraft{EndClass: UnknownTermination}
	c := Classification{Class: ErrorClassOAuthInvalidGrant}
	ApplyClassificationToDraft(d, c)
	if d.EndClass != UpstreamAuthFailure {
		t.Fatalf("EndClass=%s; want UpstreamAuthFailure", d.EndClass)
	}
}

// Apply 不会覆盖已设置的 EndClass(保留先前的判定)。
func TestApply_PreservesNonUnknownEndClass(t *testing.T) {
	d := &UsageRecordDraft{EndClass: ClientDisconnect}
	c := Classification{Class: ErrorClassRateLimited}
	ApplyClassificationToDraft(d, c)
	if d.EndClass != ClientDisconnect {
		t.Fatalf("EndClass=%s; should preserve ClientDisconnect", d.EndClass)
	}
	// RoutingReason 仍然会被写入
	if len(d.RoutingReason) == 0 {
		t.Fatal("RoutingReason should still be written even when EndClass preserved")
	}
}

// Apply 不会覆盖 UpstreamError4xx(forwarder 先前的判定)。
func TestApply_PreservesError4xx(t *testing.T) {
	d := &UsageRecordDraft{EndClass: UpstreamError4xx}
	c := Classification{Class: ErrorClassOAuthInvalidGrant}
	ApplyClassificationToDraft(d, c)
	if d.EndClass != UpstreamError4xx {
		t.Fatalf("EndClass=%s; should preserve UpstreamError4xx", d.EndClass)
	}
}

// draft 为 nil:静默空操作(不 panic)。
func TestApply_NilDraftNoOp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ApplyClassificationToDraft(nil) panicked: %v", r)
		}
	}()
	ApplyClassificationToDraft(nil, Classification{})
}

// ClassifyAndApply 在 draft 为 nil 时返回 error。
func TestClassifyAndApply_NilDraftReturnsError(t *testing.T) {
	_, err := ClassifyAndApply(nil, 401, nil, []byte("invalid_grant"), "openai")
	if err == nil {
		t.Fatal("expected error for nil draft")
	}
}

// ClassifyAndApply 正常路径:classify + apply,draft 被更新,返回 classification。
func TestClassifyAndApply_HappyPath(t *testing.T) {
	d := &UsageRecordDraft{}
	c, err := ClassifyAndApply(d, 401, nil, []byte("invalid_grant"), "openai")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if c.RuleID != "R-001" {
		t.Fatalf("got rule=%s; want R-001", c.RuleID)
	}
	if d.EndClass != UpstreamAuthFailure {
		t.Fatalf("draft EndClass=%s; want UpstreamAuthFailure", d.EndClass)
	}
	if len(d.RoutingReason) == 0 {
		t.Fatal("draft RoutingReason not set")
	}
}

// ClassifyAndApply 的 Classify 出错:错误被返回,draft 不被改动。
func TestClassifyAndApply_ClassifyErrorPropagated(t *testing.T) {
	d := &UsageRecordDraft{}
	_, err := ClassifyAndApply(d, -1, nil, nil, "openai")
	if err == nil {
		t.Fatal("expected Classify error for negative status")
	}
	if len(d.RoutingReason) != 0 || d.EndClass != "" {
		t.Fatal("draft should not be mutated when Classify errored")
	}
}

// headers 与 body 均为 nil:不 panic,通过通配规则分类。
func TestClassifyAndApply_NilHeadersAndBody(t *testing.T) {
	d := &UsageRecordDraft{}
	c, err := ClassifyAndApply(d, 429, nil, nil, "openai")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if c.Class != ErrorClassRateLimited {
		t.Fatalf("class=%s; want upstream_rate_limited", c.Class)
	}
	if d.EndClass != UpstreamRateLimit {
		t.Fatalf("EndClass=%s; want UpstreamRateLimit", d.EndClass)
	}
}

// headers 与 body 均为空:不 panic。
func TestApply_EmptyHeadersAndBody(t *testing.T) {
	d := &UsageRecordDraft{}
	c, _ := Classify(429, http.Header{}, []byte{}, "openai")
	ApplyClassificationToDraft(d, c)
	if d.EndClass != UpstreamRateLimit {
		t.Fatalf("EndClass=%s; want UpstreamRateLimit", d.EndClass)
	}
}

// Retry-After 头会传递进 JSON payload。
func TestApply_RetryAfterPropagates(t *testing.T) {
	d := &UsageRecordDraft{}
	h := http.Header{"Retry-After": []string{"45"}}
	_, err := ClassifyAndApply(d, 429, h, nil, "openai")
	if err != nil {
		t.Fatal(err)
	}
	var p RoutingReasonPayload
	if err := json.Unmarshal(d.RoutingReason, &p); err != nil {
		t.Fatal(err)
	}
	if p.RetryAfterMs != 45_000 {
		t.Fatalf("retry_after_ms=%d; want 45000", p.RetryAfterMs)
	}
}

// 500 服务端错误 → UpstreamError5xx + warn_only / degraded。
func TestApply_5xxMapping(t *testing.T) {
	d := &UsageRecordDraft{}
	_, _ = ClassifyAndApply(d, 502, nil, []byte("bad gateway"), "openai")
	if d.EndClass != UpstreamError5xx {
		t.Fatalf("EndClass=%s; want UpstreamError5xx", d.EndClass)
	}
}

// Anthropic 403 携带 validation → UpstreamError4xx(platform_policy 映射)。
func TestApply_PlatformPolicyMapping(t *testing.T) {
	d := &UsageRecordDraft{}
	_, _ = ClassifyAndApply(d, 403, nil, []byte("validation failed"), "anthropic")
	if d.EndClass != UpstreamError4xx {
		t.Fatalf("EndClass=%s; want UpstreamError4xx", d.EndClass)
	}
}
