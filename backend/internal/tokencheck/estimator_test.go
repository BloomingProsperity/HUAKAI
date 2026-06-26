package tokencheck

import (
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestHeuristicEstimatorEmpty(t *testing.T) {
	got := HeuristicEstimator{}.Estimate(nil)
	if got != 0 {
		t.Fatalf("empty blocks estimated %d, want 0", got)
	}
}

func TestNoopEstimatorDisabled(t *testing.T) {
	blocks := []proto.CanonicalContentBlock{{Type: "text", Text: "hello 世界"}}
	got := NoopEstimator{}.Estimate(blocks)
	if got != 0 {
		t.Fatalf("noop estimated %d, want 0", got)
	}
}

func TestHeuristicEstimatorEnglishApprox(t *testing.T) {
	blocks := []proto.CanonicalContentBlock{{Type: "text", Text: "hello world from huakai"}}
	got := HeuristicEstimator{}.Estimate(blocks)
	if got < 5 || got > 8 {
		t.Fatalf("english estimate %d outside expected range", got)
	}
}

func TestHeuristicEstimatorChineseApprox(t *testing.T) {
	blocks := []proto.CanonicalContentBlock{{Type: "text", Text: "你好世界你好世界"}}
	got := HeuristicEstimator{}.Estimate(blocks)
	if got < 6 || got > 9 {
		t.Fatalf("chinese estimate %d outside expected range", got)
	}
}

func TestHeuristicEstimatorMixedChineseEnglish(t *testing.T) {
	blocks := []proto.CanonicalContentBlock{{Type: "text", Text: "hello 世界 token 校验"}}
	got := HeuristicEstimator{}.Estimate(blocks)
	if got < 6 || got > 10 {
		t.Fatalf("mixed estimate %d outside expected range", got)
	}
}

func TestHeuristicEstimatorLongText(t *testing.T) {
	blocks := []proto.CanonicalContentBlock{{Type: "text", Text: strings.Repeat("abcd", 300)}}
	got := HeuristicEstimator{}.Estimate(blocks)
	if got < 290 || got > 320 {
		t.Fatalf("long text estimate %d outside expected range", got)
	}
}

func TestHeuristicEstimatorThinkingText(t *testing.T) {
	blocks := []proto.CanonicalContentBlock{{Thinking: "visible thinking text from Anthropic"}}
	got := HeuristicEstimator{}.Estimate(blocks)
	// 变异守卫:若不估算 block.Thinking,仅含 thinking 的 block 会估算为 0 -> RED。
	if got <= 0 {
		t.Fatalf("thinking estimate %d, want positive", got)
	}
}

func TestHeuristicEstimatorToolJSON(t *testing.T) {
	blocks := []proto.CanonicalContentBlock{{
		Type:  "tool_use",
		Name:  "weather",
		Input: []byte(`{"city":"杭州","unit":"celsius"}`),
	}}
	got := HeuristicEstimator{}.Estimate(blocks)
	if got < 8 {
		t.Fatalf("tool json estimate %d too small", got)
	}
}

// TestEstimateStreamDelta 验证 流式可见输出估算计入可见文本 + 工具参数增量字节。
// 隐藏 reasoning 不经此函数(由调用方排除 ReasoningText),故此处只验 text + partialJSON 两路均计入。
func TestEstimateStreamDelta(t *testing.T) {
	textOnly := EstimateStreamDelta("hello world answer", nil)
	if textOnly <= 0 {
		t.Fatalf("text-only delta estimate=%d want positive", textOnly)
	}
	// MUTATION: EstimateStreamDelta 漏算 toolPartialJSON → withJSON == textOnly → RED。
	withJSON := EstimateStreamDelta("hello world answer", []byte(`{"city":"hangzhou","unit":"celsius","extra":"padding-bytes"}`))
	if withJSON <= textOnly {
		t.Fatalf("delta with tool partial JSON=%d want > text-only %d (partial JSON must count)", withJSON, textOnly)
	}
}
