package router

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestRouter_Plan_RejectsMissingRequestID 强制要求每个请求在 Router
// 运行时都必须携带 request_id。
func TestRouter_Plan_RejectsMissingRequestID(t *testing.T) {
	r := NewDefaultRouter()
	_, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{TenantID: 1},
		Model:   ResolvedModel{ProtocolFamily: "anthropic_messages", PoolCandidates: []int64{42}},
	})
	if err == nil {
		t.Fatal("expected PlanError for missing RequestID")
	}
	var pe *PlanError
	if !errors.As(err, &pe) || pe.Code != "missing_request_id" {
		t.Fatalf("expected missing_request_id PlanError; got %v", err)
	}
}

// TestRouter_Plan_RejectsMissingTenant 强制要求当 Auth 尚未运行时
// Router 必须 fail closed。
func TestRouter_Plan_RejectsMissingTenant(t *testing.T) {
	r := NewDefaultRouter()
	_, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "req-x"},
		Model:   ResolvedModel{ProtocolFamily: "anthropic_messages", PoolCandidates: []int64{42}},
	})
	var pe *PlanError
	if !errors.As(err, &pe) || pe.Code != "missing_tenant" {
		t.Fatalf("expected missing_tenant PlanError; got %v", err)
	}
}

// TestRouter_Plan_RejectsUnknownModel 确保当 Registry 尚未对 model 的
// protocol family 做分类时，Router 拒绝规划。
func TestRouter_Plan_RejectsUnknownModel(t *testing.T) {
	r := NewDefaultRouter()
	_, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r1", TenantID: 99},
		Model:   ResolvedModel{PoolCandidates: []int64{42}}, // 没有 ProtocolFamily
	})
	var pe *PlanError
	if !errors.As(err, &pe) || pe.Code != "model_unsupported" {
		t.Fatalf("expected model_unsupported PlanError; got %v", err)
	}
}

// TestRouter_Plan_RequiresPoolCandidates 验证当 Registry 暴露出一个空的
// PoolCandidates 列表时，Router 会 fail closed。Registry 在上游本应
// 已经返回 ErrTenantNoAccess —— 这是纵深防御
// （N+5b 合成 plan §"requestPoolGroupID rewrite"）。
func TestRouter_Plan_RequiresPoolCandidates(t *testing.T) {
	r := NewDefaultRouter()
	_, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "rNP", TenantID: 7},
		Model:   ResolvedModel{ProtocolFamily: "anthropic_messages"}, // 没有 PoolCandidates
	})
	var pe *PlanError
	if !errors.As(err, &pe) || pe.Code != "no_eligible_pool" {
		t.Fatalf("expected no_eligible_pool PlanError; got %v", err)
	}
}

// TestRouter_Plan_UsesRankedCandidates 验证 Router 保留 registry 排序，
// 并输出有界 multi-attempt plan。
func TestRouter_Plan_UsesRankedCandidates(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context:  RequestContext{RequestID: "r-pri", TenantID: 5, APIKeyID: 6, UserID: 7},
		Model:    ResolvedModel{ProtocolFamily: "anthropic_messages", PoolCandidates: []int64{99, 100, 101}},
		Features: RequestFeatures{Stream: true, WantsToolUse: true},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Attempts) != 3 {
		t.Fatalf("expected 3 attempts; got %d", len(plan.Attempts))
	}
	assertAttempts(t, plan, []wantAttempt{
		{index: 0, poolGroupID: 99, reason: "primary"},
		{index: 1, poolGroupID: 100, reason: "cross_pool_fallback"},
		{index: 2, poolGroupID: 101, reason: "cross_pool_fallback"},
	})
	if plan.AttemptBudget != 3 {
		t.Fatalf("expected AttemptBudget=3; got %d", plan.AttemptBudget)
	}
	want := map[string]bool{"stream": true, "tools": true}
	for i, attempt := range plan.Attempts {
		if len(attempt.RequiredCapabilities) != len(want) {
			t.Fatalf("attempt %d RequiredCapabilities mismatch; got %v want %v", i, attempt.RequiredCapabilities, want)
		}
		for _, c := range attempt.RequiredCapabilities {
			if !want[c] {
				t.Fatalf("attempt %d unexpected capability %q in plan; want only stream+tools", i, c)
			}
		}
	}
}

func TestRouter_Plan_MetadataAbsentFallsBackToPoolCandidateOrder(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-meta-missing", TenantID: 5},
		Model: ResolvedModel{
			ProtocolFamily:  "openai_chat",
			ProviderModelID: "default-upstream",
			PoolCandidates:  []int64{201, 202},
		},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	assertAttempts(t, plan, []wantAttempt{
		{index: 0, poolGroupID: 201, reason: "primary", upstreamModelID: "default-upstream"},
		{index: 1, poolGroupID: 202, reason: "cross_pool_fallback", upstreamModelID: "default-upstream"},
		{index: 2, poolGroupID: 201, reason: "same_pool_account_failover", upstreamModelID: "default-upstream"},
	})
	if plan.AttemptBudget != 3 {
		t.Fatalf("AttemptBudget=%d want 3", plan.AttemptBudget)
	}
}

func TestRouter_Plan_SinglePoolAddsSamePoolFailover(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-single", TenantID: 5},
		Model:   ResolvedModel{ProtocolFamily: "openai_chat", PoolCandidates: []int64{301}},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	assertAttempts(t, plan, []wantAttempt{
		{index: 0, poolGroupID: 301, reason: "primary"},
		{index: 1, poolGroupID: 301, reason: "same_pool_account_failover"},
	})
	if plan.AttemptBudget != 2 {
		t.Fatalf("AttemptBudget=%d want 2", plan.AttemptBudget)
	}
}

func TestRouter_Plan_TruncatesBudgetAtThree(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-budget", TenantID: 5},
		Model:   ResolvedModel{ProtocolFamily: "openai_chat", PoolCandidates: []int64{401, 402, 403, 404}},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	assertAttempts(t, plan, []wantAttempt{
		{index: 0, poolGroupID: 401, reason: "primary"},
		{index: 1, poolGroupID: 402, reason: "cross_pool_fallback"},
		{index: 2, poolGroupID: 403, reason: "cross_pool_fallback"},
	})
	if plan.AttemptBudget != 3 {
		t.Fatalf("AttemptBudget=%d want 3", plan.AttemptBudget)
	}
}

func TestRouter_Plan_CarriesPerPoolUpstreamModelOverride(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-model-override", TenantID: 5},
		Model: ResolvedModel{
			ProtocolFamily:  "openai_chat",
			ProviderModelID: "default-model",
			PoolCandidates:  []int64{501, 502},
			PoolMetadata: []PoolCandidateMeta{
				{PoolGroupID: 501, ProviderModelID: "pool-a-model", Priority: 10},
				{PoolGroupID: 502, ProviderModelID: "pool-b-model", Priority: 20},
			},
		},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	assertAttempts(t, plan, []wantAttempt{
		{index: 0, poolGroupID: 501, reason: "primary", upstreamModelID: "pool-a-model"},
		{index: 1, poolGroupID: 502, reason: "cross_pool_fallback", upstreamModelID: "pool-b-model"},
		{index: 2, poolGroupID: 501, reason: "same_pool_account_failover", upstreamModelID: "pool-a-model"},
	})
}

func TestRouter_Plan_RetryableEndClassesMatchPreDeliveryFailures(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-retryable-classes", TenantID: 5},
		Model:   ResolvedModel{ProtocolFamily: "openai_chat", PoolCandidates: []int64{601, 602}},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.RetryableEndClasses == nil {
		t.Fatal("RetryableEndClasses must be non-nil for a multi-attempt plan")
	}

	retryable := make(map[string]bool, len(plan.RetryableEndClasses))
	for _, endClass := range plan.RetryableEndClasses {
		retryable[endClass] = true
	}
	for _, want := range []string{
		"upstream_error_5xx",
		"upstream_rate_limit",
		"first_token_timeout",
		"inter_event_timeout",
	} {
		if !retryable[want] {
			t.Fatalf("RetryableEndClasses missing %q; got %v", want, plan.RetryableEndClasses)
		}
	}
	for _, forbidden := range []string{
		"upstream_auth_failure",
		"upstream_error_4xx",
		"response_event_too_large",
	} {
		if retryable[forbidden] {
			t.Fatalf("RetryableEndClasses must not include %q; got %v", forbidden, plan.RetryableEndClasses)
		}
	}
}

// TestRouter_Plan_StampsConcatenatedSnapshot 验证 registry+router 快照
// 按 migration 0008 中记录的格式拼接到 RoutePlan.SnapshotVersion 上。
func TestRouter_Plan_StampsConcatenatedSnapshot(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-stamp", TenantID: 8},
		Model: ResolvedModel{
			ProtocolFamily:  "anthropic_messages",
			PoolCandidates:  []int64{42},
			SnapshotVersion: "registry:8:5",
		},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := "registry:8:5;router:v0.2-binding-weighted"
	if plan.SnapshotVersion != want {
		t.Fatalf("SnapshotVersion = %q; want %q", plan.SnapshotVersion, want)
	}
}

// TestRouter_Plan_StampsFallbackOnEmptyRegistryStamp 覆盖
// Resolved.SnapshotVersion 为空（遗留 / 启动边界）时的防御分支。
// 该 stamp 绝不能以一个孤立的分号开头。
func TestRouter_Plan_StampsFallbackOnEmptyRegistryStamp(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-fb", TenantID: 9},
		Model: ResolvedModel{
			ProtocolFamily: "anthropic_messages",
			PoolCandidates: []int64{42},
			// SnapshotVersion 故意留空
		},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if strings.HasPrefix(plan.SnapshotVersion, ";") {
		t.Fatalf("SnapshotVersion %q must not start with semicolon", plan.SnapshotVersion)
	}
	if !strings.HasPrefix(plan.SnapshotVersion, "registry:unknown;") {
		t.Fatalf("SnapshotVersion %q should fall back to registry:unknown prefix", plan.SnapshotVersion)
	}
}

type wantAttempt struct {
	index           int
	poolGroupID     int64
	reason          string
	upstreamModelID string
}

func assertAttempts(t *testing.T, plan RoutePlan, want []wantAttempt) {
	t.Helper()
	if len(plan.Attempts) != len(want) {
		t.Fatalf("attempt len=%d want %d", len(plan.Attempts), len(want))
	}
	for i, w := range want {
		got := plan.Attempts[i]
		if got.Index != w.index || got.PoolGroupID != w.poolGroupID || got.Reason != w.reason {
			t.Fatalf("attempt[%d]=Index:%d PoolGroupID:%d Reason:%q; want Index:%d PoolGroupID:%d Reason:%q",
				i, got.Index, got.PoolGroupID, got.Reason, w.index, w.poolGroupID, w.reason)
		}
		if got.UpstreamModelID != w.upstreamModelID {
			t.Fatalf("attempt[%d].UpstreamModelID=%q want %q", i, got.UpstreamModelID, w.upstreamModelID)
		}
	}
}

// TestRequiredCapabilities_EmitsMapperVocabularyNotProtoTokens 锁死能力词表:
// requiredCapabilities 必须吐 {stream,tools,vision,json,audio} (账号 capability_flags
// 被戳的同一套词,registry 的 model_capabilities 也认 "audio"),而不是 proto 层的
// 'image'/'tool_use'。词表错位时 @> 会排除所有账号 -> 恒 no_eligible_account 503。
// mutation: 把 mapper 里的 "vision" 改成 "image" 或 "tools" 改成 "tool_use" -> 转红。
func TestRequiredCapabilities_EmitsMapperVocabularyNotProtoTokens(t *testing.T) {
	caps := requiredCapabilities(RequestFeatures{
		Stream:       true,
		WantsToolUse: true,
		WantsVision:  true,
		WantsJSON:    true,
		WantsAudio:   true,
	})
	got := map[string]bool{}
	for _, c := range caps {
		got[c] = true
	}
	for _, want := range []string{"stream", "tools", "vision", "json", "audio"} {
		if !got[want] {
			t.Fatalf("requiredCapabilities missing token %q; got %v", want, caps)
		}
	}
	for _, forbidden := range []string{"image", "tool_use", "reasoning_high", "input_audio", "long_context"} {
		if got[forbidden] {
			t.Fatalf("requiredCapabilities emitted off-vocabulary token %q; would exclude every account; got %v", forbidden, caps)
		}
	}
	if len(caps) != 5 {
		t.Fatalf("requiredCapabilities should emit exactly the 5 locked tokens; got %v", caps)
	}
}

// TestRequiredCapabilities_AudioTokenDiscriminates 锁死 audio 词表映射: WantsAudio
// 必须且只能吐 capability token "audio" (registry model_capabilities 与账号
// capability_flags 共用的同一套词),不带任何其它约束。这样 SelectionRequest
// .RequiredCapabilities 经 @> 过滤只筛掉缺 audio 的账号,不误排他人。
// mutation: 删 mapper 里 `if f.WantsAudio { append "audio" }` 这一支 -> 第一个断言
// (audio token 缺失) 转红;把 token 写成 "input_audio"/"voice" 等错词 -> 同样转红。
func TestRequiredCapabilities_AudioTokenDiscriminates(t *testing.T) {
	caps := requiredCapabilities(RequestFeatures{WantsAudio: true})
	if len(caps) != 1 || caps[0] != "audio" {
		t.Fatalf("WantsAudio-only request must emit exactly [audio]; got %v", caps)
	}

	// 反向分支：一个不需要 audio 的请求绝不能携带 audio 约束（否则
	// 每个纯文本调用都会被钉死在 audio 类账号上而被饿死）。
	// 做一次正确值与基线的对照。
	noAudio := requiredCapabilities(RequestFeatures{Stream: true, WantsVision: true})
	for _, c := range noAudio {
		if c == "audio" {
			t.Fatalf("non-audio request leaked the audio token; got %v", noAudio)
		}
	}
}

// TestRequiredCapabilities_OnlyEmitsRequestedTokens 守过度检测: 只有被请求的 feature
// 才进 required_capabilities。不请求 vision/json/tools 时不能凭空多约束 (会缩小可用账号集)。
// mutation: 在 mapper 里无条件 append "vision" -> 转红。
func TestRequiredCapabilities_OnlyEmitsRequestedTokens(t *testing.T) {
	caps := requiredCapabilities(RequestFeatures{Stream: true})
	if len(caps) != 1 || caps[0] != "stream" {
		t.Fatalf("stream-only request should emit only [stream]; got %v", caps)
	}
	if len(requiredCapabilities(RequestFeatures{})) != 0 {
		t.Fatalf("empty features should emit no capability constraints")
	}
}
