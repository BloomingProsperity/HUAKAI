package rate

import (
	"context"
	"net/http"
	"testing"
	"time"
)

type staticRulesProvider struct {
	tempEnabled   bool
	rules         []TempUnschedulableRule
	customEnabled bool
	customCodes   []int32
	poolMode      bool
}

func (p *staticRulesProvider) GetAccountErrorPolicy(accountID int64) AccountErrorPolicy {
	_ = accountID
	var rules []TempUnschedulableRule
	if p.tempEnabled {
		rules = p.rules
	}
	var codes []int32
	if p.customEnabled {
		codes = p.customCodes
	}
	return AccountErrorPolicy{Rules: rules, CustomErrorCodes: codes, PoolMode: p.poolMode}
}

func fixedNow() time.Time {
	return time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
}

func durationProvider(minutes int) time.Duration {
	if minutes <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(minutes) * time.Minute
}

const testDefaultCooldown = 5 * time.Minute

// TC1:keyword 匹配 -> StateTempUnsched 且 CooldownUntil 非零。
func TestEvalAccountErrorRules_KeywordMatch(t *testing.T) {
	rules := []TempUnschedulableRule{
		{ErrorCode: 403, Keywords: []string{"unusual activity"}, DurationMinutes: 30},
	}
	body := []byte(`{"error":"Your account has been flagged for Unusual Activity detection"}`)
	dec := evalAccountErrorRules(403, body, rules, nil, durationProvider, testDefaultCooldown, fixedNow(), false)
	if dec.StateChange != StateTempUnsched {
		t.Fatalf("StateChange=%v want StateTempUnsched", dec.StateChange)
	}
	if dec.Reason != ReasonTempUnschedRule {
		t.Fatalf("Reason=%s want ReasonTempUnschedRule", dec.Reason)
	}
	if !dec.ShouldFailover {
		t.Fatal("ShouldFailover=false want true")
	}
	if dec.CooldownUntil.IsZero() {
		t.Fatal("CooldownUntil must be non-zero for keyword-rule match")
	}
	expectedUntil := fixedNow().Add(30 * time.Minute)
	if !dec.CooldownUntil.Equal(expectedUntil) {
		t.Fatalf("CooldownUntil=%s want %s", dec.CooldownUntil, expectedUntil)
	}
}

// TC2:body 中没有 keyword -> 空操作。
func TestEvalAccountErrorRules_KeywordMismatch(t *testing.T) {
	rules := []TempUnschedulableRule{
		{ErrorCode: 403, Keywords: []string{"unusual activity"}, DurationMinutes: 30},
	}
	body := []byte(`{"error":"Forbidden"}`)
	dec := evalAccountErrorRules(403, body, rules, nil, durationProvider, testDefaultCooldown, fixedNow(), false)
	if dec.StateChange != StateNoChange {
		t.Fatalf("StateChange=%v want StateNoChange (no keyword match)", dec.StateChange)
	}
}

// TC3:status 不匹配 -> 空操作。
func TestEvalAccountErrorRules_StatusMismatch(t *testing.T) {
	rules := []TempUnschedulableRule{
		{ErrorCode: 403, Keywords: []string{"unusual activity"}, DurationMinutes: 30},
	}
	body := []byte(`{"error":"unusual activity detected"}`)
	dec := evalAccountErrorRules(429, body, rules, nil, durationProvider, testDefaultCooldown, fixedNow(), false)
	if dec.StateChange != StateNoChange {
		t.Fatalf("StateChange=%v want StateNoChange (status mismatch)", dec.StateChange)
	}
}

// TC4:nil rules + nil codes -> 空操作。
func TestEvalAccountErrorRules_NilRules_NoOp(t *testing.T) {
	body := []byte(`{"error":"unusual activity"}`)
	dec := evalAccountErrorRules(403, body, nil, nil, durationProvider, testDefaultCooldown, fixedNow(), false)
	if dec.StateChange != StateNoChange {
		t.Fatalf("StateChange=%v want StateNoChange when rules=nil", dec.StateChange)
	}
}

// TC5:custom_error_codes 匹配 -> StateTempUnsched 且 CooldownUntil 非零(修复 3)。
func TestEvalAccountErrorRules_CustomErrorCode(t *testing.T) {
	customCodes := []int32{418, 503}
	body := []byte(`{}`)
	dec := evalAccountErrorRules(418, body, nil, customCodes, durationProvider, testDefaultCooldown, fixedNow(), false)
	if dec.StateChange != StateTempUnsched {
		t.Fatalf("StateChange=%v want StateTempUnsched", dec.StateChange)
	}
	if dec.Reason != ReasonCustomErrorCode {
		t.Fatalf("Reason=%s want ReasonCustomErrorCode", dec.Reason)
	}
	if dec.CooldownUntil.IsZero() {
		t.Fatal("CooldownUntil must be non-zero for custom_error_code match (FIX 3)")
	}
	expectedUntil := fixedNow().Add(testDefaultCooldown)
	if !dec.CooldownUntil.Equal(expectedUntil) {
		t.Fatalf("CooldownUntil=%s want %s (default cooldown)", dec.CooldownUntil, expectedUntil)
	}
}

// TC6:code 不在 custom 列表中 -> 空操作。
func TestEvalAccountErrorRules_CustomErrorCode_Miss(t *testing.T) {
	customCodes := []int32{418}
	dec := evalAccountErrorRules(503, nil, nil, customCodes, durationProvider, testDefaultCooldown, fixedNow(), false)
	if dec.StateChange != StateNoChange {
		t.Fatalf("StateChange=%v want StateNoChange (code not in custom list)", dec.StateChange)
	}
}

// TC7:空 keywords = 通配。
func TestEvalAccountErrorRules_EmptyKeywords_Wildcard(t *testing.T) {
	rules := []TempUnschedulableRule{
		{ErrorCode: 403, Keywords: []string{}, DurationMinutes: 10},
	}
	body := []byte(`{"error":"some random body"}`)
	dec := evalAccountErrorRules(403, body, rules, nil, durationProvider, testDefaultCooldown, fixedNow(), false)
	if dec.StateChange != StateTempUnsched {
		t.Fatalf("StateChange=%v want StateTempUnsched for empty-keyword wildcard", dec.StateChange)
	}
}

// TC8(修复 2):temp_unschedulable_enabled=false -> 即便存在行也返回空 rules。
func TestProvider_TempUnschedulableDisabled_EmptyRules(t *testing.T) {
	p := &staticRulesProvider{
		tempEnabled:   false,
		rules:         []TempUnschedulableRule{{ErrorCode: 403, Keywords: []string{"x"}, DurationMinutes: 10}},
		customEnabled: true,
		customCodes:   []int32{418},
	}
	policy := p.GetAccountErrorPolicy(1)
	if len(policy.Rules) != 0 {
		t.Fatalf("expected empty rules when temp_unschedulable_enabled=false, got %v", policy.Rules)
	}
	if len(policy.CustomErrorCodes) == 0 {
		t.Fatal("expected custom codes when custom_error_codes_enabled=true")
	}
}

// TC9(修复 2):custom_error_codes_enabled=false -> 即便存在 codes 也返回空 codes。
func TestProvider_CustomCodesDisabled_EmptyCodes(t *testing.T) {
	p := &staticRulesProvider{
		tempEnabled:   true,
		rules:         []TempUnschedulableRule{{ErrorCode: 403, Keywords: []string{"x"}, DurationMinutes: 10}},
		customEnabled: false,
		customCodes:   []int32{403, 418},
	}
	policy := p.GetAccountErrorPolicy(1)
	if len(policy.CustomErrorCodes) != 0 {
		t.Fatalf("expected empty codes when custom_error_codes_enabled=false, got %v", policy.CustomErrorCodes)
	}
	if len(policy.Rules) == 0 {
		t.Fatal("expected rules when temp_unschedulable_enabled=true")
	}
}

// TC10:nil provider -> 空操作。
func TestHandleUpstreamError_NilProvider_NoOp(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	svc := NewUpstreamRateService(func() time.Time { return now }, time.Minute)
	body := []byte(`{"error":"unusual activity"}`)
	dec, err := svc.HandleUpstreamError(context.TODO(), 101, 403, nil, body)
	if err != nil {
		t.Fatalf("HandleUpstreamError: %v", err)
	}
	if dec.StateChange != StateNoChange {
		t.Fatalf("StateChange=%v want StateNoChange when provider=nil", dec.StateChange)
	}
}

// TC11:两个标志都禁用 -> 空操作。
func TestHandleUpstreamError_ProviderAllDisabled_NoOp(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	provider := &staticRulesProvider{
		tempEnabled:   false,
		rules:         []TempUnschedulableRule{{ErrorCode: 403, Keywords: []string{"unusual activity"}, DurationMinutes: 30}},
		customEnabled: false,
		customCodes:   []int32{403},
	}
	svc := NewUpstreamRateService(
		func() time.Time { return now }, time.Minute,
		WithAccountErrorRulesProvider(provider),
	)
	body := []byte(`{"error":"unusual activity detected"}`)
	dec, err := svc.HandleUpstreamError(context.TODO(), 42, 403, nil, body)
	if err != nil {
		t.Fatalf("HandleUpstreamError: %v", err)
	}
	if dec.StateChange != StateNoChange {
		t.Fatalf("StateChange=%v want StateNoChange when both flags disabled", dec.StateChange)
	}
}

// TC12:规则匹配 -> StateTempUnsched 且 CooldownUntil 非零。
func TestHandleUpstreamError_RuleMatch_TempUnsched(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	provider := &staticRulesProvider{
		tempEnabled: true,
		rules:       []TempUnschedulableRule{{ErrorCode: 403, Keywords: []string{"unusual activity"}, DurationMinutes: 20}},
	}
	svc := NewUpstreamRateService(
		func() time.Time { return now }, time.Minute,
		WithAccountErrorRulesProvider(provider),
	)
	body := []byte(`{"error":"Unusual Activity triggered"}`)
	dec, err := svc.HandleUpstreamError(context.TODO(), 42, 403, nil, body)
	if err != nil {
		t.Fatalf("HandleUpstreamError: %v", err)
	}
	if dec.StateChange != StateTempUnsched {
		t.Fatalf("StateChange=%v want StateTempUnsched", dec.StateChange)
	}
	if dec.Reason != ReasonTempUnschedRule {
		t.Fatalf("Reason=%s want ReasonTempUnschedRule", dec.Reason)
	}
	expected := now.Add(20 * time.Minute)
	if !dec.CooldownUntil.Equal(expected) {
		t.Fatalf("CooldownUntil=%s want %s", dec.CooldownUntil, expected)
	}
}

// TC13(修复 3):经由 service 的自定义 code 匹配 -> 使用 defaultCooldown 的非零 CooldownUntil。
func TestHandleUpstreamError_CustomCode_NonZeroCooldown(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	defaultCooldown := 7 * time.Minute
	provider := &staticRulesProvider{
		customEnabled: true,
		customCodes:   []int32{403},
	}
	svc := NewUpstreamRateService(
		func() time.Time { return now }, defaultCooldown,
		WithAccountErrorRulesProvider(provider),
	)
	dec, err := svc.HandleUpstreamError(context.TODO(), 42, 403, nil, []byte(`{}`))
	if err != nil {
		t.Fatalf("HandleUpstreamError: %v", err)
	}
	if dec.StateChange != StateTempUnsched {
		t.Fatalf("StateChange=%v want StateTempUnsched", dec.StateChange)
	}
	if dec.Reason != ReasonCustomErrorCode {
		t.Fatalf("Reason=%s want ReasonCustomErrorCode", dec.Reason)
	}
	if dec.CooldownUntil.IsZero() {
		t.Fatal("CooldownUntil must be non-zero (FIX 3: dispatch skips zero CooldownUntil)")
	}
	expected := now.Add(defaultCooldown)
	if !dec.CooldownUntil.Equal(expected) {
		t.Fatalf("CooldownUntil=%s want %s", dec.CooldownUntil, expected)
	}
}

// TC14:现有 429 行为不受影响。
func TestHandleUpstreamError_429_Unaffected(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	provider := &staticRulesProvider{
		tempEnabled: true,
		rules:       []TempUnschedulableRule{{ErrorCode: 403, Keywords: []string{"unusual activity"}, DurationMinutes: 30}},
	}
	svc := NewUpstreamRateService(
		func() time.Time { return now }, time.Minute,
		WithAccountErrorRulesProvider(provider),
	)
	headers := http.Header{"Retry-After": []string{"3600"}}
	body := []byte(`{"error":"rate limited"}`)
	dec, err := svc.HandleUpstreamError(context.TODO(), 42, 429, headers, body)
	if err != nil {
		t.Fatalf("HandleUpstreamError: %v", err)
	}
	if dec.StateChange != StateRateLimited {
		t.Fatalf("StateChange=%v want StateRateLimited", dec.StateChange)
	}
}

func TestHandleUpstreamError_PoolModeSkipsUnmatchedLocalState(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	provider := &staticRulesProvider{
		poolMode:      true,
		customEnabled: true,
		customCodes:   []int32{418},
	}
	svc := NewUpstreamRateService(func() time.Time { return now }, time.Minute, WithAccountErrorRulesProvider(provider))

	dec, err := svc.HandleUpstreamError(context.Background(), 42, http.StatusTooManyRequests, http.Header{"Retry-After": []string{"60"}}, nil)
	if err != nil {
		t.Fatalf("HandleUpstreamError: %v", err)
	}
	if !dec.SuppressLocalState || dec.StateChange != StateNoChange || !dec.CooldownUntil.IsZero() {
		t.Fatalf("未匹配错误必须只故障转移且不改本地状态，得到 %+v", dec)
	}
	if !dec.ShouldFailover {
		t.Fatal("pool_mode 跳过状态写入时仍须允许故障转移")
	}
}

func TestHandleUpstreamError_PoolModeKeepsCustomRuleOverride(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	provider := &staticRulesProvider{
		poolMode:    true,
		tempEnabled: true,
		rules:       []TempUnschedulableRule{{ErrorCode: 503, Keywords: []string{"busy"}, DurationMinutes: 11}},
	}
	svc := NewUpstreamRateService(func() time.Time { return now }, time.Minute, WithAccountErrorRulesProvider(provider))

	dec, err := svc.HandleUpstreamError(context.Background(), 42, http.StatusServiceUnavailable, nil, []byte(`{"error":"busy"}`))
	if err != nil {
		t.Fatalf("HandleUpstreamError: %v", err)
	}
	if dec.SuppressLocalState || dec.StateChange != StateTempUnsched || dec.Reason != ReasonTempUnschedRule {
		t.Fatalf("自定义规则应覆盖 pool_mode 短路，得到 %+v", dec)
	}
}
