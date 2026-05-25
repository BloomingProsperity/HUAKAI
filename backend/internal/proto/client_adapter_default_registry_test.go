package proto

import (
	"testing"
)

func TestDefaultClientAdapterRegistry_AllThreeRegistered(t *testing.T) {
	reg := DefaultClientAdapterRegistry()
	if reg == nil {
		t.Fatal("DefaultClientAdapterRegistry returned nil")
	}
	expected := []ClientProtocol{
		ClientProtocolAnthropicMessages,
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
		path    string
		want    ClientProtocol
		wantOK  bool
	}{
		{"/v1/chat/completions", ClientProtocolOpenAIChat, true},
		{"/v1/responses", ClientProtocolOpenAIResponses, true},
		{"/v1/native/openai/responses", ClientProtocolOpenAIResponses, true},
		{"/v1/messages", ClientProtocolAnthropicMessages, true},
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
