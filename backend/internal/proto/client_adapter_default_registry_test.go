package proto

import (
	"testing"
)

func TestDefaultClientAdapterRegistry_AllRegistered(t *testing.T) {
	reg := DefaultClientAdapterRegistry()
	if reg == nil {
		t.Fatal("DefaultClientAdapterRegistry returned nil")
	}
	expected := []ClientProtocol{
		ClientProtocolAnthropicMessages,
		ClientProtocolGemini,
		ClientProtocolOpenAIChat,
		ClientProtocolOpenAIResponses,
	}
	for _, p := range expected {
		a, ok := reg.Lookup(p)
		if !ok {
			t.Errorf("expected %s registered, got missing", p)
			continue
		}
		if a == nil {
			t.Errorf("registered adapter for %s is nil", p)
		}
	}
}

func TestDefaultClientAdapterRegistry_Singleton(t *testing.T) {
	r1 := DefaultClientAdapterRegistry()
	r2 := DefaultClientAdapterRegistry()
	if r1 != r2 {
		t.Error("DefaultClientAdapterRegistry must return same singleton")
	}
}

func TestDefaultClientAdapterRegistry_ProtocolsSorted(t *testing.T) {
	reg := DefaultClientAdapterRegistry()
	got := reg.Protocols()
	want := []ClientProtocol{
		ClientProtocolAnthropicMessages,
		ClientProtocolGemini,
		ClientProtocolOpenAIChat,
		ClientProtocolOpenAIResponses,
	}
	if len(got) != len(want) {
		t.Fatalf("len got=%d want=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("Protocols()[%d] got=%s want=%s", i, got[i], want[i])
		}
	}
}

func TestDefaultClientAdapterRegistry_AdaptersImplementInterface(t *testing.T) {
	// 编译期 _ = ClientAdapter(&XClient{}) 已经覆盖；本测试只是做 runtime 二次确认。
	reg := DefaultClientAdapterRegistry()
	for _, p := range reg.Protocols() {
		a, _ := reg.Lookup(p)
		var _ ClientAdapter = a // 类型断言
	}
}

func TestClientProtocolByIngressPath(t *testing.T) {
	cases := []struct {
		path   string
		want   ClientProtocol
		wantOK bool
	}{
		{"/v1/chat/completions", ClientProtocolOpenAIChat, true},
		{"/v1/responses", ClientProtocolOpenAIResponses, true},
		{"/v1/native/openai/responses", ClientProtocolOpenAIResponses, true},
		{"/backend-api/codex/responses", ClientProtocolOpenAIResponses, true},
		{"/v1/messages", ClientProtocolAnthropicMessages, true},
		{"/v1beta/models", ClientProtocolGemini, true},
		{"/v1beta/models/gemini-pro:generateContent", ClientProtocolGemini, true},
		{"/v1/unknown", "", false},
		{"", "", false},
		{"/v1/completions", "", false},
	}
	for _, tc := range cases {
		got, ok := ClientProtocolByIngressPath(tc.path)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("path=%q got=(%s,%v) want=(%s,%v)", tc.path, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestCodexResponsesSamePipelineAsV1Responses(t *testing.T) {
	// 变异点：把 /backend-api/codex/responses 映射到 openai_chat，或让它落入
	// 默认的 unknown 路径；这样 Codex CLI 会绕过 Responses adapter，或返回 404，
	// 而不是复用 /v1/responses 的行为。
	got, ok := ClientProtocolByIngressPath("/backend-api/codex/responses")
	if !ok {
		t.Fatal("Codex Responses ingress path must resolve instead of falling through to 404")
	}
	if got != ClientProtocolOpenAIResponses {
		t.Fatalf("Codex Responses protocol=%s want %s", got, ClientProtocolOpenAIResponses)
	}
}
