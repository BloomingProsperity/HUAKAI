package dify

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

// TestMessageDeltasAccumulate 抓的回归:message 增量事件未先发
// message_start/content_block_start(下游 client adapter 收到孤儿 delta 即
// 弃流),或后续 delta 重复发 start 事件。
func TestMessageDeltasAccumulate(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}

	first, losses := feed(t, a, st, `{"event":"message","answer":"Hel","conversation_id":"conv-1"}`)
	if len(losses) != 0 {
		t.Fatalf("message 事件不应产生 loss: %+v", losses)
	}
	wantTypes := []string{"message_start", "content_block_start", "content_block_delta"}
	if got := eventTypes(first); strings.Join(got, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("首事件序列=%v want %v", got, wantTypes)
	}
	if first[0].MessageID != "conv-1" {
		t.Errorf("message_start.MessageID=%q want conv-1", first[0].MessageID)
	}
	if first[2].Delta == nil || first[2].Delta.Text != "Hel" {
		t.Fatalf("首 delta=%+v want text=Hel", first[2].Delta)
	}

	second, _ := feed(t, a, st, `{"event":"message","answer":"lo"}`)
	if len(second) != 1 || second[0].Type != "content_block_delta" {
		t.Fatalf("后续增量应只发 delta: %v", eventTypes(second))
	}
	if second[0].Delta.Text != "lo" {
		t.Fatalf("第二 delta text=%q want lo", second[0].Delta.Text)
	}
}

// TestAgentMessageSameAsMessage 抓的回归:agent_message(agent 编排同形增量)
// 被当成未知事件丢进 loss,agent 模式 app 整流无输出。
func TestAgentMessageSameAsMessage(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	events, losses := feed(t, a, st, `{"event":"agent_message","answer":"Hi","conversation_id":"conv-a"}`)
	if len(losses) != 0 {
		t.Fatalf("agent_message 不应产生 loss: %+v", losses)
	}
	want := []string{"message_start", "content_block_start", "content_block_delta"}
	if got := eventTypes(events); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("agent_message 事件序列=%v want %v", got, want)
	}
	if events[2].Delta.Text != "Hi" {
		t.Fatalf("delta text=%q want Hi", events[2].Delta.Text)
	}
}

// TestMessageEndEmitsUsageAndFinalizeDeduplicates 抓的回归:message_end 的
// usage 不进计费事件,或 message_end 与 EOF Finalize 双触发各发一次
// message_stop(下游重复终止、usage 双记)。
func TestMessageEndEmitsUsageAndFinalizeDeduplicates(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	feed(t, a, st, `{"event":"message","answer":"Hello","conversation_id":"conv-1"}`)

	events, losses := feed(t, a, st, `{"event":"message_end","metadata":{"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":16}}}`)
	if len(losses) != 0 {
		t.Fatalf("message_end 不应产生 loss: %+v", losses)
	}
	want := []string{"content_block_stop", "message_delta", "message_stop"}
	if got := eventTypes(events); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("message_end 事件序列=%v want %v", got, want)
	}
	delta := events[1]
	if delta.Usage == nil {
		t.Fatal("message_delta.Usage 为 nil(usage 丢失=计费丢失)")
	}
	if delta.Usage.InputTokens != 11 || delta.Usage.OutputTokens != 5 || delta.Usage.TotalTokens != 16 {
		t.Fatalf("usage=%+v want in=11 out=5 total=16", delta.Usage)
	}
	if delta.StopReason != proto.CanonicalStopEndTurn {
		t.Fatalf("stop_reason=%q want end_turn", delta.StopReason)
	}

	final, err := a.FinalizeUpstreamStream(context.Background(), st)
	if err != nil {
		t.Fatalf("FinalizeUpstreamStream: %v", err)
	}
	if len(final) != 0 {
		t.Fatalf("message_end 后 Finalize 不得重复发事件(双触发去重): %v", final)
	}
}

// TestFinalizeWithoutMessageEndSynthesizesStop 抓的回归:上游 EOF 无
// message_end 时不补 block stop/usage/message_stop,下游悬挂等终止帧。
func TestFinalizeWithoutMessageEndSynthesizesStop(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	feed(t, a, st, `{"event":"message","answer":"partial"}`)

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

// TestErrorEventFailsLoud 抓的回归:上游 error 事件被静默吞(返回空事件无
// error),客户端收到貌似正常的空流。
func TestErrorEventFailsLoud(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	events, losses, err := a.ProviderEventToCanonicalEvents(context.Background(), []byte(`{"event":"error","status":400,"code":"invalid_param","message":"query is required"}`), st)
	if err == nil {
		t.Fatal("error 事件必须返回 error(fail loud)")
	}
	if !strings.Contains(err.Error(), "invalid_param") || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("error 信息应携带 code 与 message: %v", err)
	}
	if len(events) != 0 || len(losses) != 0 {
		t.Fatalf("error 事件不应同时产出事件/loss: events=%v losses=%v", events, losses)
	}
}

// TestOrchestrationEventsDroppedWithLoss 抓的回归:workflow/node 编排事件被
// 误当文本下发,或被静默丢弃不记 loss。
func TestOrchestrationEventsDroppedWithLoss(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	for _, payload := range []string{
		`{"event":"workflow_started","workflow_run_id":"w1"}`,
		`{"event":"node_started","data":{"node_id":"n1"}}`,
		`{"event":"node_finished","data":{"node_id":"n1"}}`,
		`{"event":"totally_unknown"}`,
	} {
		events, losses := feed(t, a, st, payload)
		if len(events) != 0 {
			t.Errorf("编排事件 %q 不得产生 canonical 事件: %v", payload, eventTypes(events))
		}
		if len(losses) != 1 {
			t.Errorf("编排事件 %q 应记恰一条 loss: %+v", payload, losses)
		}
	}
}

// TestMalformedChunkRecordsLossNotError 抓的回归:malformed JSON 帧把整条流
// 打死(返回 error)而非记 loss 跳过。
func TestMalformedChunkRecordsLossNotError(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	events, losses, err := a.ProviderEventToCanonicalEvents(context.Background(), []byte(`{"event":`), st)
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

// TestEventNameReadFromDataJSONNotSSELine 抓的回归:事件分发误读 SSE event:
// 行而非 data JSON 的 event 字段——raw 帧直灌时两者可能不一致,必须以 JSON
// 为准。
func TestEventNameReadFromDataJSONNotSSELine(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	frame := "event: ping\ndata: {\"event\":\"message\",\"answer\":\"X\"}"
	events, losses := feed(t, a, st, frame)
	if len(losses) != 0 {
		t.Fatalf("不应产生 loss: %+v", losses)
	}
	if len(events) != 3 || events[2].Delta == nil || events[2].Delta.Text != "X" {
		t.Fatalf("应按 JSON event=message 解出 text_delta X: %v", eventTypes(events))
	}
}

// TestTolerantSentinels 抓的回归:空帧/[DONE]/EOF 哨兵导致报错或重复终止。
func TestTolerantSentinels(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	if events, losses := feed(t, a, st, "   "); len(events) != 0 || len(losses) != 0 {
		t.Fatalf("空帧应零输出: events=%v losses=%v", events, losses)
	}
	events, _ := feed(t, a, st, "[DONE]")
	types := eventTypes(events)
	if strings.Join(types, ",") != "message_stop" {
		t.Fatalf("[DONE] 应触发收尾 message_stop: %v", types)
	}
	// 已终止后再次收尾必须零输出。
	again, _, err := a.ProviderEventToCanonicalEvents(context.Background(), StreamEnd{}, st)
	if err != nil || len(again) != 0 {
		t.Fatalf("重复哨兵应零输出: events=%v err=%v", again, err)
	}
}

// TestProviderResponseToCanonicalBlocking 抓的回归:blocking 响应的 text/
// usage/stop/conversation_id 任一映射断链(text 丢=空响应,usage 丢=漏计费)。
func TestProviderResponseToCanonicalBlocking(t *testing.T) {
	a := &Adapter{}
	raw := []byte(`{"conversation_id":"conv-9","answer":"Hello!","metadata":{"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}}`)
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
	if resp.ID != "conv-9" {
		t.Errorf("ID=%q want conv-9", resp.ID)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "Hello!" {
		t.Fatalf("Content=%+v want 单 text 块 Hello!", resp.Content)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 3 || resp.Usage.TotalTokens != 10 {
		t.Fatalf("usage=%+v want in=7 out=3 total=10", resp.Usage)
	}
	if resp.StopReason != proto.CanonicalStopEndTurn {
		t.Fatalf("StopReason=%q want end_turn", resp.StopReason)
	}

	if _, _, err := a.ProviderResponseToCanonical(context.Background(), []byte("not json")); err == nil {
		t.Fatal("malformed blocking 响应必须报错")
	}
}

// TestPostTerminalEventsDroppedWithLoss 抓的回归:message_end 之后的事件
// (迟到 message 增量/二次 message_end)再进 canonical 流——message_stop 已
// 发,继续吐 delta/二次 stop 会破坏客户端事件序(openai 客户端可能见双
// [DONE])。post-terminal 一律丢弃+记 loss。
// Mutation:删 chunkToCanonicalEvents 入口的 Terminated 守卫 → 本测试红。
func TestPostTerminalEventsDroppedWithLoss(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	feed(t, a, st, `{"event":"message","answer":"hi","conversation_id":"c1"}`)
	feed(t, a, st, `{"event":"message_end","metadata":{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}}`)

	late, losses := feed(t, a, st, `{"event":"message","answer":"late","conversation_id":"c1"}`)
	if len(late) != 0 {
		t.Fatalf("message_end 后的迟到增量不应再发事件: %v", eventTypes(late))
	}
	if len(losses) != 1 {
		t.Fatalf("迟到事件应记一条 loss: %+v", losses)
	}
	dup, losses2 := feed(t, a, st, `{"event":"message_end"}`)
	if len(dup) != 0 {
		t.Fatalf("二次 message_end 不应重复发终止事件: %v", eventTypes(dup))
	}
	if len(losses2) != 1 {
		t.Fatalf("二次 message_end 应记一条 loss: %+v", losses2)
	}
}

// TestPingKeepAliveSilentlySkipped 抓的回归:ping 保活帧被当未知事件记 loss
// (长流账面噪音)或误产出事件。
func TestPingKeepAliveSilentlySkipped(t *testing.T) {
	a := &Adapter{}
	st := &UpstreamState{}
	events, losses := feed(t, a, st, `{"event":"ping"}`)
	if len(events) != 0 || len(losses) != 0 {
		t.Fatalf("ping 应静默跳过,events=%v losses=%+v", eventTypes(events), losses)
	}
}

// TestCanonicalToProviderRequestReturnsMarshalLosses 抓的回归:adapter 路径
// 的 marshal loss 没回传给调用方(只写图不返回=调用方损耗记账缺口)。
func TestCanonicalToProviderRequestReturnsMarshalLosses(t *testing.T) {
	a := &Adapter{}
	env := marshalEnv(textNode("n1", "user", "hi"))
	maxTokens := 8
	env.RequestControls.MaxTokens = &maxTokens
	body, losses, err := a.CanonicalToProviderRequest(context.Background(), env)
	if err != nil {
		t.Fatalf("CanonicalToProviderRequest: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("body 为空")
	}
	if len(losses) != 1 || losses[0].Field != "max_tokens" {
		t.Fatalf("marshal loss 未回传: %+v", losses)
	}
}
