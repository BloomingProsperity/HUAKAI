package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func TestPR4RetryKeepsGeneratedLogicalRequestIDStable(t *testing.T) {
	ex := &chatExecution{
		r:    httptest.NewRequest("POST", "/v1/chat/completions", nil),
		body: []byte(`{"model":"gpt-4.1-mini","messages":[]}`),
	}
	ex.ensureIdempotencyState()
	first := ex.logicalRequestID
	if first == "" {
		t.Fatal("first logical request id is empty")
	}

	ex.prepareNextAttemptAfterAbort()
	ex.ensureIdempotencyState()

	if ex.logicalRequestID != first {
		t.Fatalf("logical request id changed across attempts: first=%s second=%s", first, ex.logicalRequestID)
	}
}

func TestPR5EndClassFallsThroughUnknownClassificationToTransportClass(t *testing.T) {
	got := endClassFromAttemptFailure(gateway.Classification{}, gateway.AttemptRetryDecision{
		TransportClass: gateway.TransportErrorConnectTimeout,
	})
	if got != gateway.InterEventTimeout {
		t.Fatalf("EndClass=%q want %q for connect timeout", got, gateway.InterEventTimeout)
	}
}

func TestPR4PrepareNextAttemptAfterAbortClearsReservationAndAcquisition(t *testing.T) {
	token := uuid.New()
	ex := &chatExecution{
		reserveRes:        &billing.ReserveResult{ClaimID: 123},
		selRes:            &pool.SelectionResult{AccountID: 456, AcquisitionToken: token},
		acquiredAccountID: 456,
		acquisitionToken:  token,
		healthKeyOK:       true,
	}

	ex.prepareNextAttemptAfterAbort()

	if ex.reserveRes != nil {
		t.Fatalf("reserveRes still set: %+v", ex.reserveRes)
	}
	if ex.selRes != nil {
		t.Fatalf("selection still set: %+v", ex.selRes)
	}
	if ex.acquiredAccountID != 0 {
		t.Fatalf("acquiredAccountID=%d want 0", ex.acquiredAccountID)
	}
	if ex.acquisitionToken != uuid.Nil {
		t.Fatalf("acquisitionToken=%s want nil UUID", ex.acquisitionToken)
	}
	if ex.healthKeyOK {
		t.Fatal("healthKeyOK should be cleared for the next attempt")
	}
}

// TestUpstreamInboundBodySkipsModelRewriteForDifyChat 抓的回归:dify_chat
// 的翻译产物 body 没有 model 字段(Dify 契约无此键),流式路径经
// upstreamInboundBody 时不得被 rewriteUpstreamModel 注入顶层 model 污染契约。
// Mutation:删掉 upstreamInboundBody 的 dify_chat 跳过 → 本测试红。
func TestUpstreamInboundBodySkipsModelRewriteForDifyChat(t *testing.T) {
	original := []byte(`{"inputs":{},"query":"USER: hi","response_mode":"streaming","user":"req-1","auto_generate_name":false}`)
	ex := &chatExecution{
		upstreamModelID: "dify-app-model",
		body:            original,
		resolved:        registry.Resolved{ProtocolFamily: "dify_chat"},
	}
	out := ex.upstreamInboundBody(ex.body)
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("outbound body is not JSON: %v", err)
	}
	if _, has := parsed["model"]; has {
		t.Fatalf("dify_chat 出站 body 被注入 model 字段: %s", out)
	}
	if parsed["query"] != "USER: hi" {
		t.Fatalf("body 内容被意外改写: %s", out)
	}
}

func TestUpstreamInboundBodyUsesResolvedModelWithoutMutatingOriginal(t *testing.T) {
	original := []byte(`{"model":"primary-model","messages":[{"role":"user","content":"hello"}]}`)
	ex := &chatExecution{
		upstreamModelID: "fallback-model",
		body:            original,
	}

	out := ex.upstreamInboundBody(ex.body)
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("outbound body is not JSON: %v", err)
	}
	if parsed["model"] != "fallback-model" {
		t.Fatalf("outbound model=%v want fallback-model body=%s", parsed["model"], string(out))
	}
	if string(ex.body) != string(original) {
		t.Fatalf("original body mutated: got %s want %s", string(ex.body), string(original))
	}
}

func TestUpstreamInboundBodyAppliesChannelBodyParamGateAfterModelRewrite(t *testing.T) {
	original := []byte(`{"model":"client-model","temperature":0.9,"service_tier":"flex","stream_options":{"include_obfuscation":true,"include_usage":true},"messages":[{"role":"user","content":"hello"}]}`)
	ex := &chatExecution{
		upstreamModelID: "provider-model",
		body:            original,
		attempt:         router.AttemptPlan{PoolGroupID: 42},
		resolved: registry.Resolved{BindingMetadata: []registry.BindingMetadata{{
			PoolGroupID:     42,
			BodyParamStrips: []string{"service_tier", "stream_options.include_obfuscation"},
			ParamOverride: map[string]json.RawMessage{
				"temperature": json.RawMessage(`0`),
			},
		}}},
	}

	out := ex.upstreamInboundBody(ex.body)
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("outbound body is not JSON: %v body=%s", err, out)
	}
	if parsed["model"] != "provider-model" {
		t.Fatalf("model=%v want provider-model body=%s", parsed["model"], out)
	}
	if parsed["temperature"] != float64(0) {
		t.Fatalf("temperature=%v want 0 body=%s", parsed["temperature"], out)
	}
	if _, ok := parsed["service_tier"]; ok {
		t.Fatalf("service_tier still present after channel strip: %s", out)
	}
	streamOptions, ok := parsed["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("stream_options missing or non-object: %s", out)
	}
	if _, ok := streamOptions["include_obfuscation"]; ok {
		t.Fatalf("include_obfuscation still present: %s", out)
	}
	if _, ok := streamOptions["include_usage"]; !ok {
		t.Fatalf("include_usage sibling stripped: %s", out)
	}
	if string(ex.body) != string(original) {
		t.Fatalf("original body mutated: got %s want %s", string(ex.body), string(original))
	}
}

func TestDegradeFailureIfAbortFailedUsesSafeAbortReasonAndLogsErrorClass(t *testing.T) {
	const marker = "SENSITIVE_ABORT_REASON_MARKER"
	logs := captureSlogForTest(t)
	failure := terminalLocalAttemptFailure(409, "claim_race", "claim could not be completed", "claim_race", errors.New("claim race"))

	got := degradeFailureIfAbortFailed(context.Background(), "req-abort-safe", failure, errors.New(marker))
	if got == nil {
		t.Fatal("degradeFailureIfAbortFailed returned nil")
	}
	if strings.Contains(got.AbortReason, marker) || strings.Contains(got.Decision.AbortReason, marker) {
		t.Fatalf("abort reason leaked marker: failure=%+v", got)
	}
	if got.AbortReason != "claim_race;abort_failed=1" || got.Decision.AbortReason != got.AbortReason {
		t.Fatalf("abort reason=%q decision=%q want safe abort_failed marker", got.AbortReason, got.Decision.AbortReason)
	}
	assertLogContains(t, logs, "req-abort-safe", "abort_failed", "error_class")
	assertLogOmits(t, logs, marker)
}

// MUTATION: endClassFromAttemptFailure 漏 DM-06 持久传输类 → 红——健康记账
// 信号落 UnknownTermination,channelhealth 看不见持久故障(不摘账号复发)。
func TestEndClassFromAttemptFailure_PersistentTransportClassesAreUpstream5xx(t *testing.T) {
	for _, class := range []gateway.TransportErrorClass{
		gateway.TransportErrorConnectionRefused,
		gateway.TransportErrorDNSFailure,
		gateway.TransportErrorNetworkUnreachable,
		gateway.TransportErrorProxyFailure,
	} {
		got := endClassFromAttemptFailure(gateway.Classification{}, gateway.AttemptRetryDecision{TransportClass: class})
		if got != gateway.UpstreamError5xx {
			t.Fatalf("class %s endClass=%s want UpstreamError5xx", class, got)
		}
	}
}

// MUTATION: stripCrossAccountResponseChain 去掉 miss 判定(任何 responses 都
// 剥)→ hit/none 用例红;去掉剥除 → miss 用例红(DM-07:只在 sticky miss 剥
// 跨账号链 ID,其余场景 body 必须原样)。
func TestStripCrossAccountResponseChain(t *testing.T) {
	body := []byte(`{"model":"gpt-5.2","previous_response_id":"resp_abc","input":"hi"}`)

	ex := &chatExecution{
		ctx:            context.Background(),
		clientProtocol: proto.ClientProtocolOpenAIResponses,
		selRes:         &pool.SelectionResult{AccountID: 7, StickyState: pool.StickyStateMiss},
	}
	got := ex.stripCrossAccountResponseChain(body)
	if strings.Contains(string(got), "previous_response_id") {
		t.Fatalf("sticky miss 应剥 previous_response_id: %s", got)
	}
	if !strings.Contains(string(got), `"input":"hi"`) || !strings.Contains(string(got), `"model":"gpt-5.2"`) {
		t.Fatalf("其余字段必须保留: %s", got)
	}

	for name, e := range map[string]*chatExecution{
		"sticky hit":  {ctx: context.Background(), clientProtocol: proto.ClientProtocolOpenAIResponses, selRes: &pool.SelectionResult{AccountID: 7, StickyState: pool.StickyStateHit}},
		"no binding":  {ctx: context.Background(), clientProtocol: proto.ClientProtocolOpenAIResponses, selRes: &pool.SelectionResult{AccountID: 7}},
		"nil selRes":  {ctx: context.Background(), clientProtocol: proto.ClientProtocolOpenAIResponses},
		"chat client": {ctx: context.Background(), clientProtocol: proto.ClientProtocolOpenAIChat, selRes: &pool.SelectionResult{AccountID: 7, StickyState: pool.StickyStateMiss}},
	} {
		if got := e.stripCrossAccountResponseChain(body); string(got) != string(body) {
			t.Fatalf("[%s] 不应改动 body: %s", name, got)
		}
	}
}

// MUTATION: writeAttemptFailure 去掉 clearRetryableAttemptFailureHeaders 调用,
// 或清单删掉 X-Accel-Buffering → 红(DM-19:终局 JSON 错误携带流式残留头)。
func TestWriteAttemptFailureClearsStreamResidualHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	// 模拟 forwardSSEAndSettle 已预设的流式头之后尝试失败
	rec.Header().Set("Trailer", "X-HUAKAI-Stream-State")
	rec.Header().Set("X-Accel-Buffering", "no")
	rec.Header().Set("Cache-Control", "no-cache")
	rec.Header().Set(headerHUAKAIStreamState, "in_flight")

	writeAttemptFailure(rec, &classifiedAttemptFailure{ClientStatus: 502})

	for _, h := range []string{"Trailer", "X-Accel-Buffering", "Cache-Control", headerHUAKAIStreamState} {
		if got := rec.Header().Get(h); got != "" {
			t.Fatalf("终局错误响应残留 %s=%q", h, got)
		}
	}
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "error") {
		t.Fatalf("应写 502 JSON error: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 已开播(DeliveredToClient)绝不能动头/写体
	rec2 := httptest.NewRecorder()
	rec2.Header().Set("Trailer", "X-HUAKAI-Stream-State")
	writeAttemptFailure(rec2, &classifiedAttemptFailure{ClientStatus: 502, DeliveredToClient: true})
	if rec2.Header().Get("Trailer") == "" || rec2.Body.Len() != 0 {
		t.Fatalf("DeliveredToClient 不应动头/写体: %v %q", rec2.Header(), rec2.Body.String())
	}

	// 诊断标记必须在终局错误上存活(PR5 abort-failed 取证契约)
	rec3 := httptest.NewRecorder()
	rec3.Header().Set(headerHuakaiAbortFailed, "1")
	rec3.Header().Set("Trailer", "X-HUAKAI-Stream-State")
	writeAttemptFailure(rec3, &classifiedAttemptFailure{ClientStatus: 502})
	if rec3.Header().Get(headerHuakaiAbortFailed) != "1" {
		t.Fatal("终局错误必须保留 abort_failed 诊断标记")
	}
	if rec3.Header().Get("Trailer") != "" {
		t.Fatal("终局错误仍须清 Trailer")
	}
}

// MUTATION: clientTailMessageRole 任一协议分支解析错位 → 对应子断言红(DM-16)。
func TestClientTailMessageRole(t *testing.T) {
	cases := []struct {
		name  string
		proto proto.ClientProtocol
		body  string
		want  string
	}{
		{"chat tail user", proto.ClientProtocolOpenAIChat, `{"messages":[{"role":"assistant"},{"role":"user"}]}`, "user"},
		{"chat tail tool", proto.ClientProtocolOpenAIChat, `{"messages":[{"role":"user"},{"role":"Tool"}]}`, "tool"},
		{"anthropic tail assistant", proto.ClientProtocolAnthropicMessages, `{"messages":[{"role":"user"},{"role":"assistant"}]}`, "assistant"},
		{"gemini tail model", proto.ClientProtocolGemini, `{"contents":[{"role":"user"},{"role":"model"}]}`, "model"},
		{"responses string input", proto.ClientProtocolOpenAIResponses, `{"input":"hello"}`, "user"},
		{"responses tail function output", proto.ClientProtocolOpenAIResponses, `{"input":[{"role":"user"},{"type":"function_call_output"}]}`, "tool"},
		{"responses tail user item", proto.ClientProtocolOpenAIResponses, `{"input":[{"type":"function_call_output"},{"role":"user"}]}`, "user"},
		{"unparseable", proto.ClientProtocolOpenAIChat, `not json`, ""},
		{"empty messages", proto.ClientProtocolOpenAIChat, `{"messages":[]}`, ""},
	}
	for _, tc := range cases {
		if got := clientTailMessageRole(tc.proto, []byte(tc.body)); got != tc.want {
			t.Fatalf("[%s] got %q want %q", tc.name, got, tc.want)
		}
	}
}

// TestActiveBindingSelectionMode 钉死 dispatch 级 selection_mode 取值:必须按当前请求命中的
// binding(ex.attempt.PoolGroupID)取其 selection_mode 透传给 SelectionRequest,而非取错 binding
// 或恒空。这是路由加权激活闭环在 dispatch 端的接线点。
// MUTATION:若 activeBindingSelectionMode 恒返回 ""(漏接)→ 命中 priority_weighted binding 的请求
// 也走默认 → 下面 weighted 断言红;若取错 binding(忽略 PoolGroupID)→ 多 binding 用例红。
func TestActiveBindingSelectionMode(t *testing.T) {
	ex := &chatExecution{
		resolved: registry.Resolved{
			BindingMetadata: []registry.BindingMetadata{
				{BindingID: 1, PoolGroupID: 701, SelectionMode: "strict_priority"},
				{BindingID: 2, PoolGroupID: 702, SelectionMode: "priority_weighted"},
			},
		},
	}

	// 命中 702 → 取到 priority_weighted。
	ex.attempt = router.AttemptPlan{PoolGroupID: 702}
	if got := ex.activeBindingSelectionMode(); got != "priority_weighted" {
		t.Fatalf("命中 pool 702 selection_mode=%q, 期望 priority_weighted", got)
	}
	// 命中 701 → 取到 strict_priority(证明按 PoolGroupID 取对 binding,非取首条)。
	ex.attempt = router.AttemptPlan{PoolGroupID: 701}
	if got := ex.activeBindingSelectionMode(); got != "strict_priority" {
		t.Fatalf("命中 pool 701 selection_mode=%q, 期望 strict_priority", got)
	}
	// 无 binding 元数据 → 空(默认 strict)。
	empty := &chatExecution{}
	if got := empty.activeBindingSelectionMode(); got != "" {
		t.Fatalf("无 binding 时 selection_mode=%q, 期望空", got)
	}
}
