// stream_scanner_test.go — A1 atomic 单测：StreamScanner 抽象 + 注册表 +
// SSEStreamScanner 包装等价性。
package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestStaticStreamScannerRegistry_RegisterAndFor(t *testing.T) {
	r := NewStaticStreamScannerRegistry()
	want := &SSEStreamScanner{}
	if err := r.Register("demo", want); err != nil {
		t.Fatalf("Register err=%v", err)
	}
	got, err := r.For("demo")
	if err != nil {
		t.Fatalf("For err=%v", err)
	}
	if got != want {
		t.Errorf("For 返回的不是注册的 scanner")
	}
}

func TestStaticStreamScannerRegistry_MissReturnsErrUnknown(t *testing.T) {
	r := NewStaticStreamScannerRegistry()
	_, err := r.For("nonexistent")
	if !errors.Is(err, ErrUnknownStreamScanner) {
		t.Errorf("For 未知 family err=%v want ErrUnknownStreamScanner", err)
	}
}

func TestStaticStreamScannerRegistry_DuplicateRejected(t *testing.T) {
	r := NewStaticStreamScannerRegistry()
	a := &SSEStreamScanner{}
	b := &SSEStreamScanner{}
	if err := r.Register("dup", a); err != nil {
		t.Fatal(err)
	}
	err := r.Register("dup", b)
	if err == nil {
		t.Error("重复 Register 应报错")
	}
	if !strings.Contains(err.Error(), "重复") {
		t.Errorf("err=%v want 含'重复'", err)
	}
	got, _ := r.For("dup")
	if got != a {
		t.Error("重复注册不应覆盖已存在的 scanner")
	}
}

func TestStaticStreamScannerRegistry_MustRegisterPanicsOnDuplicate(t *testing.T) {
	r := NewStaticStreamScannerRegistry()
	r.MustRegister("once", &SSEStreamScanner{})
	defer func() {
		if recover() == nil {
			t.Error("MustRegister 重复应 panic")
		}
	}()
	r.MustRegister("once", &SSEStreamScanner{})
}

func TestStaticStreamScannerRegistry_NilReceiver(t *testing.T) {
	var r *StaticStreamScannerRegistry
	_, err := r.For("any")
	if !errors.Is(err, ErrUnknownStreamScanner) {
		t.Errorf("nil receiver For err=%v want ErrUnknownStreamScanner", err)
	}
	if err := r.Register("x", &SSEStreamScanner{}); err == nil {
		t.Error("nil receiver Register 应报错")
	}
}

func TestStaticStreamScannerRegistry_RejectsNilScanner(t *testing.T) {
	r := NewStaticStreamScannerRegistry()
	if err := r.Register("nilfam", nil); err == nil {
		t.Error("Register(nil) 应报错")
	}
	var typedNil *SSEStreamScanner
	if err := r.Register("typednil", typedNil); err == nil {
		t.Error("Register(typed nil) 应报错")
	}
}

func TestStaticStreamScannerRegistry_RejectsEmptyFamily(t *testing.T) {
	r := NewStaticStreamScannerRegistry()
	if err := r.Register("", &SSEStreamScanner{}); err == nil {
		t.Error("Register 空 family 应报错")
	}
}

// TestBuildDefaultStreamScannerRegistry 验证默认注册表覆盖所有当前
// 注册了 protocol adapter 的 family（除 bedrock_invoke）。
func TestBuildDefaultStreamScannerRegistry(t *testing.T) {
	r := BuildDefaultStreamScannerRegistry()
	if r == nil {
		t.Fatal("BuildDefaultStreamScannerRegistry() = nil")
	}

	for _, family := range []string{
		"anthropic_messages", "openai_chat", "openai_responses", "openai_codex",
		"gemini_messages", "openrouter_chat", "grok_chat",
		"deepseek_chat", "mistral_chat", "groqcloud_chat",
		"together_chat", "perplexity_chat", "fireworks_chat",
		"copilot_session", "cursor_session", "gemini_advanced_session",
		"antigravity_session", "kiro_session", "windsurf_session",
	} {
		got, err := r.For(family)
		if err != nil {
			t.Errorf("For(%q) err=%v want non-nil scanner", family, err)
			continue
		}
		if _, ok := got.(*SSEStreamScanner); !ok {
			t.Errorf("For(%q) type=%T want *SSEStreamScanner", family, got)
		}
	}

	// bedrock_invoke 故意未注册（A2+A3 才加）
	if _, err := r.For("bedrock_invoke"); !errors.Is(err, ErrUnknownStreamScanner) {
		t.Errorf("bedrock_invoke 应明确未注册，err=%v", err)
	}
}

// TestSSEStreamScanner_DelegatesToScanSSEEvents 验证 wrapper 与原函数行为
// 等价：构造一个 SSE byte stream，两条路径输出 event 序列必须一致。
func TestSSEStreamScanner_DelegatesToScanSSEEvents(t *testing.T) {
	const wire = "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	collect := func(seq func(yield func(SSEEvent, error) bool)) []SSEEvent {
		var out []SSEEvent
		seq(func(evt SSEEvent, err error) bool {
			if err != nil {
				return false
			}
			out = append(out, evt)
			return true
		})
		return out
	}

	wrapper := &SSEStreamScanner{}
	wrapped := collect(wrapper.Scan(context.Background(), strings.NewReader(wire), 0))
	direct := collect(ScanSSEEvents(context.Background(), strings.NewReader(wire), 0))

	if len(wrapped) != len(direct) {
		t.Fatalf("event count wrapped=%d direct=%d", len(wrapped), len(direct))
	}
	for i := range wrapped {
		if wrapped[i].Type != direct[i].Type {
			t.Errorf("[%d] Type wrapped=%q direct=%q", i, wrapped[i].Type, direct[i].Type)
		}
		if string(wrapped[i].Data) != string(direct[i].Data) {
			t.Errorf("[%d] Data wrapped=%q direct=%q", i, wrapped[i].Data, direct[i].Data)
		}
	}
}
