package ollama

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func feed(t *testing.T, a *Adapter, st *UpstreamState, payload string) ([]proto.CanonicalEvent, []proto.ProtocolLossEntry) {
	t.Helper()
	rawEvents, losses, err := a.ProviderEventToCanonicalEvents(context.Background(), []byte(payload), st)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents(%q): %v", payload, err)
	}
	events := make([]proto.CanonicalEvent, 0, len(rawEvents))
	for _, e := range rawEvents {
		evt, ok := e.(proto.CanonicalEvent)
		if !ok {
			t.Fatalf("事件类型=%T 期望 proto.CanonicalEvent", e)
		}
		events = append(events, evt)
	}
	return events, losses
}

func eventTypes(events []proto.CanonicalEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

// TestContentDeltasAccumulate 抓的回归:content 增量帧未先发 message_start/
// content_block_start(下游 client adapter 收到孤儿 delta 即弃流),或后续
// 增量重复发 start 事件。
func TestContentDeltasAccumulate(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}

	first, losses := feed(t, a, st, `{"model":"llama3.2","message":{"role":"assistant","content":"Hel"},"done":false}`)
	if len(losses) != 0 {
		t.Fatalf("content 帧不应产生 loss: %+v", losses)
	}
	want := []string{"message_start", "content_block_start", "content_block_delta"}
	if got := eventTypes(first); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("首帧事件序列=%v want %v", got, want)
	}
	if first[0].Model != "llama3.2" {
		t.Errorf("message_start.Model=%q want llama3.2", first[0].Model)
	}
	if first[2].Delta == nil || first[2].Delta.Type != "text_delta" || first[2].Delta.Text != "Hel" {
		t.Fatalf("首 delta=%+v want text_delta Hel", first[2].Delta)
	}

	second, _ := feed(t, a, st, `{"model":"llama3.2","message":{"role":"assistant","content":"lo"},"done":false}`)
	if len(second) != 1 || second[0].Type != "content_block_delta" {
		t.Fatalf("后续增量应只发 delta: %v", eventTypes(second))
	}
	if second[0].Delta.Text != "lo" {
		t.Fatalf("第二 delta text=%q want lo", second[0].Delta.Text)
	}
}

// TestThinkingDeltaIsReasoningNotText 抓的回归:thinking 增量被误投影成
// text_delta 混进答案正文(思维链进计费正文+污染客户端答案)。必须是
// reasoning_delta 且文本走 ReasoningText 通道。
// Mutation:把 thinking 分支映射改成 text_delta → 本测试红。
func TestThinkingDeltaIsReasoningNotText(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	events, losses := feed(t, a, st, `{"model":"r1","message":{"role":"assistant","content":"","thinking":"pondering"},"done":false}`)
	if len(losses) != 0 {
		t.Fatalf("thinking 帧不应产生 loss: %+v", losses)
	}
	want := []string{"message_start", "content_block_start", "content_block_delta"}
	if got := eventTypes(events); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("thinking 帧事件序列=%v want %v", got, want)
	}
	delta := events[2].Delta
	if delta == nil || delta.Type != "reasoning_delta" {
		t.Fatalf("thinking 增量必须是 reasoning_delta: %+v", delta)
	}
	if delta.ReasoningText != "pondering" || delta.Text != "" {
		t.Fatalf("思维链文本必须走 ReasoningText 而非 Text: %+v", delta)
	}
}

// TestToolCallFrameEmitsToolUseBlock 抓的回归:tool_calls 帧的对象形
// arguments 没序列化进 Input(或被字符串二次编码),或 tool_use 块没发
// start/stop 成对事件。
func TestToolCallFrameEmitsToolUseBlock(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	events, losses := feed(t, a, st, `{"model":"llama3.2","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"sf","days":3}}}]},"done":false}`)
	if len(losses) != 0 {
		t.Fatalf("tool_calls 帧不应产生 loss: %+v", losses)
	}
	want := []string{"message_start", "content_block_start", "content_block_stop"}
	if got := eventTypes(events); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tool_calls 帧事件序列=%v want %v", got, want)
	}
	block := events[1].ContentBlock
	if block == nil || block.Type != "tool_use" || block.Name != "get_weather" {
		t.Fatalf("tool_use 块字段不齐: %+v", block)
	}
	if block.CallID == "" {
		t.Fatal("tool_use 块必须携带合成 CallID(Ollama 原生帧无 id 可透传)")
	}
	if got := string(block.Input); got != `{"city":"sf","days":3}` {
		t.Fatalf("Input 必须是 arguments 对象原文: %s", got)
	}
}

// TestDoneFrameUsageMapping 抓的回归:终帧 usage 映射断链或映反——
// prompt_eval_count→InputTokens、eval_count→OutputTokens,映反则计费输入/
// 输出颠倒(本测试用不对称数值钉死方向),Total 必须为两者之和。
func TestDoneFrameUsageMapping(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	feed(t, a, st, `{"model":"llama3.2","message":{"role":"assistant","content":"Hi"},"done":false}`)

	events, losses := feed(t, a, st, `{"model":"llama3.2","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":11,"eval_count":5}`)
	if len(losses) != 0 {
		t.Fatalf("stop 终帧不应产生 loss: %+v", losses)
	}
	want := []string{"content_block_stop", "message_delta", "message_stop"}
	if got := eventTypes(events); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("终帧事件序列=%v want %v", got, want)
	}
	delta := events[1]
	if delta.Usage == nil {
		t.Fatal("message_delta.Usage 为 nil(usage 丢失=计费丢失)")
	}
	if delta.Usage.InputTokens != 11 || delta.Usage.OutputTokens != 5 {
		t.Fatalf("usage=%+v want in=11 out=5(prompt_eval→Input/eval→Output 映反必红)", delta.Usage)
	}
	if delta.Usage.TotalTokens != 16 {
		t.Fatalf("TotalTokens=%d want 16(两者之和)", delta.Usage.TotalTokens)
	}
	if delta.StopReason != proto.CanonicalStopEndTurn {
		t.Fatalf("stop_reason=%q want end_turn", delta.StopReason)
	}
	if delta.NativeFinishReason != "stop" {
		t.Fatalf("NativeFinishReason=%q want stop", delta.NativeFinishReason)
	}
}

// TestDoneReasonMappingTable 抓的回归:done_reason 映射表漂移——stop→end_turn,
// length→max_tokens,其余(load 等)→unknown 且记恰一条 loss。
func TestDoneReasonMappingTable(t *testing.T) {
	for _, tc := range []struct {
		reason   string
		want     proto.CanonicalStopReason
		wantLoss int
	}{
		{"stop", proto.CanonicalStopEndTurn, 0},
		{"length", proto.CanonicalStopMaxTokens, 0},
		{"load", proto.CanonicalStopUnknown, 1},
		{"unload", proto.CanonicalStopUnknown, 1},
	} {
		a := &Adapter{}
		st := &UpstreamState{}
		events, losses := feed(t, a, st, `{"model":"m","done":true,"done_reason":"`+tc.reason+`","prompt_eval_count":1,"eval_count":1}`)
		var deltaReason proto.CanonicalStopReason
		for _, e := range events {
			if e.Type == "message_delta" {
				deltaReason = e.StopReason
			}
		}
		if deltaReason != tc.want {
			t.Errorf("done_reason=%q → %q want %q", tc.reason, deltaReason, tc.want)
		}
		if len(losses) != tc.wantLoss {
			t.Errorf("done_reason=%q loss 数=%d want %d: %+v", tc.reason, len(losses), tc.wantLoss, losses)
		}
	}
}

// TestDoneFrameWithoutUsageEmitsNilUsage 抓的回归:终帧缺 usage 计数时发
// 零值 usage(零计费帧会被下游当真实 usage 落账)——message_delta.Usage 必须
// 留 nil,交 Finalize 兜底。
func TestDoneFrameWithoutUsageEmitsNilUsage(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	feed(t, a, st, `{"model":"m","message":{"role":"assistant","content":"x"},"done":false}`)
	events, _ := feed(t, a, st, `{"model":"m","done":true,"done_reason":"stop"}`)
	var sawDelta bool
	for _, e := range events {
		if e.Type == "message_delta" {
			sawDelta = true
			if e.Usage != nil {
				t.Fatalf("无 usage 终帧不得发零值 usage: %+v", e.Usage)
			}
		}
	}
	if !sawDelta {
		t.Fatalf("终帧应发 message_delta(承载 stop_reason): %v", eventTypes(events))
	}
}

// TestPostTerminalFramesDroppedWithLoss 抓的回归:done:true 之后的迟到帧再进
// canonical 流(message_stop 已发,继续吐 delta 破坏客户端事件序)。
// Mutation:删 frameToCanonicalEvents 入口的 Terminated 守卫 → 本测试红。
func TestPostTerminalFramesDroppedWithLoss(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	feed(t, a, st, `{"model":"m","message":{"role":"assistant","content":"hi"},"done":false}`)
	feed(t, a, st, `{"model":"m","done":true,"done_reason":"stop","prompt_eval_count":1,"eval_count":1}`)

	late, losses := feed(t, a, st, `{"model":"m","message":{"role":"assistant","content":"late"},"done":false}`)
	if len(late) != 0 {
		t.Fatalf("done 后的迟到帧不应再发事件: %v", eventTypes(late))
	}
	if len(losses) != 1 {
		t.Fatalf("迟到帧应记恰一条 loss: %+v", losses)
	}
	dup, losses2 := feed(t, a, st, `{"model":"m","done":true,"done_reason":"stop"}`)
	if len(dup) != 0 {
		t.Fatalf("二次 done 帧不应重复发终止事件: %v", eventTypes(dup))
	}
	if len(losses2) != 1 {
		t.Fatalf("二次 done 帧应记一条 loss: %+v", losses2)
	}
}

// TestDoneThenFinalizeDeduplicates 抓的回归:done 终帧与 EOF Finalize 双触发
// 各发一次 message_stop(下游重复终止、usage 双记)。
func TestDoneThenFinalizeDeduplicates(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	feed(t, a, st, `{"model":"m","message":{"role":"assistant","content":"hi"},"done":false}`)
	feed(t, a, st, `{"model":"m","done":true,"done_reason":"stop","prompt_eval_count":2,"eval_count":3}`)

	final, err := a.FinalizeUpstreamStream(context.Background(), st)
	if err != nil {
		t.Fatalf("FinalizeUpstreamStream: %v", err)
	}
	if len(final) != 0 {
		t.Fatalf("done 后 Finalize 不得重复发事件(双触发去重): %v", final)
	}
}

// TestFinalizeWithoutDoneSynthesizesStop 抓的回归:上游 EOF 无 done 终帧时不
// 补 block stop/message_stop,下游悬挂等终止帧。
func TestFinalizeWithoutDoneSynthesizesStop(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	feed(t, a, st, `{"model":"m","message":{"role":"assistant","content":"partial"},"done":false}`)

	final, err := a.FinalizeUpstreamStream(context.Background(), st)
	if err != nil {
		t.Fatalf("FinalizeUpstreamStream: %v", err)
	}
	types := make([]string, 0, len(final))
	for _, e := range final {
		types = append(types, e.(proto.CanonicalEvent).Type)
	}
	want := []string{"content_block_stop", "message_stop"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("EOF 收尾事件=%v want %v", types, want)
	}
}

// TestErrorFrameFailsLoud 抓的回归:Ollama 中流 {"error":...} 帧被静默吞成
// 空帧——客户端会拿到干净 message_stop 而真相是上游崩了(静默截断伪装成功,
// 劣于 malformed 处理)。结构良好的 error 帧必须 fail-loud 返回 error。
// Mutation:删 chunks.go 的 frame.Error 检查 → 本测试红。
func TestErrorFrameFailsLoud(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	events, losses, err := a.ProviderEventToCanonicalEvents(context.Background(), []byte(`{"error":"model runner has unexpectedly stopped"}`), st)
	if err == nil {
		t.Fatalf("error 帧必须 fail-loud,却得到 events=%v losses=%v err=nil", events, losses)
	}
	if !strings.Contains(err.Error(), "model runner has unexpectedly stopped") {
		t.Fatalf("error 信息应携带上游原文: %v", err)
	}

	// 非流式 200+error 体同款 fail-loud。
	if _, _, err := a.ProviderResponseToCanonical(context.Background(), []byte(`{"error":"out of memory"}`)); err == nil {
		t.Fatal("非流式 error 体必须 fail-loud")
	}
}

// TestDoneFrameWithContentKeepsText 抓的回归:done:true 终帧携带非空
// message.content 时文本被丢(Done 检查提前于 content 处理即丢终帧文本)。
func TestDoneFrameWithContentKeepsText(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	feed(t, a, st, `{"model":"m","message":{"role":"assistant","content":"hel"},"done":false}`)
	events, _ := feed(t, a, st, `{"model":"m","message":{"role":"assistant","content":"lo"},"done":true,"done_reason":"stop","prompt_eval_count":2,"eval_count":4}`)
	var sawTail bool
	for _, e := range events {
		if e.Type == "content_block_delta" && e.Delta != nil && e.Delta.Text == "lo" {
			sawTail = true
		}
	}
	if !sawTail {
		t.Fatalf("终帧携带的文本增量丢失: %v", eventTypes(events))
	}
}

// TestMalformedFrameRecordsLossNotError 抓的回归:malformed NDJSON 帧把整条流
// 打死(返回 error / panic)而非记 loss 跳过。
func TestMalformedFrameRecordsLossNotError(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	events, losses, err := a.ProviderEventToCanonicalEvents(context.Background(), []byte(`{"model":`), st)
	if err != nil {
		t.Fatalf("malformed 帧不应返回 error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("malformed 帧不应产出事件: %v", events)
	}
	if len(losses) != 1 {
		t.Fatalf("malformed 帧应记恰一条 loss: %+v", losses)
	}
}

// TestTolerantSentinels 抓的回归:空帧/[DONE]/EOF 哨兵导致报错或重复终止
// (NDJSON 本无 [DONE],容忍代理补发并按流结束处理)。
func TestTolerantSentinels(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	if events, losses := feed(t, a, st, "   "); len(events) != 0 || len(losses) != 0 {
		t.Fatalf("空帧应零输出: events=%v losses=%v", events, losses)
	}
	events, _ := feed(t, a, st, "[DONE]")
	if got := eventTypes(events); strings.Join(got, ",") != "message_stop" {
		t.Fatalf("[DONE] 应触发收尾 message_stop: %v", got)
	}
	again, _, err := a.ProviderEventToCanonicalEvents(context.Background(), StreamEnd{}, st)
	if err != nil || len(again) != 0 {
		t.Fatalf("重复哨兵应零输出: events=%v err=%v", again, err)
	}
}

// TestProviderResponseToCanonicalBlocking 抓的回归:非流式单 JSON 的 content/
// thinking/tool_calls/usage/done_reason 任一映射断链(text 丢=空响应,usage
// 丢=漏计费,thinking 混进 text=正文污染)。
func TestProviderResponseToCanonicalBlocking(t *testing.T) {
	a := &Adapter{}
	raw := []byte(`{"model":"llama3.2","message":{"role":"assistant","content":"Hello!","thinking":"hmm","tool_calls":[{"function":{"name":"lookup","arguments":{"q":"x"}}}]},"done":true,"done_reason":"length","prompt_eval_count":7,"eval_count":3}`)
	env, losses, err := a.ProviderResponseToCanonical(context.Background(), raw)
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("正常响应不应有 loss: %+v", losses)
	}
	if env == nil || env.BufferedResponse == nil {
		t.Fatal("必须返回带 BufferedResponse 的最小 envelope")
	}
	if env.Version != proto.HCSFVersion {
		t.Fatalf("Version=%q want %q", env.Version, proto.HCSFVersion)
	}
	resp := env.BufferedResponse
	if len(resp.Content) != 3 {
		t.Fatalf("Content 应为 thinking+text+tool_use 三块: %+v", resp.Content)
	}
	if resp.Content[0].Type != "thinking" || resp.Content[0].Thinking != "hmm" {
		t.Fatalf("thinking 块=%+v want Thinking=hmm", resp.Content[0])
	}
	if resp.Content[1].Type != "text" || resp.Content[1].Text != "Hello!" {
		t.Fatalf("text 块=%+v want Hello!", resp.Content[1])
	}
	tu := resp.Content[2]
	if tu.Type != "tool_use" || tu.Name != "lookup" || string(tu.Input) != `{"q":"x"}` || tu.CallID == "" {
		t.Fatalf("tool_use 块=%+v want lookup/{\"q\":\"x\"}/非空 CallID", tu)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 3 || resp.Usage.TotalTokens != 10 {
		t.Fatalf("usage=%+v want in=7 out=3 total=10", resp.Usage)
	}
	if resp.StopReason != proto.CanonicalStopMaxTokens {
		t.Fatalf("StopReason=%q want max_tokens(done_reason=length)", resp.StopReason)
	}

	if _, _, err := a.ProviderResponseToCanonical(context.Background(), []byte("not json")); err == nil {
		t.Fatal("malformed blocking 响应必须报错")
	}
}

// TestCanonicalToProviderRequestReturnsMarshalLosses 抓的回归:adapter 路径的
// marshal loss 没回传给调用方(只写图不返回=调用方损耗记账缺口)。
func TestCanonicalToProviderRequestReturnsMarshalLosses(t *testing.T) {
	a := &Adapter{}
	env := marshalEnv(textNode("n1", "user", "hi"))
	env.RequestControls.ToolChoice = []byte(`"auto"`)
	body, losses, err := a.CanonicalToProviderRequest(context.Background(), env)
	if err != nil {
		t.Fatalf("CanonicalToProviderRequest: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("body 为空")
	}
	if len(losses) != 1 || losses[0].Field != "tool_choice" {
		t.Fatalf("marshal loss 未回传: %+v", losses)
	}
}

// TestWrongStateTypeFailsLoud 抓的回归:state 类型分派错位(newUpstreamState
// 漏 case 回落别家 state)被静默吞——必须 fail-loud 报类型错。
func TestWrongStateTypeFailsLoud(t *testing.T) {
	a := &Adapter{}
	if _, _, err := a.ProviderEventToCanonicalEvents(context.Background(), []byte(`{}`), struct{}{}); err == nil {
		t.Fatal("错误 state 类型必须报错")
	}
	if _, err := a.FinalizeUpstreamStream(context.Background(), struct{}{}); err == nil {
		t.Fatal("错误 state 类型必须报错")
	}
}
