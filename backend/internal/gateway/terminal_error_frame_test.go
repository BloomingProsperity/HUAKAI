package gateway

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// contentThenBlockReader 先吐固定内容(让 firstEmitted=true),之后阻塞 → 触发
// inter-event 超时(模拟"已交付真实内容后上游卡死/出错")。
type contentThenBlockReader struct {
	content []byte
	off     int
	block   chan struct{}
}

func (r *contentThenBlockReader) Read(p []byte) (int, error) {
	if r.off < len(r.content) {
		n := copy(p, r.content[r.off:])
		r.off += n
		return n, nil
	}
	<-r.block
	return 0, io.EOF
}

// T2:terminalErrorFrame 纯函数 — 每个客户端协议的终止 error 帧格式正确。
// 这是修复的根:canonicalStreamErrorSSE() 是 anthropic-only event: error,对
// openai_chat 客户端(只认 data: 行)等于没发=静默截断(Codex 报 stream closed)。
// 变异守卫: terminalErrorFrame 永远 return canonicalStreamErrorSSE() →
// openai_chat/gemini/responses 子断言红。
func TestTerminalErrorFrame_PerProtocolFormat(t *testing.T) {
	join := func(fr [][]byte) string {
		var b strings.Builder
		for _, f := range fr {
			b.Write(f)
		}
		return b.String()
	}

	// openai_chat（含空/未知默认）：裸 data: 行 + [DONE]，无 event: error
	for _, cp := range []string{"openai_chat", "", "something_unknown"} {
		body := join(terminalErrorFrame(cp))
		if !strings.Contains(body, `data: {"error"`) {
			t.Fatalf("openai_chat(%q) 帧应有 data: error: %q", cp, body)
		}
		if strings.Contains(body, "event: error") {
			t.Fatalf("openai_chat(%q) 不应发 event: error(严格 SDK 忽略): %q", cp, body)
		}
		if strings.Contains(body, "[DONE]") {
			t.Fatalf("openai_chat(%q) 错误帧不应跟 [DONE](避免截断流伪装成正常完成): %q", cp, body)
		}
		if !strings.Contains(body, `"upstream_error"`) {
			t.Fatalf("openai_chat(%q) 缺固定 upstream_error code: %q", cp, body)
		}
	}
	// anthropic：event: error，恰一个
	an := join(terminalErrorFrame("anthropic_messages"))
	if strings.Count(an, "event: error") != 1 || !strings.Contains(an, `"upstream_error"`) {
		t.Fatalf("anthropic 帧应恰一个 event: error: %q", an)
	}
	// responses：命名 error 事件 + type:error
	rs := join(terminalErrorFrame("openai_responses"))
	if !strings.Contains(rs, "event: error") || !strings.Contains(rs, `"type":"error"`) {
		t.Fatalf("responses 帧应有命名 error 事件 + type:error: %q", rs)
	}
	// gemini：data-only，UNAVAILABLE，无 event，无 [DONE]
	gm := join(terminalErrorFrame("gemini"))
	if !strings.Contains(gm, `data: {"error"`) || !strings.Contains(gm, "UNAVAILABLE") {
		t.Fatalf("gemini 帧应有 data: error UNAVAILABLE: %q", gm)
	}
	if strings.Contains(gm, "event: error") || strings.Contains(gm, "[DONE]") {
		t.Fatalf("gemini 帧不应有具名 event 或 [DONE]: %q", gm)
	}
}

// T1:已交付真实内容后 inter-event 超时 → 必须补一个【按客户端协议正确的】终止
// error 帧。此前条件含 !firstEmitted,已交付后超时被漏掉=静默截断。
// 变异守卫: 补帧条件改回 !firstEmitted → firstEmitted=true 时不补 → body 无 error 帧红。
func TestForward_DeliveredThenTimeout_EmitsProtocolTerminalFrame(t *testing.T) {
	cases := []struct {
		clientProto     string
		wantContains    []string
		wantNotContains []string
	}{
		{"openai_chat", []string{`data: {"error"`, "upstream_error"}, []string{"event: error", "[DONE]"}},
		{"anthropic_messages", []string{"event: error", "upstream_error"}, nil},
		{"gemini", []string{`data: {"error"`, "UNAVAILABLE"}, []string{"event: error", "[DONE]"}},
	}
	content := sseBytes(messageStart("m1"), textDelta(0, "hello"))
	for _, tc := range cases {
		t.Run(tc.clientProto, func(t *testing.T) {
			block := make(chan struct{})
			t.Cleanup(func() { close(block) })
			reader := &contentThenBlockReader{content: content, block: block}

			f := newForwarder()
			f.Timeouts.KeepAliveInterval = 0
			f.Timeouts.FirstTokenTimeout = 0
			f.Timeouts.InterEventTimeout = 50 * time.Millisecond
			f.Timeouts.TotalStreamTimeout = 2 * time.Second

			req := anthropicForwardRequest(1, 100)
			req.ClientProtocol = tc.clientProto
			rec := httptest.NewRecorder()
			_, err := f.Forward(context.Background(), reader, rec, req)
			if !errors.Is(err, ErrInterEventTimeout) {
				t.Fatalf("应 inter-event 超时收尾; got %v", err)
			}
			body := rec.Body.String()
			for _, w := range tc.wantContains {
				if !strings.Contains(body, w) {
					t.Fatalf("[%s] body 缺 %q: %q", tc.clientProto, w, body)
				}
			}
			for _, nw := range tc.wantNotContains {
				if strings.Contains(body, nw) {
					t.Fatalf("[%s] body 不应含 %q: %q", tc.clientProto, nw, body)
				}
			}
		})
	}
}
