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
