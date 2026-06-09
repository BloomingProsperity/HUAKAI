package gateway

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// TestUsageAccumulator_ToolCallsAccumulate verifies that WebSearchCalls /
// FileSearchCalls / ImageGenerationCalls are ACCUMULATED (+=) across Update
// calls, not overwritten.
//
// MUTATION: change += to = in Update => count becomes 1 after 3 calls => RED.
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

// TestUsageAccumulator_ToolCallsRespectTerminalLock verifies that once
// Freeze() is called, subsequent Updates do NOT accumulate tool counts.
func TestUsageAccumulator_ToolCallsRespectTerminalLock(t *testing.T) {
	var acc UsageAccumulator

	acc.Update(UsageSourceReported, proto.CanonicalUsage{WebSearchCalls: 2})
	acc.Freeze()
	acc.Update(UsageSourceReported, proto.CanonicalUsage{WebSearchCalls: 5})

	if acc.Usage.WebSearchCalls != 2 {
		t.Errorf("WebSearchCalls after Freeze: want 2 (locked), got %d", acc.Usage.WebSearchCalls)
	}
}

// TestUsageAccumulator_TokensStillSetToLatest verifies that the += change for
// tool counts does NOT break the existing set-to-latest behaviour for tokens.
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
