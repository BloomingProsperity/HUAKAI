package protosse

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/dify"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/geminicodeassist"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/ollama"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/openai"
)

// floodEventReader 惰性产出 remaining 个相同的坏 SSE 事件后 EOF,用于在不分配巨大
// 字符串的前提下模拟「超量/近乎无界的上游流」;每次 Read 至多吐一个事件的字节。
// 读到 EOF 前 remaining 会随消费递减,故测试可用 remaining>0 断言重组提前终止、
// 没有把整条流读完(证明总事件上限真的短路了消费)。
type floodEventReader struct {
	event     []byte
	remaining int
	buf       []byte
}

func (f *floodEventReader) Read(p []byte) (int, error) {
	if len(f.buf) == 0 {
		if f.remaining <= 0 {
			return 0, io.EOF
		}
		f.remaining--
		f.buf = append([]byte(nil), f.event...)
	}
	n := copy(p, f.buf)
	f.buf = f.buf[n:]
	return n, nil
}

// TestReconstructBufferedFromSSEReaderUpstreamEventFloodTripsLimit 守卫 S2 DoS:
// 坏事件(JSON 解析失败)不产 canonical 事件、绕过 MaxCanonicalPayloadBytes /
// MaxCanonicalEvents 两道 canonical 软上限,却每个让 losses 增长一项。恶意上游
// 狂发不可解析小事件时,若无「总消费事件上限」,losses 切片无界增长直至 OOM、
// 拖垮承载所有租户的共享网关。
// 判别 fixture:cap=5,惰性产出 5 万个坏事件(`data: {`)。修好后断言返回
// ErrBufferedSSECanonicalTooLarge,且 reader 仍有大量未读事件(remaining>0)=提前
// 终止、没读完整条流。
// Mutation:删 ReconstructBufferedFromSSEReader 回调里的 consumedEvents 上限检查 →
// 5 万个坏事件全被消费(remaining 归 0)、0 canonical 事件 → 返回 err==nil、
// ok==true → 本测试两处断言均红。
func TestReconstructBufferedFromSSEReaderUpstreamEventFloodTripsLimit(t *testing.T) {
	r := &floodEventReader{event: []byte("data: {\n\n"), remaining: 50000}
	limits := BufferedSSEReconstructLimits{MaxUpstreamEvents: 5}
	env, _, _, err := ReconstructBufferedFromSSEReader(context.Background(), &openai.ResponsesAdapter{}, r, limits)
	if !errors.Is(err, ErrBufferedSSECanonicalTooLarge) {
		t.Fatalf("坏事件洪流必须触发 too-large,得 err=%v env=%+v", err, env)
	}
	if r.remaining == 0 {
		t.Fatal("上限未短路消费:reader 被读到底(remaining=0),说明总事件上限没生效")
	}
}

// TestReconstructBufferedFromSSEReaderGoodStreamUnaffectedByEventLimit 负向:总事件
// 上限不能误伤良构流。良构流每事件都产 canonical 事件、事件数远小于上限,必须照常
// 重组出 buffered response,不被 too-large 误拦。
func TestReconstructBufferedFromSSEReaderGoodStreamUnaffectedByEventLimit(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"id":"chatcmpl-sse","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"rescued text"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-sse","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
	env, _, ok, err := ReconstructBufferedFromSSEReader(context.Background(), &openai.Adapter{}, strings.NewReader(raw), BufferedSSEReconstructLimits{})
	if err != nil {
		t.Fatalf("良构流不应报错: %v", err)
	}
	if !ok || env == nil || env.BufferedResponse == nil {
		t.Fatalf("良构流必须重组出 buffered response: ok=%v env=%+v", ok, env)
	}
	if got := env.BufferedResponse.Content; len(got) != 1 || got[0].Type != "text" || got[0].Text != "rescued text" {
		t.Fatalf("content = %+v, want one text block 'rescued text'", got)
	}
	if got := env.BufferedResponse.Usage; got.InputTokens != 10 || got.OutputTokens != 5 {
		t.Fatalf("usage = %+v, want input=10 output=5", got)
	}
}

// TestReconstructBufferedFromSSEDifyStream 守卫族集对称第 8 站:本包
// newUpstreamState 与 gateway/forwarder.newUpstreamState 是孪生类型分派,
// dify case 缺失时 dify adapter type-assert *dify.UpstreamState 失败,每个
// 事件进 loss、重组永远失败——blocking 请求遇上游回 SSE 时整族不可恢复。
// Mutation:删 newUpstreamState 的 dify case → 本测试红。
func TestReconstructBufferedFromSSEDifyStream(t *testing.T) {
	raw := []byte(`
data: {"event":"message","conversation_id":"conv-1","answer":"rescued dify text"}

data: {"event":"message_end","conversation_id":"conv-1","metadata":{"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}}
`)
	env, _, ok := ReconstructBufferedFromSSE(&dify.Adapter{}, raw)
	if !ok {
		t.Fatal("dify SSE body must be detected and reconstructed")
	}
	if env == nil || env.BufferedResponse == nil {
		t.Fatalf("reconstructed envelope missing buffered response: %+v", env)
	}
	if got := env.BufferedResponse.Content; len(got) != 1 || got[0].Type != "text" || got[0].Text != "rescued dify text" {
		t.Fatalf("reconstructed content = %+v, want one text block", got)
	}
	if got := env.BufferedResponse.Usage; got.InputTokens != 7 || got.OutputTokens != 3 {
		t.Fatalf("reconstructed usage = %+v, want input=7 output=3", got)
	}
}

// TestReconstructBufferedFromSSEOllamaStream 守卫族集对称第 8 站:
// newUpstreamState 漏 ollama case 时 ollama adapter type-assert
// *ollama.UpstreamState 失败,每个事件进 loss、重组永远失败——blocking 请求
// 遇代理把 NDJSON 包成 SSE 回传时整族不可恢复。
// Mutation:删 newUpstreamState 的 ollama case → 本测试红。
func TestReconstructBufferedFromSSEOllamaStream(t *testing.T) {
	raw := []byte(`
data: {"model":"llama3.2","message":{"role":"assistant","content":"rescued ollama text"},"done":false}

data: {"model":"llama3.2","done":true,"done_reason":"stop","prompt_eval_count":7,"eval_count":3}
`)
	env, _, ok := ReconstructBufferedFromSSE(&ollama.Adapter{}, raw)
	if !ok {
		t.Fatal("ollama SSE-shaped body must be detected and reconstructed")
	}
	if env == nil || env.BufferedResponse == nil {
		t.Fatalf("reconstructed envelope missing buffered response: %+v", env)
	}
	if got := env.BufferedResponse.Content; len(got) != 1 || got[0].Type != "text" || got[0].Text != "rescued ollama text" {
		t.Fatalf("reconstructed content = %+v, want one text block", got)
	}
	if got := env.BufferedResponse.Usage; got.InputTokens != 7 || got.OutputTokens != 3 {
		t.Fatalf("reconstructed usage = %+v, want input=7 output=3", got)
	}
}

// TestReconstructBufferedFromSSEGeminiCodeAssistStream 守卫族集对称第 8 站:
// newUpstreamState 漏 geminicodeassist case 时,它委托内嵌 gemini.Adapter,
// type-assert *gemini.UpstreamState 失败,每个事件进 loss、重组永远失败——
// blocking 请求遇 cloudcode-pa 回 {response}-包裹的 SSE 时整族不可恢复。
// 本测试同时覆盖 per-chunk {response} unwrap(SSE data 包一层 response)。
// Mutation:删 newUpstreamState 的 geminicodeassist case → state 落 anthropic、
// 委托内 type-assert 失败 → 重组无内容 → 红;删 adapter 的 unwrap → gemini 解
// {response:...} 无 candidates → 无内容 → 红。
func TestReconstructBufferedFromSSEGeminiCodeAssistStream(t *testing.T) {
	raw := []byte(`
data: {"response":{"candidates":[{"content":{"parts":[{"text":"rescued code assist"}],"role":"model"},"index":0}],"modelVersion":"gemini-2.5-pro","responseId":"r1"}}

data: {"response":{"candidates":[{"content":{"parts":[{"text":""}],"role":"model"},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"totalTokenCount":10}}}
`)
	env, _, ok := ReconstructBufferedFromSSE(&geminicodeassist.Adapter{}, raw)
	if !ok {
		t.Fatal("gemini code assist SSE body must be detected and reconstructed")
	}
	if env == nil || env.BufferedResponse == nil {
		t.Fatalf("reconstructed envelope missing buffered response: %+v", env)
	}
	if got := env.BufferedResponse.Content; len(got) != 1 || got[0].Type != "text" || got[0].Text != "rescued code assist" {
		t.Fatalf("reconstructed content = %+v, want one text block 'rescued code assist'", got)
	}
	if got := env.BufferedResponse.Usage; got.InputTokens != 7 || got.OutputTokens != 3 {
		t.Fatalf("reconstructed usage = %+v, want input=7 output=3", got)
	}
}

func TestReconstructBufferedFromSSEOpenAITextAndUsage(t *testing.T) {
	// 移除 SSE 嗅探/兜底会让本测试变红:原始响应体不是单个 JSON 对象,
	// 普通缓冲解析器无法从中恢复出内容或 usage。
	raw := []byte(`
data: {"id":"chatcmpl-sse","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"rescued text"},"finish_reason":null}]}

data: {"id":"chatcmpl-sse","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}

data: [DONE]
`)

	env, losses, ok := ReconstructBufferedFromSSE(&openai.Adapter{}, raw)
	if !ok {
		t.Fatal("SSE body must be detected and reconstructed")
	}
	if len(losses) != 0 {
		t.Fatalf("happy path should not emit losses: %+v", losses)
	}
	if env == nil || env.BufferedResponse == nil {
		t.Fatalf("reconstructed envelope missing buffered response: %+v", env)
	}
	if got := env.BufferedResponse.Content; len(got) != 1 || got[0].Type != "text" || got[0].Text != "rescued text" {
		t.Fatalf("reconstructed content = %+v, want one text block with rescued text", got)
	}
	if got := env.BufferedResponse.Usage; got.InputTokens != 10 || got.OutputTokens != 5 {
		t.Fatalf("reconstructed usage = %+v, want input=10 output=5", got)
	}
}

func TestReconstructBufferedFromSSEPlainJSONDoesNotTrigger(t *testing.T) {
	raw := []byte(`{"id":"chatcmpl-json","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"plain json"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`)

	env, losses, ok := ReconstructBufferedFromSSE(&openai.Adapter{}, raw)
	if ok {
		t.Fatalf("plain JSON body must not trigger SSE fallback: env=%+v losses=%+v", env, losses)
	}
	if env != nil || losses != nil {
		t.Fatalf("non-SSE body must return nil envelope/losses, got env=%+v losses=%+v", env, losses)
	}
}

// TestReconstructBufferedFromSSEAnthropicPreservesInputAndCacheTokens 守卫 S1 少计费:
// 真 Anthropic SSE 把 input/cache_read/cache_creation 只放在 message_start,
// message_delta 顶层 usage 通常只带新的 output_tokens。缓冲重组若在 message_delta
// 整段覆盖 Usage,会把 message_start 的 input=1200/cache_read=800/cache_creation=50
// 抹成 0,而该缓冲响应被非流式计费消费(reportedUsageMissing 因 output>0 为 false、
// 不 abort)→ 静默按 input=0 结算,缓存重/长上下文 Claude 流量(input 常是主成本)
// 被少计费。
// 判别性 fixture:message_start 带 input=1200/cache_read=800/cache_creation=50、
// message_delta 只带 output=45。修好后断言 input==1200 且 cache_read==800 且
// output==45;同时验证 message_delta 真带新 output 时确实更新(45 而非 message_start
// 的 1)。
// Mutation:把 reconstruct.go message_delta 分支改回 `resp.Usage = *evt.Usage`
// 整段覆盖 → input/cache_read/cache_creation 全变 0 → 本测试红。
func TestReconstructBufferedFromSSEAnthropicPreservesInputAndCacheTokens(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_cache","model":"claude-3-5-sonnet","usage":{"input_tokens":1200,"cache_read_input_tokens":800,"cache_creation_input_tokens":50,"output_tokens":1}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":45}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n"))

	env, _, ok := ReconstructBufferedFromSSE(&anthropic.Adapter{}, raw)
	if !ok {
		t.Fatal("Anthropic SSE-shaped body must be detected and reconstructed")
	}
	if env == nil || env.BufferedResponse == nil {
		t.Fatalf("reconstructed envelope missing buffered response: %+v", env)
	}
	got := env.BufferedResponse.Usage
	// 核心断言:message_start 的 input 必须存活,不能被 message_delta 抹零。
	if got.InputTokens != 1200 {
		t.Fatalf("InputTokens = %d, want 1200 (message_start 的 input 被 message_delta 抹零=少计费)", got.InputTokens)
	}
	// cache_read 是命中缓存的 prompt token,同样只在 message_start 出现,必须存活。
	if got.CacheReadInputTokens != 800 {
		t.Fatalf("CacheReadInputTokens = %d, want 800 (缓存读 token 被抹零=少计费)", got.CacheReadInputTokens)
	}
	if got.CacheCreationInputTokens != 50 {
		t.Fatalf("CacheCreationInputTokens = %d, want 50 (缓存创建 token 被抹零=少计费)", got.CacheCreationInputTokens)
	}
	// message_delta 带的新 output 必须更新到最新值(45),不能停在 message_start 的 1。
	if got.OutputTokens != 45 {
		t.Fatalf("OutputTokens = %d, want 45 (message_delta 的新 output 未被采纳)", got.OutputTokens)
	}
	// 计费消费方依赖 reportedUsageMissing=false 才不 abort;总账也应反映完整 input。
	if got.TotalTokens != 1245 {
		t.Fatalf("TotalTokens = %d, want 1245 (input1200+output45)", got.TotalTokens)
	}
	// 计费侧 envelope 的 Accounting.Usage 与缓冲响应须一致(它是实际结算入口)。
	if env.Accounting.Usage.InputTokens != 1200 || env.Accounting.Usage.CacheReadInputTokens != 800 {
		t.Fatalf("Accounting.Usage = %+v, want input=1200 cache_read=800", env.Accounting.Usage)
	}
}

func TestReconstructBufferedFromSSEMissingMessageStartDoesNotReturnResponse(t *testing.T) {
	// 已堵风险:缓冲兜底绝不能把缺少 message_start 的 Anthropic content delta
	// 变成一个成功响应。变异自检:移除 content-before-start 守门后此处会返回
	// 一个 BufferedResponse。
	raw := []byte(strings.Join([]string{
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"orphan"}}`,
		``,
		``,
	}, "\n"))

	env, losses, ok := ReconstructBufferedFromSSE(&anthropic.Adapter{}, raw)
	if !ok {
		t.Fatal("SSE-shaped body must be recognized")
	}
	if env != nil && env.BufferedResponse != nil {
		t.Fatalf("missing message_start reconstructed response: %+v", env.BufferedResponse)
	}
	if len(losses) == 0 {
		t.Fatal("missing message_start should emit reconstruction loss evidence")
	}
}
