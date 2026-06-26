package thinkingnorm

import (
	"bytes"
	"encoding/json"
	"testing"
)

// fakeResolver 用两个 map 来回答 Resolve:resolves[name] -> 已知,以及
// reasoning[name] -> 具备推理能力。它还对调用次数计数,以便测试能证明
// 无后缀 / 廉价预检查的路径会执行零次 resolver 调用。
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

// 回归:一个真实上线、名称以 "-medium" 结尾的模型(yi-medium,pricing seed
// 迁移 0131)绝不能被剥除成 "yi";因为完整名能解析,调用方根本不会进入剥除
// 路径。这里我们模拟调用方只在完整名解析失败之后才调用
// NormalizeEffortSuffix,并额外证明:即便它真的被调用,一个无法解析的 base
//("yi")也会使其保持不变。变异:把感知注册表的剥除替换为纯字符串剥除 ->
// "yi-medium" -> "yi" -> 此处转红。
func TestNormalizeEffortSuffix_YiMediumNotStrippedWhenBaseUnresolved(t *testing.T) {
	body := []byte(`{"model":"yi-medium","messages":[]}`)
	resolver := &fakeResolver{
		resolves:  map[string]bool{}, // base "yi" 无法解析
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

// 回归:即便一个 base "yi" 恰好能解析,但不具备推理能力,后缀也不能被剥除
//(非推理模型无法接受 effort 参数)。变异:去掉 reasoning-capable 门控 -> 转红。
func TestNormalizeEffortSuffix_BaseResolvesButNotReasoning(t *testing.T) {
	body := []byte(`{"model":"yi-medium","messages":[]}`)
	resolver := &fakeResolver{
		resolves:  map[string]bool{"yi": true},
		reasoning: map[string]bool{}, // 能解析但不具备推理能力
	}
	out, gotBody := NormalizeEffortSuffix("yi-medium", body, IngressOpenAIChat, resolver)
	if out.Normalized {
		t.Fatalf("must NOT normalize when base is not reasoning-capable; got %+v", out)
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("body must be byte-identical; got %s", gotBody)
	}
}

// 回归:一个真正的 "<reasoning-model>-<effort>" —— base 能解析且具备推理
// 能力 —— 会被剥除并发出 effort。变异:跳过剥除 -> BaseModel 仍带后缀 -> 转红。
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

// 回归(S2 跨协议 effort 丢失):到达 OPENAI-CHAT 入口的 "claude-...-high"
// 必须发出 reasoning_effort(openai-chat 解析器会读取它),而不是顶层
// thinking 对象(该解析器会丢弃它)。所发出的参数取决于入口,而非模型名
// 族系。变异:让参数取决于模型名(claude -> thinking)-> openai-chat body
// 上出现 thinking 对象 -> 此处转红。
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

// 对称:到达 ANTHROPIC 入口的 "gpt-...-high" 发出顶层 thinking 对象
//(anthropic 解析器会读取它),而非 reasoning_effort。
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

// 回归:anthropic thinking 预算会被下钳到请求自身的 max output 预算之下。
// high -> 24576,但 max_tokens=2000 会把它钳到 2000。变异:去掉钳制 ->
// 24576 -> 转红。
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

// 回归(零影响):一个没有 effort 后缀的模型会让 model + body 逐字节相同,
// 且执行零次 resolver 调用。变异:在廉价预检查之前调用 resolver ->
// calls>0 -> 转红。
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

// 回归(优先级):一个已识别的后缀会覆盖显式的 body 级 reasoning_effort。
// 自证:body 写 low,后缀写 high;结果必须是 high。变异:让 body 优先于
// 后缀 -> low -> 转红。
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

// 回归:"-none" 禁用推理。对 openai-chat 而言会移除 reasoning_effort;
// 对 anthropic 而言会禁用 thinking。
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

// 回归:对于 base 不是已知推理模型的名称,外来 / 真实后缀不会被误剥除。
// 例如 gemini-2.5-flash(无 token)、grok-4-max(有 -max token,但此处
// base grok-4 无法解析)。变异:纯字符串剥除 -> grok-4-max -> grok-4 -> 转红。
func TestNormalizeEffortSuffix_ForeignSuffixNotFalseStripped(t *testing.T) {
	resolver := &fakeResolver{resolves: map[string]bool{}, reasoning: map[string]bool{}}
	for _, name := range []string{
		"gemini-2.5-flash",  // 完全没有 effort token
		"gpt-4o-latest",     // 没有 effort token
		"deepseek-chat-max", // 有 -max token,但 base 无法解析
		"grok-4-max",        // 有 -max token,但 base 无法解析
		"claude-3-5-sonnet", // 没有 effort token
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

// 回归:未建模的入口(例如 gemini)永远不会剥除后缀。
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

// 回归:非对象 body 仍会剥除 base 模型(让路由/定价看到 base),但保持
// body 不动 —— 永不崩溃。
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

// 钉住 level->budget 表,使将来对某个预算值的修改是一次有意识的变更。
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
