package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/thinkingnorm"
)

// TestIngressProtocolForEffort_ResponsesIsUnmodeled 守护 v2-review 的 S2:
// OpenAI Responses ingress 解析器消费的是一个嵌套的 reasoning 对象,而非
// chat 解析器读取的顶层 reasoning_effort 字符串。把 Responses 映射到
// IngressOpenAIChat 会发出一个被规范化悄悄丢弃的 reasoning_effort——
// 即静默丢失 effort。Responses 必须映射到 IngressOther(未建模 →
// 请求保持不变,无静默丢失;原生接线是后续工作)。
//
// 捕获的回归:把 ClientProtocolOpenAIResponses 折回 OpenAIChat 分支会让本测试变红。
func TestIngressProtocolForEffort_ResponsesIsUnmodeled(t *testing.T) {
	if got := ingressProtocolForEffort(proto.ClientProtocolOpenAIResponses); got != thinkingnorm.IngressOther {
		t.Fatalf("OpenAI Responses ingress -> %v, want IngressOther (else suffix effort is silently dropped during canonicalization)", got)
	}
	// 健全性检查:已建模的 ingress 仍正确映射。
	if got := ingressProtocolForEffort(proto.ClientProtocolOpenAIChat); got != thinkingnorm.IngressOpenAIChat {
		t.Fatalf("OpenAIChat ingress -> %v, want IngressOpenAIChat", got)
	}
	if got := ingressProtocolForEffort(proto.ClientProtocolAnthropicMessages); got != thinkingnorm.IngressAnthropic {
		t.Fatalf("Anthropic ingress -> %v, want IngressAnthropic", got)
	}
}

// perModelRegistry 是一个 registry 伪实现,只解析 entries 中的名字;
// 其它任何名字返回 registry.ErrUnknownModel(即真正触发 404)。它对
// ResolveModel 的调用计数,使测试能证明 no-suffix / resolved-first
// 路径不会产生额外查询。Capabilities 会透传,以便驱动 reasoning 闸门。
type perModelRegistry struct {
	entries map[string]registry.Resolved
	calls   int
}

func (p *perModelRegistry) ResolveModel(_ context.Context, name string, _ int64) (registry.Resolved, error) {
	p.calls++
	if r, ok := p.entries[name]; ok {
		return r, nil
	}
	return registry.Resolved{}, registry.ErrUnknownModel
}

func reasoningResolved(alias string) registry.Resolved {
	return registry.Resolved{
		PublicAlias:      alias,
		CanonicalModelID: "x/" + alias,
		ProviderModelID:  alias,
		ProtocolFamily:   "openai_chat",
		Capabilities:     []string{"reasoning"},
		PoolCandidates:   []int64{42},
	}
}

func newEffortExecution(t *testing.T, model, body string, ingress proto.ClientProtocol, reg *perModelRegistry) *chatExecution {
	t.Helper()
	ex := &chatExecution{
		ctx:            context.Background(),
		ident:          auth.Identity{TenantID: 7, UserID: 3, APIKeyID: 9},
		d:              ChatHandlerDeps{Router: router.NewDefaultRouter(), Registry: reg},
		body:           []byte(body),
		req:            chatRequest{Model: model},
		clientProtocol: ingress,
		requestID:      "r-effort",
	}
	return ex
}

// 回归(S1 误剥,致命款):一个真实上线的模型 "yi-medium"
// (定价种子迁移 0131)经过 ingress 钩子后,必须让 routing/pricing 仍看到
// "yi-medium"——绝不能是 "yi"。因为完整名在第一次 ResolveModel 即解析成功,
// 不会进入 effort-suffix 路径,且恰好只发生一次 registry 调用。
// 变异:在 ingress 处做纯字符串剥离 -> routing 看到 "yi" -> 此处 ex.req.Model
// 变成 "yi" -> 变红。
func TestPrepareRoute_YiMediumNotFalseStripped(t *testing.T) {
	reg := &perModelRegistry{entries: map[string]registry.Resolved{
		"yi-medium": reasoningResolved("yi-medium"),
	}}
	ex := newEffortExecution(t, "yi-medium",
		`{"model":"yi-medium","messages":[{"role":"user","content":"hi"}]}`,
		proto.ClientProtocolOpenAIChat, reg)

	if ok := ex.prepareRoute(httptest.NewRecorder()); !ok {
		t.Fatalf("prepareRoute must succeed for real model yi-medium")
	}
	if ex.req.Model != "yi-medium" {
		t.Fatalf("routing must see base model yi-medium, never yi; got %q", ex.req.Model)
	}
	if reg.calls != 1 {
		t.Fatalf("a resolving model must make exactly 1 registry call (no suffix probe); made %d", reg.calls)
	}
	if _, ok := topField(t, ex.body, "reasoning_effort"); ok {
		t.Fatalf("yi-medium body must not gain a reasoning_effort field")
	}
}

// 回归:无后缀的未知模型仍以恰好一次 registry 调用 404——没有 effort token
// 时不会运行后缀探测。变异:无条件运行后缀探测 -> 2 次调用 -> 变红。
func TestPrepareRoute_NoSuffixUnknownModelSingleCall(t *testing.T) {
	reg := &perModelRegistry{entries: map[string]registry.Resolved{}}
	ex := newEffortExecution(t, "totally-unknown",
		`{"model":"totally-unknown","messages":[]}`,
		proto.ClientProtocolOpenAIChat, reg)
	if ok := ex.prepareRoute(httptest.NewRecorder()); ok {
		t.Fatalf("unknown model must 404 (prepareRoute false)")
	}
	if reg.calls != 1 {
		t.Fatalf("no-suffix unknown model must make exactly 1 registry call; made %d", reg.calls)
	}
}

// 回归:一个真正的 "<reasoning-model>-<effort>",其完整名无法解析但其 base
// 可以(具备 reasoning 能力)会被规范化:routing 看到 base,且请求体获得
// reasoning_effort。变异:跳过 ingress 规范化 -> 完整名无法解析 ->
// prepareRoute 返回 404 -> 变红。
func TestPrepareRoute_ThinkingHighStrippedBaseRouted(t *testing.T) {
	reg := &perModelRegistry{entries: map[string]registry.Resolved{
		"gpt-5-thinking": reasoningResolved("gpt-5-thinking"),
	}}
	ex := newEffortExecution(t, "gpt-5-thinking-high",
		`{"model":"gpt-5-thinking-high","messages":[{"role":"user","content":"hi"}]}`,
		proto.ClientProtocolOpenAIChat, reg)

	if ok := ex.prepareRoute(httptest.NewRecorder()); !ok {
		t.Fatalf("prepareRoute must succeed after stripping to base gpt-5-thinking")
	}
	if ex.req.Model != "gpt-5-thinking" {
		t.Fatalf("routing must see base gpt-5-thinking; got %q", ex.req.Model)
	}
	v, ok := topField(t, ex.body, "reasoning_effort")
	if !ok || v != "high" {
		t.Fatalf("body must carry reasoning_effort=high; got %v ok=%v", v, ok)
	}
}

// 回归(S2 跨协议):到达 OPENAI-CHAT ingress 的 "claude-...-high" 必须产出一个
// openai-chat 解析器能读取的 reasoning_effort——而非一个会被丢弃的 thinking
// 对象。参数由 ingress 决定,而非由模型名决定。变异:按模型名分类
// (claude -> thinking) -> 在 openai-chat 请求体上放 thinking 对象 ->
// 下游丢弃 -> 此处 reasoning_effort 缺失 -> 变红。
func TestPrepareRoute_ClaudeViaOpenAIChatIngressEmitsReasoningEffort(t *testing.T) {
	reg := &perModelRegistry{entries: map[string]registry.Resolved{
		"claude-opus-4-8": reasoningResolved("claude-opus-4-8"),
	}}
	ex := newEffortExecution(t, "claude-opus-4-8-high",
		`{"model":"claude-opus-4-8-high","messages":[{"role":"user","content":"hi"}]}`,
		proto.ClientProtocolOpenAIChat, reg)

	if ok := ex.prepareRoute(httptest.NewRecorder()); !ok {
		t.Fatalf("prepareRoute must succeed after stripping to base claude-opus-4-8")
	}
	if ex.req.Model != "claude-opus-4-8" {
		t.Fatalf("routing must see base claude-opus-4-8; got %q", ex.req.Model)
	}
	if v, ok := topField(t, ex.body, "reasoning_effort"); !ok || v != "high" {
		t.Fatalf("openai-chat ingress must emit reasoning_effort=high; got %v ok=%v", v, ok)
	}
	if _, ok := topField(t, ex.body, "thinking"); ok {
		t.Fatalf("openai-chat ingress must NOT emit a thinking object the parser would drop")
	}
}

// 回归:当 base 不具备 reasoning 能力时,后缀被原样保留,请求会像没有该特性
// 时一样 404(无行为变化)。变异:不论是否具备 reasoning 能力都剥离 ->
// base 可路由 -> prepareRoute 成功 -> 变红。
func TestPrepareRoute_BaseNotReasoningNotStripped(t *testing.T) {
	nonReasoning := reasoningResolved("foo")
	nonReasoning.Capabilities = []string{"text", "tools"} // 无 reasoning/thinking
	reg := &perModelRegistry{entries: map[string]registry.Resolved{
		"foo": nonReasoning,
	}}
	ex := newEffortExecution(t, "foo-high",
		`{"model":"foo-high","messages":[]}`,
		proto.ClientProtocolOpenAIChat, reg)
	if ok := ex.prepareRoute(httptest.NewRecorder()); ok {
		t.Fatalf("non-reasoning base must NOT be stripped; request must 404")
	}
}

// 回归(经真实钩子的钳制):claude-opus-4-8-high 通过 anthropic ingress 且
// max_tokens=2000 时,产出的 thinking 预算被钳制到 2000,而非 24576。
// 变异:去掉钳制 -> 24576 -> 变红。
func TestPrepareRoute_AnthropicIngressBudgetClamped(t *testing.T) {
	reg := &perModelRegistry{entries: map[string]registry.Resolved{
		"claude-opus-4-8": reasoningResolved("claude-opus-4-8"),
	}}
	ex := newEffortExecution(t, "claude-opus-4-8-high",
		`{"model":"claude-opus-4-8-high","max_tokens":2000,"messages":[]}`,
		proto.ClientProtocolAnthropicMessages, reg)
	if ok := ex.prepareRoute(httptest.NewRecorder()); !ok {
		t.Fatalf("prepareRoute must succeed")
	}
	v, ok := topField(t, ex.body, "thinking")
	if !ok {
		t.Fatalf("anthropic ingress must emit a thinking object")
	}
	obj := v.(map[string]interface{})
	if got := obj["budget_tokens"].(float64); got != 2000 {
		t.Fatalf("thinking budget must clamp to 2000; got %v", got)
	}
}

func topField(t *testing.T, body []byte, key string) (interface{}, bool) {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := m[key]
	return v, ok
}
