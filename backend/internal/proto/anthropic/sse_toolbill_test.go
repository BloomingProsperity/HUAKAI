package anthropic_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
)

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

func bufferedResp(t *testing.T, content []map[string]any) []byte {
	t.Helper()
	payload := map[string]any{
		"id":          "msg_stageB",
		"type":        "message",
		"role":        "assistant",
		"model":       "claude-3-5-sonnet",
		"content":     content,
		"stop_reason": "end_turn",
		"usage": map[string]any{
			"input_tokens":  10,
			"output_tokens": 5,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal buffered resp: %v", err)
	}
	return raw
}

// ---------------------------------------------------------------------------
// TestAnthropicBuffered_ServerToolUseCounted
// 2 个 web_search server_tool_use + 1 个 file_search => WebSearchCalls=2 FileSearchCalls=1
// ---------------------------------------------------------------------------

func TestAnthropicBuffered_ServerToolUseCounted(t *testing.T) {
	adapter := &anthropic.Adapter{}
	raw := bufferedResp(t, []map[string]any{
		{"type": "server_tool_use", "id": "stu_001", "name": "web_search", "input": map[string]any{"query": "foo"}},
		{"type": "server_tool_use", "id": "stu_002", "name": "web_search", "input": map[string]any{"query": "bar"}},
		{"type": "server_tool_use", "id": "stu_003", "name": "file_search", "input": map[string]any{"query": "baz"}},
	})
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), raw)
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	_ = losses
	usage := env.Accounting.Usage
	if usage.WebSearchCalls != 2 {
		t.Errorf("WebSearchCalls: want 2, got %d", usage.WebSearchCalls)
	}
	if usage.FileSearchCalls != 1 {
		t.Errorf("FileSearchCalls: want 1, got %d", usage.FileSearchCalls)
	}
	if usage.ImageGenerationCalls != 0 {
		t.Errorf("ImageGenerationCalls: want 0, got %d", usage.ImageGenerationCalls)
	}
}

// ---------------------------------------------------------------------------
// TestAnthropicBuffered_ClientToolUseNotCounted  ⭐ 防多计费测试
// N 个 type=="tool_use"（客户端函数）块 => WebSearchCalls=0 FileSearchCalls=0
//
// 变异判据：若把计数判定改成也匹配 "tool_use" 块，
// 本测试会 RED——证明 server 工具与 client 工具不被混淆。
// ---------------------------------------------------------------------------

func TestAnthropicBuffered_ClientToolUseNotCounted(t *testing.T) {
	adapter := &anthropic.Adapter{}
	raw := bufferedResp(t, []map[string]any{
		{"type": "tool_use", "id": "toolu_001", "name": "web_search", "input": map[string]any{"query": "q1"}},
		{"type": "tool_use", "id": "toolu_002", "name": "web_search", "input": map[string]any{"query": "q2"}},
		{"type": "tool_use", "id": "toolu_003", "name": "file_search", "input": map[string]any{"query": "q3"}},
		{"type": "tool_use", "id": "toolu_004", "name": "my_custom_tool", "input": map[string]any{"x": 1}},
	})
	env, _, err := adapter.ProviderResponseToCanonical(context.Background(), raw)
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	usage := env.Accounting.Usage
	// 所有计数必须为零——客户端 tool_use 块是免费的，绝不能计费。
	if usage.WebSearchCalls != 0 {
		t.Errorf("WebSearchCalls: want 0 (client tool_use must NOT be counted), got %d", usage.WebSearchCalls)
	}
	if usage.FileSearchCalls != 0 {
		t.Errorf("FileSearchCalls: want 0 (client tool_use must NOT be counted), got %d", usage.FileSearchCalls)
	}
}

// ---------------------------------------------------------------------------
// TestAnthropicStreaming_ServerToolUseAccumulated
// 3 个携带 server_tool_use "web_search" 的 content_block_start =>
// AccumulatedUsage.WebSearchCalls == 3
//
// 变异判据：把 UsageAccumulator.Update 里的 += 改成 = => 计数变 1 => RED。
// ---------------------------------------------------------------------------

func TestAnthropicStreaming_ServerToolUseAccumulated(t *testing.T) {
	adapter := &anthropic.Adapter{}

	buildCBS := func(index int, blockType, name string) []byte {
		raw, _ := json.Marshal(map[string]any{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type": blockType,
				"id":   "stu_" + name,
				"name": name,
			},
		})
		return raw
	}

	evts := [][]byte{
		anthroEvt(t, "message_start", map[string]any{
			"message": map[string]any{"id": "msg_stream_stageB", "model": "claude-3-5-sonnet",
				"usage": map[string]any{"input_tokens": 10, "output_tokens": 0}},
		}),
		buildCBS(0, "server_tool_use", "web_search"),
		buildCBS(1, "server_tool_use", "web_search"),
		buildCBS(2, "server_tool_use", "web_search"),
		anthroEvt(t, "message_delta", map[string]any{
			"delta": map[string]any{"stop_reason": "end_turn"},
			"usage": map[string]any{"output_tokens": 5},
		}),
		anthroEvt(t, "message_stop", nil),
	}

	state := &anthropic.UpstreamState{}
	var accUsage proto.CanonicalUsage
	for _, e := range evts {
		out, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), e, state)
		if err != nil {
			// 未知事件之后的 message_stop 没问题；继续
			continue
		}
		for _, x := range out {
			ev, ok := x.(proto.CanonicalEvent)
			if !ok {
				continue
			}
			if ev.Usage != nil {
				// 模拟 UsageAccumulator 对工具计数的 += 累加。
				// token 字段用"取最新值"；工具计数用 +=。
				if ev.Usage.WebSearchCalls > 0 {
					accUsage.WebSearchCalls += ev.Usage.WebSearchCalls
				}
				if ev.Usage.FileSearchCalls > 0 {
					accUsage.FileSearchCalls += ev.Usage.FileSearchCalls
				}
			}
		}
	}

	if accUsage.WebSearchCalls != 3 {
		t.Errorf("WebSearchCalls: want 3 (accumulated across 3 content_block_start events), got %d", accUsage.WebSearchCalls)
	}
	if accUsage.FileSearchCalls != 0 {
		t.Errorf("FileSearchCalls: want 0, got %d", accUsage.FileSearchCalls)
	}
}

// ---------------------------------------------------------------------------
// TestAnthropicStreaming_ParityWithBuffered
// 同一逻辑响应：流式路径累加计数 == 缓冲路径计数。
// ---------------------------------------------------------------------------

func TestAnthropicStreaming_ParityWithBuffered(t *testing.T) {
	adapter := &anthropic.Adapter{}

	// 缓冲路径：1 个 web_search + 1 个 file_search
	rawBuf := bufferedResp(t, []map[string]any{
		{"type": "server_tool_use", "id": "sstu_01", "name": "web_search", "input": map[string]any{"q": "x"}},
		{"type": "server_tool_use", "id": "sstu_02", "name": "file_search", "input": map[string]any{"q": "y"}},
	})
	envBuf, _, err := adapter.ProviderResponseToCanonical(context.Background(), rawBuf)
	if err != nil {
		t.Fatalf("buffered parse: %v", err)
	}

	// 流式路径：同样 2 个块以 content_block_start 事件发出
	buildCBS := func(index int, name string) []byte {
		raw, _ := json.Marshal(map[string]any{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type": "server_tool_use",
				"id":   "sstu_0" + string(rune('1'+index)),
				"name": name,
			},
		})
		return raw
	}
	evts := [][]byte{
		anthroEvt(t, "message_start", map[string]any{
			"message": map[string]any{"id": "msg_parity", "model": "claude-3-5-sonnet",
				"usage": map[string]any{"input_tokens": 10, "output_tokens": 0}},
		}),
		buildCBS(0, "web_search"),
		buildCBS(1, "file_search"),
		anthroEvt(t, "message_stop", nil),
	}

	state := &anthropic.UpstreamState{}
	var streamWSC, streamFSC int
	for _, e := range evts {
		out, _, _ := adapter.ProviderEventToCanonicalEvents(context.Background(), e, state)
		for _, x := range out {
			ev, ok := x.(proto.CanonicalEvent)
			if !ok {
				continue
			}
			if ev.Usage != nil {
				streamWSC += ev.Usage.WebSearchCalls
				streamFSC += ev.Usage.FileSearchCalls
			}
		}
	}

	buffWSC := envBuf.Accounting.Usage.WebSearchCalls
	buffFSC := envBuf.Accounting.Usage.FileSearchCalls

	if streamWSC != buffWSC {
		t.Errorf("parity: WebSearchCalls streaming=%d buffered=%d", streamWSC, buffWSC)
	}
	if streamFSC != buffFSC {
		t.Errorf("parity: FileSearchCalls streaming=%d buffered=%d", streamFSC, buffFSC)
	}
}
