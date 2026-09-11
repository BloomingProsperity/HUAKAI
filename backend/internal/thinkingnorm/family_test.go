package thinkingnorm

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestClassifyReplayUsesAccountVendorOnly(t *testing.T) {
	if got := ClassifyReplay(""); got != FamilyUnknown {
		t.Fatalf("empty vendor=%q want unknown", got)
	}
	if got := ClassifyReplay("Anthropic"); got != FamilyOfficial {
		t.Fatalf("anthropic=%q want official", got)
	}
	if got := ClassifyReplay("bedrock"); got != FamilyOfficial {
		t.Fatalf("bedrock=%q want official", got)
	}
	if got := ClassifyReplay("deepseek"); got != FamilyCompat {
		t.Fatalf("deepseek=%q want compat", got)
	}
	if got := ClassifyReplay("minimax"); got != FamilyCompat {
		t.Fatalf("minimax=%q want compat", got)
	}
	// 变异：若按客户端自报 claude-* 模型名分族，本用例不应看模型名。
	if got := ClassifyReplay("moonshot"); got == FamilyOfficial {
		t.Fatal("非官方 vendor 不得因模型名被判官方")
	}
}

func TestDropUnsignedHistoryOfficialOnly(t *testing.T) {
	unsigned := proto.CapabilityNode{
		Kind: proto.CapabilityThinking,
		Thinking: &proto.ThinkingNode{
			Redaction: proto.RedactionPublic,
			Blocks:    []proto.CanonicalContentBlock{{Type: "thinking", Thinking: "plan"}},
		},
	}
	signed := proto.CapabilityNode{
		Kind: proto.CapabilityThinking,
		Thinking: &proto.ThinkingNode{
			Redaction: proto.RedactionPublic,
			Signature: "sig_keep",
			Blocks:    []proto.CanonicalContentBlock{{Type: "thinking", Thinking: "plan", Signature: "sig_keep"}},
		},
	}
	control := proto.CapabilityNode{
		Kind:     proto.CapabilityThinking,
		Source:   &proto.NodeSourceRef{RequestField: "thinking"},
		Thinking: &proto.ThinkingNode{Mode: "enabled", BudgetTokens: 1024, Redaction: proto.RedactionPublic},
	}
	if !DropUnsignedHistory("anthropic", unsigned) {
		t.Fatal("官方族必须剥无签名历史")
	}
	if DropUnsignedHistory("anthropic", signed) {
		t.Fatal("官方族不得剥已签名历史")
	}
	if DropUnsignedHistory("anthropic", control) {
		t.Fatal("官方族不得剥顶层思考控制")
	}
	if DropUnsignedHistory("deepseek", unsigned) {
		t.Fatal("兼容族不得剥无签名历史")
	}
	if DropUnsignedHistory("", unsigned) {
		t.Fatal("未知族不得猜成官方可剥")
	}
}
