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
)

// perModelRegistry is a registry fake that resolves only the names in entries;
// any other name returns registry.ErrUnknownModel (the real 404 trigger). It
// counts ResolveModel calls so a test can prove the no-suffix / resolved-first
// path makes no extra lookups. Capabilities carry through so the reasoning gate
// is exercised.
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

// REGRESSION (S1 false-strip, the killer): a real shipped model "yi-medium"
// (pricing seed migration 0131) routed through the ingress hook must keep
// routing/pricing seeing "yi-medium" — never "yi". Because the FULL name
// resolves on the first ResolveModel, the effort-suffix path is never entered
// and exactly ONE registry call is made. Mutation: pure-string strip at ingress
// -> routing sees "yi" -> here ex.req.Model becomes "yi" -> RED.
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

// REGRESSION: a no-suffix unknown model still 404s with exactly ONE registry
// call — the suffix probe never runs when there is no effort token. Mutation:
// run the suffix probe unconditionally -> 2 calls -> RED.
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

// REGRESSION: a genuine "<reasoning-model>-<effort>" whose full name does NOT
// resolve but whose base DOES (reasoning-capable) is normalized: routing sees
// the BASE and the body gains reasoning_effort. Mutation: skip ingress
// normalization -> full name unresolved -> prepareRoute 404s -> RED.
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

// REGRESSION (S2 cross-protocol): "claude-...-high" arriving at the OPENAI-CHAT
// ingress must yield a reasoning_effort the openai-chat parser reads — NOT a
// thinking object it drops. Param chosen by INGRESS, not model name. Mutation:
// classify by model name (claude -> thinking) -> thinking object on an
// openai-chat body -> dropped downstream -> here reasoning_effort absent -> RED.
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

// REGRESSION: when the base is NOT reasoning-capable, the suffix is left in
// place and the request 404s exactly as it would without the feature (no
// behavior change). Mutation: strip regardless of reasoning capability -> base
// routes -> prepareRoute succeeds -> RED.
func TestPrepareRoute_BaseNotReasoningNotStripped(t *testing.T) {
	nonReasoning := reasoningResolved("foo")
	nonReasoning.Capabilities = []string{"text", "tools"} // no reasoning/thinking
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

// REGRESSION (clamp through the real hook): claude-opus-4-8-high with
// max_tokens=2000 via anthropic ingress yields a thinking budget clamped to
// 2000, not 24576. Mutation: drop the clamp -> 24576 -> RED.
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
