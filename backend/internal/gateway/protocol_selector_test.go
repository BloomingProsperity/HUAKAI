package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
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

	anthropic, err := r.For("anthropic_messages")
	if err != nil {
		t.Fatalf("For(anthropic_messages) error = %v", err)
	}
	anthropicAdapter, ok := anthropic.(*proto.AnthropicAdapter)
	if !ok {
		t.Fatalf("anthropic_messages adapter type = %T, want *proto.AnthropicAdapter", anthropic)
	}
	if anthropicAdapter.CarryForwardSignatureDelta {
		t.Fatal("anthropic_messages CarryForwardSignatureDelta = true, want false")
	}

	for _, family := range []string{
		"openai_chat", "openai_responses", "openai_codex",
		// 6 家 OpenAI 兼容直通，均注册为 OpenAIAdapter
		"deepseek_chat", "mistral_chat", "groqcloud_chat",
		"together_chat", "perplexity_chat", "fireworks_chat",
		// session 反转占位 SSE 解析（实测前先复用 OpenAIAdapter）
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
		if _, ok := got.(*proto.OpenAIAdapter); !ok {
			t.Fatalf("For(%s) adapter type = %T, want *proto.OpenAIAdapter", family, got)
		}
	}

	for _, family := range []string{"gemini_messages", "gemini_advanced_session"} {
		got, err := r.For(family)
		if err != nil {
			t.Fatalf("For(%s) error = %v", family, err)
		}
		if _, ok := got.(*proto.GeminiAdapter); !ok {
			t.Fatalf("For(%s) adapter type = %T, want *proto.GeminiAdapter", family, got)
		}
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
