// bedrock_eventstream_test.go — A4 单测：BedrockEventStreamAdapter
//
// 测试策略：
//   - 直接喂 Anthropic 事件 JSON byte slice（与 A3 scanner 实际产出形态一致）
//   - 校验 CanonicalEvent 输出与 AnthropicAdapter 等价（因为 Bedrock-on-
//     Anthropic 的 inner JSON 就是 native Anthropic 格式）
//   - 跨适配器一致性 fixture：相同 raw JSON 喂给 AnthropicAdapter 与
//     BedrockEventStreamAdapter，输出必须等价（语义同构）
package proto

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// fixture 顺序：message_start → content_block_start(text) → content_block_delta
// (text) → content_block_stop → message_delta(usage+stop_reason) → message_stop
var bedrockHappyFixture = []string{
	`{"type":"message_start","message":{"id":"msg_01abc","model":"claude-3-7-sonnet-20250219","usage":{"input_tokens":42,"output_tokens":0}}}`,
	`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
	`{"type":"content_block_stop","index":0}`,
	`{"type":"message_delta","delta":{"stop_reason":"end_turn","usage":{"output_tokens":7}},"usage":{"input_tokens":42,"output_tokens":7}}`,
	`{"type":"message_stop"}`,
}

func feedAdapter(t *testing.T, ad UpstreamAdapter, fixtures []string) ([]CanonicalEvent, []ProtocolLossEntry) {
	t.Helper()
	state := &UpstreamState{}
	var allEvents []CanonicalEvent
	var allLosses []ProtocolLossEntry
	for i, raw := range fixtures {
		evts, losses, err := ad.ProviderEventToCanonicalEvents(context.Background(), []byte(raw), state)
		if err != nil {
			t.Fatalf("[%d] err=%v raw=%s", i, err, raw)
		}
		for _, e := range evts {
			ce, ok := e.(CanonicalEvent)
			if !ok {
				t.Fatalf("[%d] non-CanonicalEvent: %T", i, e)
			}
			allEvents = append(allEvents, ce)
		}
		allLosses = append(allLosses, losses...)
	}
	return allEvents, allLosses
}

func TestBedrockAdapter_HappyPath_EquivalentToAnthropic(t *testing.T) {
	bedrock := NewBedrockEventStreamAdapter()
	anthropic := &AnthropicAdapter{}

	bEvents, bLosses := feedAdapter(t, bedrock, bedrockHappyFixture)
	aEvents, aLosses := feedAdapter(t, anthropic, bedrockHappyFixture)

	if !reflect.DeepEqual(bEvents, aEvents) {
		bjs, _ := json.MarshalIndent(bEvents, "", "  ")
		ajs, _ := json.MarshalIndent(aEvents, "", "  ")
		t.Fatalf("BedrockAdapter ≠ AnthropicAdapter\nbedrock=%s\nanthropic=%s", bjs, ajs)
	}
	if !reflect.DeepEqual(bLosses, aLosses) {
		t.Fatalf("losses differ: bedrock=%+v anthropic=%+v", bLosses, aLosses)
	}
	if len(bEvents) != 6 {
		t.Fatalf("event count=%d want 6", len(bEvents))
	}
	if bEvents[0].Type != "message_start" || bEvents[0].MessageID != "msg_01abc" {
		t.Errorf("event[0]=%+v", bEvents[0])
	}
	if bEvents[5].Type != "message_stop" {
		t.Errorf("event[5]=%+v", bEvents[5])
	}
}

func TestBedrockAdapter_ToolUseBlock(t *testing.T) {
	ad := NewBedrockEventStreamAdapter()
	state := &UpstreamState{}
	raw := []byte(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_abc","name":"get_weather","input":{}}}`)
	evts, _, err := ad.ProviderEventToCanonicalEvents(context.Background(), raw, state)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("evts=%d", len(evts))
	}
	ce := evts[0].(CanonicalEvent)
	if ce.ContentBlock == nil || ce.ContentBlock.Type != "tool_use" {
		t.Fatalf("content_block=%+v", ce.ContentBlock)
	}
	if ce.ContentBlock.Name != "get_weather" {
		t.Errorf("name=%q", ce.ContentBlock.Name)
	}
}

func TestBedrockAdapter_SignatureDeltaSkippedByDefault(t *testing.T) {
	ad := NewBedrockEventStreamAdapter()
	state := &UpstreamState{}
	raw := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_xyz"}}`)
	evts, losses, err := ad.ProviderEventToCanonicalEvents(context.Background(), raw, state)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(evts) != 0 {
		t.Fatalf("signature_delta 默认应 skip，得 %d 事件", len(evts))
	}
	if len(losses) != 1 || losses[0].Verdict != VerdictLossy {
		t.Fatalf("losses=%+v", losses)
	}
}

func TestBedrockAdapter_SignatureDeltaCarriedWhenEnabled(t *testing.T) {
	ad := &BedrockEventStreamAdapter{CarryForwardSignatureDelta: true}

	state := &UpstreamState{}
	raw := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_xyz"}}`)
	evts, _, err := ad.ProviderEventToCanonicalEvents(context.Background(), raw, state)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("CarryForwardSignatureDelta=true 应保留事件，得 %d", len(evts))
	}
	ce := evts[0].(CanonicalEvent)
	if ce.Delta == nil || ce.Delta.Signature != "sig_xyz" {
		t.Errorf("delta=%+v", ce.Delta)
	}
}

func TestBedrockAdapter_UnknownEventType(t *testing.T) {
	ad := NewBedrockEventStreamAdapter()
	state := &UpstreamState{}
	raw := []byte(`{"type":"future_event_not_yet_mapped","data":{"some":"value"}}`)
	_, losses, err := ad.ProviderEventToCanonicalEvents(context.Background(), raw, state)
	if !errors.Is(err, ErrUnknownEventType) {
		t.Fatalf("err=%v want ErrUnknownEventType", err)
	}
	if len(losses) == 0 {
		t.Errorf("应有 LOSSY entry，得 %+v", losses)
	}
}

func TestBedrockAdapter_BadJSON(t *testing.T) {
	ad := NewBedrockEventStreamAdapter()
	state := &UpstreamState{}
	raw := []byte(`{not valid json`)
	_, _, err := ad.ProviderEventToCanonicalEvents(context.Background(), raw, state)
	if err == nil {
		t.Fatal("bad JSON 应报错")
	}
}

func TestBedrockAdapter_StateTypeError(t *testing.T) {
	ad := NewBedrockEventStreamAdapter()
	raw := []byte(`{"type":"message_stop"}`)
	_, _, err := ad.ProviderEventToCanonicalEvents(context.Background(), raw, "wrong type")
	if err == nil {
		t.Fatal("非 *UpstreamState state 应报错")
	}
}

func TestBedrockAdapter_FinalizeUnclosedBlocks(t *testing.T) {
	ad := NewBedrockEventStreamAdapter()
	state := &UpstreamState{}

	// 启动 2 block，不发 stop
	for _, raw := range []string{
		`{"type":"message_start","message":{"id":"x"}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text"}}`,
	} {
		_, _, err := ad.ProviderEventToCanonicalEvents(context.Background(), []byte(raw), state)
		if err != nil {
			t.Fatalf("setup err=%v", err)
		}
	}

	out, err := ad.FinalizeUpstreamStream(context.Background(), state)
	if err != nil {
		t.Fatalf("Finalize err=%v", err)
	}
	// 期望：2 个 content_block_stop + 1 个 message_stop = 3
	if len(out) != 3 {
		t.Fatalf("Finalize emit=%d want 3", len(out))
	}
	stops := 0
	terminal := 0
	for _, e := range out {
		ce, ok := e.(CanonicalEvent)
		if !ok {
			t.Fatalf("non-CanonicalEvent: %T", e)
		}
		switch ce.Type {
		case "content_block_stop":
			stops++
		case "message_stop":
			terminal++
		}
	}
	if stops != 2 || terminal != 1 {
		t.Errorf("stops=%d terminal=%d want 2/1", stops, terminal)
	}
}

func TestBedrockAdapter_ZeroValueStructStillWorks(t *testing.T) {
	// 验证：直接 struct literal 构造（inner=nil）也能用（防御性 lazy init）
	ad := &BedrockEventStreamAdapter{}
	state := &UpstreamState{}
	raw := []byte(`{"type":"message_stop"}`)
	evts, _, err := ad.ProviderEventToCanonicalEvents(context.Background(), raw, state)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("evts=%d", len(evts))
	}
}

// TestBedrockAdapter_ConcurrentRegistrySharing 验证多 goroutine 共享一个
// adapter 实例（registry 形态）+ 各自独立 UpstreamState 时无 data race。
// codex lane plan §Failure Modes #3 + §Testing Matrix #10.
func TestBedrockAdapter_ConcurrentRegistrySharing(t *testing.T) {
	ad := NewBedrockEventStreamAdapter() // shared
	const n = 64
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			state := &UpstreamState{}
			for _, raw := range bedrockHappyFixture {
				_, _, err := ad.ProviderEventToCanonicalEvents(context.Background(), []byte(raw), state)
				if err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-done; err != nil {
			t.Fatalf("goroutine err=%v", err)
		}
	}
}

func TestBedrockAdapter_MessageDeltaUsage(t *testing.T) {
	ad := NewBedrockEventStreamAdapter()
	state := &UpstreamState{}
	for _, raw := range []string{
		`{"type":"message_start","message":{"id":"m1","usage":{"input_tokens":10}}}`,
		`{"type":"message_delta","delta":{"stop_reason":"max_tokens","usage":{"output_tokens":5}},"usage":{"input_tokens":10,"output_tokens":5}}`,
	} {
		_, _, err := ad.ProviderEventToCanonicalEvents(context.Background(), []byte(raw), state)
		if err != nil {
			t.Fatalf("err=%v raw=%s", err, raw)
		}
	}
	// state 应累计 usage
	if state.AccumulatedUsage.InputTokens != 10 || state.AccumulatedUsage.OutputTokens != 5 {
		t.Errorf("accumulated usage=%+v want 10/5", state.AccumulatedUsage)
	}
}
