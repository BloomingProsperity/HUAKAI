package anthropic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestProviderResponseToCanonical_NestedThinkingTokens(t *testing.T) {
	adapter := &Adapter{}
	env, _, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_th",
		"type":"message",
		"role":"assistant",
		"model":"claude-opus-4-7",
		"content":[{"type":"thinking","thinking":"","signature":"sig_omit"},{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{
			"input_tokens":9,
			"output_tokens":30,
			"output_tokens_details":{"thinking_tokens":11}
		}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	u := env.BufferedResponse.Usage
	if !u.ThinkingTokensKnown || u.ReasoningTokens != 11 || u.OutputTokens != 30 {
		t.Fatalf("必须收下嵌套思考分解且不改包容性输出: %+v", u)
	}
	if env.Accounting.Usage.ReasoningTokens != 11 || !env.Accounting.Usage.ThinkingTokensKnown {
		t.Fatalf("账务用量必须带同一分解: %+v", env.Accounting.Usage)
	}
	if len(env.BufferedResponse.Content) < 1 || env.BufferedResponse.Content[0].Type != "thinking" || env.BufferedResponse.Content[0].Signature != "sig_omit" {
		t.Fatalf("空思考块必须保留: %+v", env.BufferedResponse.Content)
	}
}

func TestProviderResponseToCanonical_ExplicitZeroThinkingTokens(t *testing.T) {
	adapter := &Adapter{}
	env, _, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_zero",
		"type":"message",
		"role":"assistant",
		"model":"claude-opus-4-7",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{
			"input_tokens":3,
			"output_tokens":5,
			"output_tokens_details":{"thinking_tokens":0}
		}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	u := env.BufferedResponse.Usage
	if !u.ThinkingTokensKnown || u.ReasoningTokens != 0 {
		t.Fatalf("明确 0 与字段缺失必须可分: %+v", u)
	}
}

func TestProviderResponseToCanonical_MissingThinkingDetailsUnknown(t *testing.T) {
	adapter := &Adapter{}
	env, _, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_miss",
		"type":"message",
		"role":"assistant",
		"model":"claude-opus-4-7",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":3,"output_tokens":5}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if env.BufferedResponse.Usage.ThinkingTokensKnown {
		t.Fatalf("缺分解不得合成 known: %+v", env.BufferedResponse.Usage)
	}
}

func TestMergeUsage_PreservesExplicitZeroThinkingTokens(t *testing.T) {
	base := proto.CanonicalUsage{InputTokens: 4, OutputTokens: 1}
	delta := proto.CanonicalUsage{OutputTokens: 6, ReasoningTokens: 0, ThinkingTokensKnown: true}
	got := mergeUsage(base, proto.CanonicalUsage{}, delta)
	if !got.ThinkingTokensKnown || got.ReasoningTokens != 0 || got.OutputTokens != 6 || got.InputTokens != 4 {
		t.Fatalf("终态明确 0 不得被非零合并抹掉: %+v", got)
	}
}

func TestLegacyTopLevelThinkingTokensAccepted(t *testing.T) {
	adapter := &Adapter{}
	env, _, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_legacy",
		"type":"message",
		"role":"assistant",
		"model":"claude-opus-4-7",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":1,"output_tokens":8,"thinking_tokens":3}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if !env.BufferedResponse.Usage.ThinkingTokensKnown || env.BufferedResponse.Usage.ReasoningTokens != 3 {
		t.Fatalf("遗留顶层别名可收，但不得只靠它: %+v", env.BufferedResponse.Usage)
	}
	raw, _ := json.Marshal(env.BufferedResponse.Usage)
	if strings.Contains(string(raw), "should-not") {
		t.Fatal("noop")
	}
}
