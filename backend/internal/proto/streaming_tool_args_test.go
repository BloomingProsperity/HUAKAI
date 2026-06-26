package proto

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// 本文件判别两个跨协议流式工具入参丢失 bug(对抗 bug-hunt 确认,均 S1 数据损坏):
//  Bug#1:上游 SSE 解析器(anthropic/sse.go、openai/sse.go)把工具入参 delta 统一产出为 canonical
//        类型 `tool_input_delta`,但三个 client 渲染器此前只认 anthropic 线名 `input_json_delta` →
//        真 canonical 类型掉 default 被记 loss 丢弃 → 跨协议流式工具入参整条丢失。
//  Bug#2:gemini 上游在 content_block_start 即携带完整入参(ContentBlock.Input,无后续 delta),
//        anthropic/openai_chat 渲染器此前硬写空 input → gemini→非gemini 流式工具入参整条丢失。
// 现有测试只喂 `input_json_delta`(非判别 fixture,恰好命中旧 case)故从未抓到这两个 bug。

type clientChunkRenderer interface {
	CanonicalEventToClientChunk(ctx context.Context, evt any, state any) ([][]byte, []ProtocolLossEntry, error)
}

// feedEvt 喂一个 canonical 事件给渲染器,返回拼接后的输出字节 + loss,err 即失败。
func feedEvt(t *testing.T, r clientChunkRenderer, ctx context.Context, state any, evt *CanonicalEvent) (string, []ProtocolLossEntry) {
	t.Helper()
	chunks, losses, err := r.CanonicalEventToClientChunk(ctx, evt, state)
	if err != nil {
		t.Fatalf("渲染 %s 出错: %v", evt.Type, err)
	}
	return string(joinChunks(chunks)), losses
}

func TestAnthropicStream_ToolInputDeltaCanonicalNotDropped(t *testing.T) {
	// 抓 Bug#1(anthropic 渲染器):喂真 canonical 类型 tool_input_delta,必须渲染成 anthropic 线
	// input_json_delta、工具入参 partial_json 到达客户端、且不记 loss。
	// 变异(已验证转红):把 delta case 改回只 `input_json_delta` → tool_input_delta 掉 default →
	// 记 unknown_delta_type loss + 0 字节 → 下面 loss==0 与含 "city" 两断言同时红。
	adapter := &AnthropicMessagesClient{}
	state := NewAnthropicMessagesStreamState()
	ctx := context.Background()
	feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "message_start", MessageID: "msg_1", Model: "claude"})
	feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_start", Index: 0,
		ContentBlock: &CanonicalContentBlock{Type: "tool_use", CallID: "call_1", Name: "lookup"}})
	joined, losses := feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_delta", Index: 0,
		Delta: &CanonicalContentDelta{Type: "tool_input_delta", PartialJSON: json.RawMessage(`{"city":"SF"}`)}})
	if len(losses) != 0 {
		t.Fatalf("tool_input_delta 不得记 loss 丢弃: %+v", losses)
	}
	if !strings.Contains(joined, "input_json_delta") || !strings.Contains(joined, "city") {
		t.Fatalf("工具入参 delta 被丢(未渲染): %q", joined)
	}
}

func TestAnthropicStream_GeminiStartCarriedToolInputSynthesizedAsDelta(t *testing.T) {
	// 抓 Bug#2(anthropic 渲染器):gemini 上游在 content_block_start 携带完整 Input(无 delta)。
	// anthropic 线格式 start 的 input 恒 {}、客户端从 input_json_delta 累积,故必须把 Input 合成一条
	// input_json_delta 发出。变异(已验证转红):删合成 delta 分支 → 入参整条丢失 → 含 "city" 断言红。
	adapter := &AnthropicMessagesClient{}
	state := NewAnthropicMessagesStreamState()
	ctx := context.Background()
	feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "message_start", MessageID: "msg_2", Model: "claude"})
	joined, _ := feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_start", Index: 0,
		ContentBlock: &CanonicalContentBlock{Type: "tool_use", CallID: "call_2", Name: "lookup",
			Input: json.RawMessage(`{"city":"Paris"}`)}})
	if !strings.Contains(joined, "content_block_start") {
		t.Fatalf("缺 content_block_start: %q", joined)
	}
	if !strings.Contains(joined, "input_json_delta") || !strings.Contains(joined, "Paris") {
		t.Fatalf("gemini start 携带的工具入参未合成 input_json_delta 发出(整条丢失): %q", joined)
	}
}

func TestOpenAIChatStream_ToolInputDeltaCanonicalNotDropped(t *testing.T) {
	// 抓 Bug#1(openai_chat 渲染器):真 canonical tool_input_delta 必须渲染成 function.arguments 增量。
	// 变异(已验证转红):delta case 改回只 input_json_delta → tool_input_delta 掉 default 记 loss + 0 字节。
	adapter := &OpenAIChatClient{}
	state := NewOpenAIChatStreamState()
	ctx := context.Background()
	feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "message_start", MessageID: "c_1", Model: "gpt"})
	feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_start", Index: 0,
		ContentBlock: &CanonicalContentBlock{Type: "tool_use", CallID: "call_3", Name: "lookup"}})
	joined, losses := feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_delta", Index: 0,
		Delta: &CanonicalContentDelta{Type: "tool_input_delta", PartialJSON: json.RawMessage(`{"q":"BERLINVAL"}`)}})
	if len(losses) != 0 {
		t.Fatalf("tool_input_delta 不得记 loss 丢弃: %+v", losses)
	}
	// arguments 是 JSON 字符串,partial 在输出里被转义(\"q\"),故用一个独特的值断言内容到达。
	if !strings.Contains(joined, "tool_calls") || !strings.Contains(joined, "BERLINVAL") {
		t.Fatalf("工具入参 delta 被丢(未渲染 arguments): %q", joined)
	}
}

func TestOpenAIChatStream_GeminiStartCarriedToolInputInArguments(t *testing.T) {
	// 抓 Bug#2(openai_chat 渲染器):gemini 上游 start 携带完整 Input,OpenAI 客户端跨 chunk 拼接
	// function.arguments,故首个 chunk 的 arguments 必须带上 Input。变异(已验证转红):start 恒写 "" →
	// 入参丢失 → 含 "Tokyo" 断言红。
	adapter := &OpenAIChatClient{}
	state := NewOpenAIChatStreamState()
	ctx := context.Background()
	feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "message_start", MessageID: "c_2", Model: "gpt"})
	joined, _ := feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_start", Index: 0,
		ContentBlock: &CanonicalContentBlock{Type: "tool_use", CallID: "call_4", Name: "lookup",
			Input: json.RawMessage(`{"city":"Tokyo"}`)}})
	if !strings.Contains(joined, "tool_calls") || !strings.Contains(joined, "Tokyo") {
		t.Fatalf("gemini start 携带的工具入参未进 arguments(整条丢失): %q", joined)
	}
}

func TestOpenAIResponsesStream_ToolInputDeltaCanonicalNotDropped(t *testing.T) {
	// 抓 Bug#1(openai_responses 渲染器):真 canonical tool_input_delta 必须渲染成
	// response.function_call_arguments.delta。变异(已验证转红):delta case 改回只 input_json_delta →
	// 掉 default 记 loss + 0 字节。
	adapter := &OpenAIResponsesClient{}
	state := NewOpenAIResponsesStreamState()
	ctx := context.Background()
	feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "message_start", MessageID: "resp_1", Model: "claude"})
	feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_start", Index: 0,
		ContentBlock: &CanonicalContentBlock{Type: "tool_use", CallID: "call_5", Name: "lookup"}})
	joined, losses := feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_delta", Index: 0,
		Delta: &CanonicalContentDelta{Type: "tool_input_delta", PartialJSON: json.RawMessage(`{"city":"Rome"}`)}})
	if len(losses) != 0 {
		t.Fatalf("tool_input_delta 不得记 loss 丢弃: %+v", losses)
	}
	if !strings.Contains(joined, "function_call_arguments.delta") || !strings.Contains(joined, "Rome") {
		t.Fatalf("工具入参 delta 被丢(未渲染): %q", joined)
	}
}

// 以下三个测试守护对抗审查抓出的回归:Anthropic 流式协议在 content_block_start 恒发占位 `"input":{}`,
// 真入参随后由 input_json_delta 流入。start-Input 投递逻辑必须用 meaningfulToolInput 排除占位 `{}`,
// 否则把 `{}` 与后续真 delta 双发,拼成 `{}{...}` 损坏 JSON(生产主路径:Anthropic 上游 → 任意客户端)。

func TestAnthropicStream_EmptyObjectStartInputNotSynthesized(t *testing.T) {
	// 变异(已验证转红):start 合成 delta 的 guard 用 len(Input)>0 而非 meaningfulToolInput →
	// 占位 {} 被合成 content_block_delta → 与后续真 delta 双发损坏 → 下面 content_block_delta 断言红。
	adapter := &AnthropicMessagesClient{}
	state := NewAnthropicMessagesStreamState()
	ctx := context.Background()
	feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "message_start", MessageID: "m", Model: "claude"})
	startOut, _ := feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_start", Index: 0,
		ContentBlock: &CanonicalContentBlock{Type: "tool_use", CallID: "c1", Name: "f", Input: json.RawMessage(`{}`)}})
	if strings.Contains(startOut, "content_block_delta") {
		t.Fatalf("占位 {} 被误合成 input_json_delta(会与后续真 delta 双发损坏): %q", startOut)
	}
	deltaOut, losses := feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_delta", Index: 0,
		Delta: &CanonicalContentDelta{Type: "tool_input_delta", PartialJSON: json.RawMessage(`{"city":"Madrid"}`)}})
	if len(losses) != 0 || !strings.Contains(deltaOut, "Madrid") {
		t.Fatalf("占位后真入参 delta 应正常渲染: out=%q losses=%+v", deltaOut, losses)
	}
}

func TestOpenAIChatStream_EmptyObjectStartInputNotInArguments(t *testing.T) {
	// 变异(已验证转红):startArgs guard 用 len(Input)>0 → start chunk arguments="{}" → 与后续 delta 拼损坏。
	adapter := &OpenAIChatClient{}
	state := NewOpenAIChatStreamState()
	ctx := context.Background()
	feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "message_start", MessageID: "c", Model: "gpt"})
	startOut, _ := feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_start", Index: 0,
		ContentBlock: &CanonicalContentBlock{Type: "tool_use", CallID: "c1", Name: "f", Input: json.RawMessage(`{}`)}})
	if strings.Contains(startOut, `"arguments":"{}"`) {
		t.Fatalf("占位 {} 进了 start arguments(会与后续 delta 双发损坏): %q", startOut)
	}
}

func TestOpenAIResponsesStream_EmptyObjectStartInputNotAccumulated(t *testing.T) {
	// 变异(已验证转红):ToolArgs guard 用 len(Input)>0 → 占位 {} 先入 ToolArgs,arguments.done 输出 {}{...} 损坏。
	adapter := &OpenAIResponsesClient{}
	state := NewOpenAIResponsesStreamState()
	ctx := context.Background()
	feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "message_start", MessageID: "r", Model: "claude"})
	feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_start", Index: 0,
		ContentBlock: &CanonicalContentBlock{Type: "tool_use", CallID: "c1", Name: "f", Input: json.RawMessage(`{}`)}})
	feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_delta", Index: 0,
		Delta: &CanonicalContentDelta{Type: "tool_input_delta", PartialJSON: json.RawMessage(`{"city":"Madrid"}`)}})
	doneOut, _ := feedEvt(t, adapter, ctx, state, &CanonicalEvent{Type: "content_block_stop", Index: 0})
	if strings.Contains(doneOut, "{}{") {
		t.Fatalf("占位 {} 与真入参拼成损坏 JSON: %q", doneOut)
	}
	if !strings.Contains(doneOut, "Madrid") {
		t.Fatalf("真入参丢失: %q", doneOut)
	}
}

func TestMeaningfulToolInput_WhitespaceEmptyObjectVariants(t *testing.T) {
	// 守 S3(对抗审查补强):占位空对象的所有空白/换行变体都必须判 false(不在 start 投递),否则会与
	// 后续真 input_json_delta 双发拼成损坏 JSON。canonical Input 是 json.RawMessage 逐字保留上游字节、
	// 不归一化,故 `{ }`/`{\n}` 这类带空白的空对象不能漏判。
	// 变异(已验证转红):helper 用字面量 bytes.Equal({}) 而非解析判空 → `{ }`/`{\n}` 判 true → 此处红。
	for _, s := range []string{`{}`, `{ }`, "{\n}", "  {}  ", `null`, ` null `, ``, `   `} {
		if meaningfulToolInput(json.RawMessage(s)) {
			t.Fatalf("占位/空 %q 应判 false(不投递)", s)
		}
	}
	for _, s := range []string{`{"city":"SF"}`, `{ "a": 1 }`, `[1,2]`, `"x"`} {
		if !meaningfulToolInput(json.RawMessage(s)) {
			t.Fatalf("真入参 %q 应判 true(投递)", s)
		}
	}
}
