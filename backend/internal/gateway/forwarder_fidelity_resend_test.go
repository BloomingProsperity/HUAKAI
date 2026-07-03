package gateway

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// TestForwarderNilClientAdapterDoesNotResendOnMultiCanonicalExpansion 复现保真调研的 S1 疑点:
// nil ClientAdapter(同协议 raw 直通)下,forwarder 的主循环按 canonical 事件数调 clientChunks,
// 而 clientChunks 在 ClientAdapter==nil 时恒返回【原始上游 evt】的 rawSSE。因此若 provider adapter
// 把 1 条上游事件展开成 N 个 canonical 事件(真 openai/豆包 adapter 首内容块 →
// message_start+content_block_start+content_block_delta = 3 个,见 internal/proto/openai/sse_test.go),
// 同一条 data 帧会被写给客户 N 次。
//
// 正确契约:nil ClientAdapter 下,单条上游事件应【恰好原样透传一次】,与 canonical 展开数无关。
// 本测试即该契约的判别断言——若重发缺陷存在,单条上游帧会出现 3 次,断言变红。
func TestForwarderNilClientAdapterDoesNotResendOnMultiCanonicalExpansion(t *testing.T) {
	f := newForwarder()
	f.ProtocolAdapters = &stubSingleAdapterRegistry{
		family: "anthropic_messages",
		adapter: &forwarderClientAdapterUpstreamStub{events: []any{
			&proto.CanonicalEvent{Type: "message_start"},
			&proto.CanonicalEvent{Type: "content_block_start"},
			&proto.CanonicalEvent{Type: "content_block_delta"},
		}},
	}

	upstream := []byte("event: passthrough\ndata: {\"x\":1}\n\n")
	rec := httptest.NewRecorder()
	_, err := f.Forward(context.Background(), bytes.NewReader(upstream), rec, anthropicForwardRequest(1, 100))
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}

	got := rec.Body.String()
	if n := strings.Count(got, `data: {"x":1}`); n != 1 {
		t.Fatalf("nil ClientAdapter 下单条上游事件被重发 %d 次(应恰好 1 次)——raw 直通按 canonical 展开数逐块重发;body=%q", n, got)
	}
}

// TestForwarderRealOpenAIAdapterNilClientNoResend 第二角度(零 stub):用真 openai.Adapter +
// 真注册表(newForwarder 的 BuildDefaultProtocolAdapterRegistry)+ nil ClientAdapter(同协议
// openai_chat raw 直通),喂 openai golden SSE(3 内容块 + [DONE])。upstream 第一条内容块
// (content:"hi")在上游只发一次;真 adapter 把它展开成 message_start+content_block_start+
// content_block_delta = 3 个 canonical → 若重发缺陷成立,客户端会看到该帧 3 次。
// 判别断言:客户端输出里含 "hi" 的原始 chunk 出现次数应 == 上游发送次数(1)。
func TestForwarderRealOpenAIAdapterNilClientNoResend(t *testing.T) {
	f := newForwarder() // 真 ProtocolAdapters + 真 Scanners;ClientAdapter 保持 nil(同协议直通)
	const upstream = "data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" there\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":13,\"total_tokens\":20}}\n\n" +
		"data: [DONE]\n\n"

	rec := httptest.NewRecorder()
	req := ForwardRequest{TenantID: 1, AccountID: 100, ProtocolFamily: "openai_chat", ClientProtocol: "openai_chat"}
	draft, err := f.Forward(context.Background(), strings.NewReader(upstream), rec, req)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}

	got := rec.Body.String()
	// 上游第一条内容块含唯一子串 `"content":"hi"`,只发了一次;客户端应恰好收到一次。
	if n := strings.Count(got, `"content":"hi"`); n != 1 {
		t.Fatalf("真 openai adapter 下首内容块被重发 %d 次(应恰好 1 次)——1 chunk→3 canonical 逐块重发,客户端拿到 hihihi;\nbody=%q", n, got)
	}
	// 终止/usage 帧(1 chunk→content_block_stop+message_delta=2 canonical)同样应恰好一次——
	// 钉死角度③终止帧重发(严格 SDK 会重复计 usage)。
	if n := strings.Count(got, `"finish_reason":"stop"`); n != 1 {
		t.Fatalf("终止/usage 帧被重发 %d 次(应恰好 1 次)——终止帧展开 2 canonical 逐块重发;body=%q", n, got)
	}
	// 计费 tap 在 raw 直通下仍须正确读到 usage:completion_tokens=13 应进 draft(证循环内 acc.Update
	// 保留,修复只挪了"写"没动"tap")。判别性:若修复误把 tap 也跳过,TokensOutput=0 → 红。
	if draft.TokensOutput != 13 {
		t.Fatalf("raw 直通计费 tap 失效:draft.TokensOutput=%d 应为 13(usage 未被 acc.Update 采到)", draft.TokensOutput)
	}
}
