package thinkingnorm

import (
	"bytes"
	"encoding/json"
	"testing"
)

// fakeResolver answers Resolve from two maps: resolves[name] -> known, and
// reasoning[name] -> reasoning-capable. It also counts calls so a test can prove
// the no-suffix / cheap-pre-check path makes ZERO resolver calls.
type fakeResolver struct {
	resolves  map[string]bool
	reasoning map[string]bool
	calls     int
}

func (f *fakeResolver) Resolve(name string) (bool, bool) {
	f.calls++
	return f.resolves[name], f.reasoning[name]
}

func bodyField(t *testing.T, body []byte, key string) (interface{}, bool) {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := m[key]
	return v, ok
}

// REGRESSION: a real shipped model whose name ends in "-medium" (yi-medium,
// pricing seed migration 0131) must NEVER be stripped to "yi"; because the FULL
// name resolves, the caller never even enters the strip path. Here we model that
// the caller only calls NormalizeEffortSuffix after the full name failed to
// resolve, AND additionally prove that if it WERE called, an unresolved base
// ("yi") leaves it unchanged. Mutation: replace registry-aware strip with a
// pure string strip -> "yi-medium" -> "yi" -> RED here.
func TestNormalizeEffortSuffix_YiMediumNotStrippedWhenBaseUnresolved(t *testing.T) {
	body := []byte(`{"model":"yi-medium","messages":[]}`)
	resolver := &fakeResolver{
		resolves:  map[string]bool{}, // base "yi" does NOT resolve
		reasoning: map[string]bool{},
	}
	out, gotBody := NormalizeEffortSuffix("yi-medium", body, IngressOpenAIChat, resolver)
	if out.Normalized {
		t.Fatalf("yi-medium must NOT be normalized when base 'yi' does not resolve; got %+v", out)
	}
	if out.BaseModel != "yi-medium" {
		t.Fatalf("base model must stay yi-medium; got %q", out.BaseModel)
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("body must be byte-identical for unstripped yi-medium; got %s", gotBody)
	}
}

// REGRESSION: even if a base "yi" happened to resolve but is NOT reasoning-
// capable, the suffix must not be stripped (a non-reasoning model can't take an
// effort param). Mutation: drop the reasoning-capable gate -> RED.
func TestNormalizeEffortSuffix_BaseResolvesButNotReasoning(t *testing.T) {
	body := []byte(`{"model":"yi-medium","messages":[]}`)
	resolver := &fakeResolver{
		resolves:  map[string]bool{"yi": true},
		reasoning: map[string]bool{}, // resolves but NOT reasoning-capable
	}
	out, gotBody := NormalizeEffortSuffix("yi-medium", body, IngressOpenAIChat, resolver)
	if out.Normalized {
		t.Fatalf("must NOT normalize when base is not reasoning-capable; got %+v", out)
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("body must be byte-identical; got %s", gotBody)
	}
}

// REGRESSION: a genuine "<reasoning-model>-<effort>" — base resolves AND is
// reasoning-capable — is stripped and the effort emitted. Mutation: skip the
// strip -> BaseModel stays suffixed -> RED.
func TestNormalizeEffortSuffix_GptThinkingHighStrippedAndEffort(t *testing.T) {
	body := []byte(`{"model":"gpt-5-thinking-high","messages":[]}`)
	resolver := &fakeResolver{
		resolves:  map[string]bool{"gpt-5-thinking": true},
		reasoning: map[string]bool{"gpt-5-thinking": true},
	}
	out, gotBody := NormalizeEffortSuffix("gpt-5-thinking-high", body, IngressOpenAIChat, resolver)
	if !out.Normalized || out.BaseModel != "gpt-5-thinking" || out.Level != "high" {
		t.Fatalf("want normalized base=gpt-5-thinking level=high; got %+v", out)
	}
	v, ok := bodyField(t, gotBody, "reasoning_effort")
	if !ok || v != "high" {
		t.Fatalf("openai-chat ingress must emit reasoning_effort=high; got %v ok=%v", v, ok)
	}
}

// REGRESSION (S2 cross-protocol effort loss): "claude-...-high" arriving at the
// OPENAI-CHAT ingress must emit reasoning_effort (which the openai-chat parser
// reads), NOT a top-level thinking object (which that parser drops). The emitted
// param is keyed off the INGRESS, not the model-name family. Mutation: key the
// param off the model name (claude -> thinking) -> a thinking object on an
// openai-chat body -> RED here.
func TestNormalizeEffortSuffix_ClaudeViaOpenAIChatIngressEmitsReasoningEffort(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-8-high","messages":[]}`)
	resolver := &fakeResolver{
		resolves:  map[string]bool{"claude-opus-4-8": true},
		reasoning: map[string]bool{"claude-opus-4-8": true},
	}
	out, gotBody := NormalizeEffortSuffix("claude-opus-4-8-high", body, IngressOpenAIChat, resolver)
	if !out.Normalized || out.BaseModel != "claude-opus-4-8" {
		t.Fatalf("want stripped to claude-opus-4-8; got %+v", out)
	}
	if v, ok := bodyField(t, gotBody, "reasoning_effort"); !ok || v != "high" {
		t.Fatalf("openai-chat ingress must emit reasoning_effort=high even for claude model; got %v ok=%v", v, ok)
	}
	if _, ok := bodyField(t, gotBody, "thinking"); ok {
		t.Fatalf("openai-chat ingress must NOT emit a thinking object (parser drops it)")
	}
}

// Symmetric: "gpt-...-high" arriving at the ANTHROPIC ingress emits a top-level
// thinking object (which the anthropic parser reads), not reasoning_effort.
func TestNormalizeEffortSuffix_GptViaAnthropicIngressEmitsThinking(t *testing.T) {
	body := []byte(`{"model":"gpt-5-thinking-high","messages":[]}`)
	resolver := &fakeResolver{
		resolves:  map[string]bool{"gpt-5-thinking": true},
		reasoning: map[string]bool{"gpt-5-thinking": true},
	}
	out, gotBody := NormalizeEffortSuffix("gpt-5-thinking-high", body, IngressAnthropic, resolver)
	if !out.Normalized {
		t.Fatalf("want normalized; got %+v", out)
	}
	v, ok := bodyField(t, gotBody, "thinking")
	if !ok {
		t.Fatalf("anthropic ingress must emit a thinking object")
	}
	obj := v.(map[string]interface{})
	if obj["type"] != "enabled" {
		t.Fatalf("thinking.type must be enabled; got %v", obj["type"])
	}
	if _, ok := bodyField(t, gotBody, "reasoning_effort"); ok {
		t.Fatalf("anthropic ingress must NOT emit reasoning_effort (parser drops it)")
	}
}

// REGRESSION: anthropic thinking budget is clamped DOWN under the request's own
// max output budget. high -> 24576, but max_tokens=2000 clamps it to 2000.
// Mutation: drop the clamp -> 24576 -> RED.
func TestNormalizeEffortSuffix_AnthropicBudgetClampedUnderMax(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-8-high","max_tokens":2000,"messages":[]}`)
	resolver := &fakeResolver{
		resolves:  map[string]bool{"claude-opus-4-8": true},
		reasoning: map[string]bool{"claude-opus-4-8": true},
	}
	_, gotBody := NormalizeEffortSuffix("claude-opus-4-8-high", body, IngressAnthropic, resolver)
	v, _ := bodyField(t, gotBody, "thinking")
	obj := v.(map[string]interface{})
	if got := obj["budget_tokens"].(float64); got != 2000 {
		t.Fatalf("thinking budget must clamp to max_tokens 2000; got %v", got)
	}
}

// REGRESSION (zero-impact): a model with NO effort suffix leaves model + body
// byte-identical AND makes ZERO resolver calls. Mutation: call the resolver
// before the cheap pre-check -> calls>0 -> RED.
func TestNormalizeEffortSuffix_NoSuffixZeroImpactZeroResolverCalls(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","temperature":0.7,"messages":[{"role":"user","content":"hi"}]}`)
	resolver := &fakeResolver{resolves: map[string]bool{}, reasoning: map[string]bool{}}
	if HasEffortSuffix("gpt-4o") {
		t.Fatalf("gpt-4o must not register as having an effort suffix")
	}
	out, gotBody := NormalizeEffortSuffix("gpt-4o", body, IngressOpenAIChat, resolver)
	if out.Normalized {
		t.Fatalf("no-suffix model must not be normalized")
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("no-suffix body must be byte-identical; got %s", gotBody)
	}
	if resolver.calls != 0 {
		t.Fatalf("no-suffix path must make ZERO resolver calls; made %d", resolver.calls)
	}
}

// REGRESSION (precedence): a recognized suffix OVERWRITES an explicit body-level
// reasoning_effort. Self-proving: body says low, suffix says high; result must
// be high. Mutation: honor the body over the suffix -> low -> RED.
func TestNormalizeEffortSuffix_SuffixWinsOverExplicitBody(t *testing.T) {
	body := []byte(`{"model":"gpt-5-thinking-high","reasoning_effort":"low","messages":[]}`)
	resolver := &fakeResolver{
		resolves:  map[string]bool{"gpt-5-thinking": true},
		reasoning: map[string]bool{"gpt-5-thinking": true},
	}
	_, gotBody := NormalizeEffortSuffix("gpt-5-thinking-high", body, IngressOpenAIChat, resolver)
	if v, _ := bodyField(t, gotBody, "reasoning_effort"); v != "high" {
		t.Fatalf("suffix high must win over body low; got %v", v)
	}
}

// REGRESSION: "-none" disables reasoning. For openai-chat that removes
// reasoning_effort; for anthropic it disables thinking.
func TestNormalizeEffortSuffix_NoneDisables(t *testing.T) {
	bodyOA := []byte(`{"model":"gpt-5-thinking-none","reasoning_effort":"high","messages":[]}`)
	resolver := &fakeResolver{
		resolves:  map[string]bool{"gpt-5-thinking": true},
		reasoning: map[string]bool{"gpt-5-thinking": true},
	}
	_, gotOA := NormalizeEffortSuffix("gpt-5-thinking-none", bodyOA, IngressOpenAIChat, resolver)
	if _, ok := bodyField(t, gotOA, "reasoning_effort"); ok {
		t.Fatalf("-none must remove reasoning_effort for openai-chat ingress")
	}

	bodyA := []byte(`{"model":"claude-opus-4-8-none","messages":[]}`)
	resolver2 := &fakeResolver{
		resolves:  map[string]bool{"claude-opus-4-8": true},
		reasoning: map[string]bool{"claude-opus-4-8": true},
	}
	_, gotA := NormalizeEffortSuffix("claude-opus-4-8-none", bodyA, IngressAnthropic, resolver2)
	v, _ := bodyField(t, gotA, "thinking")
	obj := v.(map[string]interface{})
	if obj["type"] != "disabled" {
		t.Fatalf("-none must disable anthropic thinking; got %v", obj["type"])
	}
}

// REGRESSION: a foreign / real suffix on a name whose base is not a known
// reasoning model is NOT false-stripped. E.g. gemini-2.5-flash (no token),
// grok-4-max (token -max, but base grok-4 unresolved here). Mutation: pure
// string strip -> grok-4-max -> grok-4 -> RED.
func TestNormalizeEffortSuffix_ForeignSuffixNotFalseStripped(t *testing.T) {
	resolver := &fakeResolver{resolves: map[string]bool{}, reasoning: map[string]bool{}}
	for _, name := range []string{
		"gemini-2.5-flash",  // no effort token at all
		"gpt-4o-latest",     // no effort token
		"deepseek-chat-max", // -max token but base unresolved
		"grok-4-max",        // -max token but base unresolved
		"claude-3-5-sonnet", // no effort token
	} {
		body := []byte(`{"model":"` + name + `","messages":[]}`)
		out, gotBody := NormalizeEffortSuffix(name, body, IngressOpenAIChat, resolver)
		if out.Normalized {
			t.Fatalf("%q must NOT be normalized", name)
		}
		if out.BaseModel != name {
			t.Fatalf("%q base must stay unchanged; got %q", name, out.BaseModel)
		}
		if !bytes.Equal(gotBody, body) {
			t.Fatalf("%q body must be byte-identical", name)
		}
	}
}

// REGRESSION: an unmodeled ingress (e.g. gemini) never strips a suffix.
func TestNormalizeEffortSuffix_OtherIngressLeavesUnchanged(t *testing.T) {
	body := []byte(`{"model":"gpt-5-thinking-high","messages":[]}`)
	resolver := &fakeResolver{
		resolves:  map[string]bool{"gpt-5-thinking": true},
		reasoning: map[string]bool{"gpt-5-thinking": true},
	}
	out, gotBody := NormalizeEffortSuffix("gpt-5-thinking-high", body, IngressOther, resolver)
	if out.Normalized {
		t.Fatalf("unmodeled ingress must not normalize")
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("unmodeled ingress must leave body byte-identical")
	}
	if resolver.calls != 0 {
		t.Fatalf("unmodeled ingress must make ZERO resolver calls; made %d", resolver.calls)
	}
}

// REGRESSION: a non-object body still strips the base model (so routing/pricing
// see the base) but leaves the body untouched — never crashes.
func TestNormalizeEffortSuffix_NonObjectBodyStripsModelOnly(t *testing.T) {
	body := []byte(`"not-an-object"`)
	resolver := &fakeResolver{
		resolves:  map[string]bool{"gpt-5-thinking": true},
		reasoning: map[string]bool{"gpt-5-thinking": true},
	}
	out, gotBody := NormalizeEffortSuffix("gpt-5-thinking-high", body, IngressOpenAIChat, resolver)
	if !out.Normalized || out.BaseModel != "gpt-5-thinking" {
		t.Fatalf("non-object body must still strip the base; got %+v", out)
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("non-object body must be left untouched; got %s", gotBody)
	}
}

// level->budget table pin so a future edit to a budget value is a conscious change.
func TestNormalizeEffortSuffix_LevelBudgetTablePin(t *testing.T) {
	want := map[effortLevel]int{
		effortMinimal: 512,
		effortLow:     1024,
		effortMedium:  8192,
		effortHigh:    24576,
		effortMax:     32768,
		effortNone:    0,
	}
	for lvl, w := range want {
		if got := levelToBudget[lvl]; got != w {
			t.Fatalf("levelToBudget[%s]=%d want %d", lvl, got, w)
		}
	}
}
