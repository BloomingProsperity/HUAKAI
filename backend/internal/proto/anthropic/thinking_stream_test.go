package anthropic_test

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
)

func canonicalEventAt(t *testing.T, events []any, i int) proto.CanonicalEvent {
	t.Helper()
	if i >= len(events) {
		t.Fatalf("events=%d 取 %d 越界", len(events), i)
	}
	switch v := events[i].(type) {
	case proto.CanonicalEvent:
		return v
	case *proto.CanonicalEvent:
		return *v
	default:
		t.Fatalf("event[%d] 类型 %T 非 CanonicalEvent", i, events[i])
		return proto.CanonicalEvent{}
	}
}

// 判别测试:Claude 上游流式 thinking / redacted_thinking / server_tool_use 的
// content_block_start 必须保留真实块类型——此前被折成 {Type:"unknown"}+LOSSY,
// Claude SDK 客户端经中转开 extended thinking 收到非法块类型,thinking 输出
// 整条损坏(delta 早已正常转 reasoning_delta,只有 start 块断)。
// Mutation guard: canonicalBlock 改回折叠 → 对应子断言红。
func TestAnthropicStream_BlockStartPreservesThinkingAndServerToolUse(t *testing.T) {
	adapter := &anthropic.Adapter{}

	cases := []struct {
		name     string
		payload  string
		wantType string
	}{
		{"thinking", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`, "thinking"},
		{"redacted_thinking", `{"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"b3BhcXVl"}}`, "redacted_thinking"},
		{"server_tool_use", `{"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","id":"srvtoolu_x1","name":"web_search","input":{}}}`, "server_tool_use"},
	}
	for _, tc := range cases {
		state := &anthropic.UpstreamState{}
		if _, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(),
			[]byte(`{"type":"message_start","message":{"id":"msg_t","model":"claude-opus","usage":{"input_tokens":3,"output_tokens":0}}}`), state); err != nil {
			t.Fatalf("%s message_start: %v", tc.name, err)
		}
		events, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(tc.payload), state)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for _, l := range losses {
			if l.Verdict == proto.VerdictLossy {
				t.Fatalf("%s 不得再被 LOSSY 折叠: %+v", tc.name, l)
			}
		}
		ev := canonicalEventAt(t, events, 0)
		if ev.ContentBlock == nil {
			t.Fatalf("%s: ContentBlock nil", tc.name)
		}
		if ev.ContentBlock.Type != tc.wantType {
			t.Fatalf("%s 块类型=%q want %q(折叠回归)", tc.name, ev.ContentBlock.Type, tc.wantType)
		}
	}
}
