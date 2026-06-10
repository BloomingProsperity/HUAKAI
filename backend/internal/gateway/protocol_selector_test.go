package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/bedrock"
	protodify "github.com/BloomingProsperity/HUAKAI/internal/proto/dify"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/gemini"
	protoollama "github.com/BloomingProsperity/HUAKAI/internal/proto/ollama"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/openai"
)

type stubUpstreamAdapter struct {
	name string
}

func (s *stubUpstreamAdapter) CanonicalToProviderRequest(ctx context.Context, canonical *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	return nil, nil, nil
}

func (s *stubUpstreamAdapter) ProviderResponseToCanonical(ctx context.Context, raw []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	return nil, nil, nil
}

func (s *stubUpstreamAdapter) ProviderEventToCanonicalEvents(ctx context.Context, providerEvt any, state any) ([]any, []proto.ProtocolLossEntry, error) {
	return nil, nil, nil
}

func (s *stubUpstreamAdapter) FinalizeUpstreamStream(ctx context.Context, state any) ([]any, error) {
	return nil, nil
}

func TestStaticProtocolAdapterRegistryRegisterAndFor(t *testing.T) {
	r := NewStaticProtocolAdapterRegistry()
	want := &stubUpstreamAdapter{name: "primary"}

	if err := r.Register("demo_family", want); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, err := r.For("demo_family")
	if err != nil {
		t.Fatalf("For() error = %v", err)
	}
	if got != want {
		t.Fatalf("For() = %#v, want %#v", got, want)
	}
}

func TestStaticProtocolAdapterRegistryMustRegisterPanics(t *testing.T) {
	r := NewStaticProtocolAdapterRegistry()
	r.MustRegister("demo_family", &stubUpstreamAdapter{name: "first"})

	assertPanic(t, func() {
		r.MustRegister("demo_family", &stubUpstreamAdapter{name: "second"})
	})
}

func TestStaticProtocolAdapterRegistryUnknownFamily(t *testing.T) {
	r := NewStaticProtocolAdapterRegistry()

	got, err := r.For("missing_family")
	if err == nil {
		t.Fatal("For() error = nil, want ErrUnknownProtocolFamily")
	}
	if got != nil {
		t.Fatalf("For() adapter = %#v, want nil", got)
	}
	if !errors.Is(err, ErrUnknownProtocolFamily) {
		t.Fatalf("For() error = %v, want errors.Is ErrUnknownProtocolFamily", err)
	}
}

func TestStaticProtocolAdapterRegistryNilReceiver(t *testing.T) {
	var r *StaticProtocolAdapterRegistry

	got, err := r.For("demo_family")
	if err == nil {
		t.Fatal("For() error = nil, want nil receiver error")
	}
	if got != nil {
		t.Fatalf("For() adapter = %#v, want nil", got)
	}
	if !errors.Is(err, ErrUnknownProtocolFamily) {
		t.Fatalf("For() error = %v, want errors.Is ErrUnknownProtocolFamily", err)
	}

	if err := r.Register("demo_family", &stubUpstreamAdapter{}); err == nil {
		t.Fatal("Register() error = nil, want nil receiver error")
	}
	assertPanic(t, func() {
		r.MustRegister("demo_family", &stubUpstreamAdapter{})
	})
}

func TestStaticProtocolAdapterRegistryDuplicate(t *testing.T) {
	r := NewStaticProtocolAdapterRegistry()
	first := &stubUpstreamAdapter{name: "first"}
	second := &stubUpstreamAdapter{name: "second"}

	if err := r.Register("demo_family", first); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := r.Register("demo_family", second); err == nil {
		t.Fatal("second Register() error = nil, want duplicate error")
	}

	got, err := r.For("demo_family")
	if err != nil {
		t.Fatalf("For() error = %v", err)
	}
	if got != first {
		t.Fatalf("For() = %#v, want original adapter %#v", got, first)
	}
}

func TestStaticProtocolAdapterRegistryRejectsNilAdapter(t *testing.T) {
	r := NewStaticProtocolAdapterRegistry()
	if err := r.Register("nil_family", nil); err == nil {
		t.Fatal("Register(nil) error = nil, want error")
	}

	var typedNil *stubUpstreamAdapter
	if err := r.Register("typed_nil_family", typedNil); err == nil {
		t.Fatal("Register(typed nil) error = nil, want error")
	}
}

func TestErrUnknownProtocolFamilyErrorsIs(t *testing.T) {
	err := errors.Join(errors.New("outer"), ErrUnknownProtocolFamily)
	if !errors.Is(err, ErrUnknownProtocolFamily) {
		t.Fatalf("errors.Is(%v, ErrUnknownProtocolFamily) = false, want true", err)
	}
}

func TestBuildDefaultProtocolAdapterRegistry(t *testing.T) {
	r := BuildDefaultProtocolAdapterRegistry()
	if r == nil {
		t.Fatal("BuildDefaultProtocolAdapterRegistry() = nil")
	}

	anthropicAdapterRaw, err := r.For("anthropic_messages")
	if err != nil {
		t.Fatalf("For(anthropic_messages) error = %v", err)
	}
	anthropicAdapter, ok := anthropicAdapterRaw.(*anthropic.Adapter)
	if !ok {
		t.Fatalf("anthropic_messages adapter type = %T, want *anthropic.Adapter", anthropicAdapterRaw)
	}
	if anthropicAdapter.CarryForwardSignatureDelta {
		t.Fatal("anthropic_messages CarryForwardSignatureDelta = true, want false")
	}

	for _, family := range []string{
		"openai_chat", "openai_responses", "openai_codex",
		// OpenRouter / Grok 也走 OpenAI 兼容 SSE
		"openrouter_chat", "grok_chat",
		// 6 家 OpenAI 兼容直通，均注册为 openai.Adapter
		"deepseek_chat", "mistral_chat", "groqcloud_chat",
		"together_chat", "perplexity_chat", "fireworks_chat",
		// session 反转占位 SSE 解析（实测前先复用 openai.Adapter）
		"copilot_session", "cursor_session",
		"antigravity_session", "kiro_session", "windsurf_session",
	} {
		got, err := r.For(family)
		if err != nil {
			t.Fatalf("For(%s) error = %v", family, err)
		}
		if got == nil {
			t.Fatalf("For(%s) adapter = nil, want non-nil", family)
		}
		if _, ok := got.(*openai.Adapter); !ok {
			t.Fatalf("For(%s) adapter type = %T, want *openai.Adapter", family, got)
		}
	}

	for _, family := range []string{"gemini_messages", "gemini_advanced_session"} {
		got, err := r.For(family)
		if err != nil {
			t.Fatalf("For(%s) error = %v", family, err)
		}
		if _, ok := got.(*gemini.Adapter); !ok {
			t.Fatalf("For(%s) adapter type = %T, want *gemini.Adapter", family, got)
		}
	}

	// bedrock_invoke 走专用 bedrock.EventStreamAdapter（A4 atomic 接入；
	// AWS Binary EventStream 与 SSE 不兼容，A2+A3 提供 binary scanner）。
	bedrockAdapter, err := r.For("bedrock_invoke")
	if err != nil {
		t.Errorf("bedrock_invoke 应已注册（A5+A6 atomic），err=%v", err)
	}
	if _, ok := bedrockAdapter.(*bedrock.EventStreamAdapter); !ok {
		t.Errorf("bedrock_invoke adapter 类型=%T 期望 *bedrock.EventStreamAdapter", bedrockAdapter)
	}

	// dify_chat 走专用 dify.Adapter(事件键 SSE,与 OpenAI 兼容形态不同)。
	// 抓的回归:漏注册(流式/非流式取 adapter 即失败)或误注册成 openai.Adapter
	// (Dify 帧解不出 choices,整流零输出)。
	difyAdapter, err := r.For("dify_chat")
	if err != nil {
		t.Errorf("dify_chat 应已注册,err=%v", err)
	}
	if difyAdapter == nil {
		t.Error("For(dify_chat) adapter = nil")
	}
	if _, ok := difyAdapter.(*protodify.Adapter); !ok {
		t.Errorf("dify_chat adapter 类型=%T 期望 *dify.Adapter", difyAdapter)
	}

	// ollama_native 走专用 ollama.Adapter(NDJSON 帧,无 data:/[DONE])。
	// 抓的回归:漏注册(流式/非流式取 adapter 即失败)或误注册成 openai.Adapter
	// (Ollama 帧解不出 choices,整流零输出)。
	ollamaAdapter, err := r.For("ollama_native")
	if err != nil {
		t.Errorf("ollama_native 应已注册,err=%v", err)
	}
	if ollamaAdapter == nil {
		t.Error("For(ollama_native) adapter = nil")
	}
	if _, ok := ollamaAdapter.(*protoollama.Adapter); !ok {
		t.Errorf("ollama_native adapter 类型=%T 期望 *ollama.Adapter", ollamaAdapter)
	}
}

func assertPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("panic = nil, want panic")
		}
	}()
	fn()
}

// TestOpenAICompatFamiliesResolveInbound 守卫出站/入站注册表对称:每个 OpenAI
// Chat Completions 兼容上游族都必须在入站协议适配器注册表登记。出站
// registrydefault 注册这些族用于 BuildRequest;非流式默认走 HCSF
// (hcsfDispatchEnabled 默认开),DispatchHCSF 用本注册表 adapter 解析上游响应。
// 漏登记 = 该 provider 非流式请求直接 "取 upstream adapter 失败"(本轮修复的
// kimi/qwen/glm/yi/baichuan/doubao/ernie/step/hunyuan/minimax 整类漏接)。
// Mutation guard: 删 protocol_selector.go 任一 MustRegister 行 → 对应族子断言红。
func TestOpenAICompatFamiliesResolveInbound(t *testing.T) {
	reg := BuildDefaultProtocolAdapterRegistry()
	families := []string{
		"deepseek_chat", "mistral_chat", "groqcloud_chat", "together_chat",
		"perplexity_chat", "fireworks_chat", "kimi_chat", "qwen_chat",
		"glm_chat", "yi_chat", "baichuan_chat", "doubao_chat", "ernie_chat",
		"step_chat", "hunyuan_chat", "minimax_chat", "cohere_chat", "ollama_chat",
		"grok_chat", "openrouter_chat",
	}
	for _, f := range families {
		if _, err := reg.For(f); err != nil {
			t.Errorf("OpenAI 兼容族 %q 未在入站协议适配器注册表登记: %v", f, err)
		}
	}
}
