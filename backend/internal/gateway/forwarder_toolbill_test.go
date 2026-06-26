package gateway

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// TestUsageAccumulator_ToolCallsAccumulate 验证 WebSearchCalls /
// FileSearchCalls / ImageGenerationCalls 在多次 Update 调用间是被累加（+=）的，
// 而不是被覆盖。
//
// 变异：把 Update 里的 += 改成 = => 三次调用后计数变成 1 => 变红。
func TestUsageAccumulator_ToolCallsAccumulate(t *testing.T) {
	var acc UsageAccumulator

	acc.Update(UsageSourceReported, proto.CanonicalUsage{WebSearchCalls: 1})
	acc.Update(UsageSourceReported, proto.CanonicalUsage{WebSearchCalls: 1})
	acc.Update(UsageSourceReported, proto.CanonicalUsage{WebSearchCalls: 1})

	if acc.Usage.WebSearchCalls != 3 {
		t.Errorf("WebSearchCalls: want 3 (accumulated), got %d — += must be used, not =", acc.Usage.WebSearchCalls)
	}

	acc.Update(UsageSourceReported, proto.CanonicalUsage{FileSearchCalls: 1})
	acc.Update(UsageSourceReported, proto.CanonicalUsage{FileSearchCalls: 1})

	if acc.Usage.FileSearchCalls != 2 {
		t.Errorf("FileSearchCalls: want 2 (accumulated), got %d", acc.Usage.FileSearchCalls)
	}

	acc.Update(UsageSourceReported, proto.CanonicalUsage{ImageGenerationCalls: 1})

	if acc.Usage.ImageGenerationCalls != 1 {
		t.Errorf("ImageGenerationCalls: want 1, got %d", acc.Usage.ImageGenerationCalls)
	}
}

// TestUsageAccumulator_ToolCallsRespectTerminalLock 验证一旦调用 Freeze()，
// 后续的 Update 就不再累加工具调用计数。
func TestUsageAccumulator_ToolCallsRespectTerminalLock(t *testing.T) {
	var acc UsageAccumulator

	acc.Update(UsageSourceReported, proto.CanonicalUsage{WebSearchCalls: 2})
	acc.Freeze()
	acc.Update(UsageSourceReported, proto.CanonicalUsage{WebSearchCalls: 5})

	if acc.Usage.WebSearchCalls != 2 {
		t.Errorf("WebSearchCalls after Freeze: want 2 (locked), got %d", acc.Usage.WebSearchCalls)
	}
}

// TestUsageAccumulator_TokensStillSetToLatest 验证针对工具计数的 += 改动不会
// 破坏 token 既有的"取最新值"行为。
func TestUsageAccumulator_TokensStillSetToLatest(t *testing.T) {
	var acc UsageAccumulator

	acc.Update(UsageSourceReported, proto.CanonicalUsage{InputTokens: 10, OutputTokens: 5})
	acc.Update(UsageSourceReported, proto.CanonicalUsage{InputTokens: 20, OutputTokens: 8})

	if acc.Usage.InputTokens != 20 {
		t.Errorf("InputTokens: want 20 (set-to-latest), got %d", acc.Usage.InputTokens)
	}
	if acc.Usage.OutputTokens != 8 {
		t.Errorf("OutputTokens: want 8 (set-to-latest), got %d", acc.Usage.OutputTokens)
	}
}
