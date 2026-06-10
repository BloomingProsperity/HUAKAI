// stream_scanner_test.go — A1 atomic 单测：StreamScanner 抽象 + 注册表 +
// SSEStreamScanner 包装等价性。
package gateway

import (
	"context"
	"errors"
	"io"
	"net"
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

// TestBuildDefaultStreamScannerRegistry 验证默认注册表覆盖所有 protocol family。
// 19 个 SSE family 走 SSEStreamScanner；bedrock_invoke 走专用 binary 切帧器。
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
		"kimi_chat", "qwen_chat", "glm_chat", "yi_chat", "baichuan_chat",
		"doubao_chat", "ernie_chat", "step_chat", "hunyuan_chat",
		"minimax_chat", "cohere_chat", "ollama_chat",
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

	// bedrock_invoke 走专用 BedrockEventStreamScanner（A3 atomic 实现）
	bedrock, err := r.For("bedrock_invoke")
	if err != nil {
		t.Errorf("bedrock_invoke 应已注册（A5+A6 atomic），err=%v", err)
	}
	if _, ok := bedrock.(*BedrockEventStreamScanner); !ok {
		t.Errorf("bedrock_invoke scanner 类型=%T 期望 *BedrockEventStreamScanner", bedrock)
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

func TestScanSSEEventsClassifiesOverflowCancelAndNetworkReadErrorsDistinctly(t *testing.T) {
	overflowErr := lastScanError(ScanSSEEvents(context.Background(), strings.NewReader("data: "+strings.Repeat("X", 64)+"\n\n"), 16))
	if !errors.Is(overflowErr, ErrScannerOverflow) {
		t.Fatalf("oversize event err=%v want ErrScannerOverflow", overflowErr)
	}
	overflowClass, _ := classifyScanError(overflowErr)
	if overflowClass != ResponseEventTooLarge {
		t.Fatalf("overflow class=%q want %q", overflowClass, ResponseEventTooLarge)
	}

	cancelClass, _ := classifyScanError(context.Canceled)
	if cancelClass != OrchestratorCancel {
		t.Fatalf("context.Canceled class=%q want %q", cancelClass, OrchestratorCancel)
	}

	resetErr := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	scanErr := lastScanError(ScanSSEEvents(context.Background(), &failingSSEReader{
		chunk: []byte("data: ok\n\n"),
		err:   resetErr,
	}, 0))
	if scanErr == nil {
		t.Fatalf("network reader error was not propagated")
	}
	if errors.Is(scanErr, ErrScannerOverflow) {
		t.Fatalf("network reader error collapsed into overflow: %v", scanErr)
	}
	networkClass, _ := classifyScanError(scanErr)
	if networkClass != UpstreamError5xx {
		t.Fatalf("network reader class=%q want %q", networkClass, UpstreamError5xx)
	}
	if networkClass == ResponseEventTooLarge {
		t.Fatalf("network reader error must not classify as response_event_too_large")
	}

	classes := map[StreamEndClass]bool{
		overflowClass: true,
		cancelClass:   true,
		networkClass:  true,
	}
	if len(classes) != 3 {
		t.Fatalf("overflow/cancel/network classes must stay distinct; got overflow=%q cancel=%q network=%q", overflowClass, cancelClass, networkClass)
	}
}

type failingSSEReader struct {
	chunk []byte
	err   error
	sent  bool
}

func (r *failingSSEReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, r.chunk), nil
	}
	if r.err == nil {
		return 0, io.EOF
	}
	return 0, r.err
}

func lastScanError(seq func(yield func(SSEEvent, error) bool)) error {
	var last error
	seq(func(_ SSEEvent, err error) bool {
		if err != nil {
			last = err
		}
		return true
	})
	return last
}

// TestStreamScannerAndProtocolAdapterRegistriesAreSymmetric 守卫两默认注册表的
// family 集合完全相等:任一 family 在 protocol-adapter 注册却漏在 stream-scanner
// (或反之),该 family 的流式请求会在 forwarder.go 的 Scanners.For 取 scanner
// 失败、在投递前直接挂。本轮修复的 kimi/qwen/glm/yi/baichuan/doubao/ernie/step/
// hunyuan/minimax/cohere/ollama_chat 12 族整类即此漏接(同 23e0cb91 入站漏接同源)。
// Mutation guard: 删 stream_scanner.go 任一 SSE family,或 protocol_selector.go
// 任一 MustRegister → 两集不等,对应方向子断言红。
func TestStreamScannerAndProtocolAdapterRegistriesAreSymmetric(t *testing.T) {
	scanners := BuildDefaultStreamScannerRegistry().scanners
	adapters := BuildDefaultProtocolAdapterRegistry().adapters
	for fam := range adapters {
		if _, ok := scanners[fam]; !ok {
			t.Errorf("family %q 有 protocol adapter 但缺 stream scanner（流式请求会在 forwarder Scanners.For 失败）", fam)
		}
	}
	for fam := range scanners {
		if _, ok := adapters[fam]; !ok {
			t.Errorf("family %q 有 stream scanner 但缺 protocol adapter", fam)
		}
	}
	if len(scanners) != len(adapters) {
		t.Errorf("注册表族数不等: scanners=%d adapters=%d", len(scanners), len(adapters))
	}
}
