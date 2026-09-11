package upstreamcontract

import (
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestValidate_GPT6AstraRejectsNoneEffortAndSampling(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		ingress proto.ClientProtocol
		body    string
		want    error
	}{
		{
			name:    "responses 合法请求放行",
			ingress: proto.ClientProtocolOpenAIResponses,
			body:    `{"model":"gpt-6-astra","reasoning":{"effort":"high"},"input":"hi"}`,
		},
		{
			name:    "关闭推理必须拒绝",
			ingress: proto.ClientProtocolOpenAIResponses,
			body:    `{"model":"gpt-6-astra","reasoning":{"effort":"none"}}`,
			want:    ErrUnsupportedEffortNone,
		},
		{
			name:    "自定义温度必须拒绝",
			ingress: proto.ClientProtocolOpenAIChat,
			body:    `{"model":"gpt-6-astra","temperature":0.2,"messages":[{"role":"user","content":"hi"}]}`,
			want:    ErrUnsupportedSampling,
		},
		{
			name:    "chat 入口带工具必须拒绝",
			ingress: proto.ClientProtocolOpenAIChat,
			body:    `{"model":"gpt-6-astra","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"x"}}]}`,
			want:    ErrToolsRequireResponses,
		},
		{
			name:    "Responses 入口带工具放行",
			ingress: proto.ClientProtocolOpenAIResponses,
			body:    `{"model":"gpt-6-astra","tools":[{"type":"function","name":"x"}],"input":"hi"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.ingress, []string{"gpt-6-astra"}, []byte(tc.body))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Validate err=%v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate err=%v want %v", err, tc.want)
			}
		})
	}
}

func TestValidate_ClaudeFable51AlwaysOnThinking(t *testing.T) {
	t.Parallel()
	ok := `{"model":"claude-fable-5-1","max_tokens":128,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hi"}]}`
	if err := Validate(proto.ClientProtocolAnthropicMessages, []string{"claude-fable-5-1"}, []byte(ok)); err != nil {
		t.Fatalf("adaptive 请求应放行: %v", err)
	}
	if err := Validate(proto.ClientProtocolAnthropicMessages, []string{"claude-fable-5-1"}, []byte(
		`{"model":"claude-fable-5-1","thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hi"}]}`,
	)); !errors.Is(err, ErrAdaptiveThinkingLocked) {
		t.Fatalf("disabled thinking 应拒绝: %v", err)
	}
	if err := Validate(proto.ClientProtocolAnthropicMessages, []string{"claude-fable-5-1"}, []byte(
		`{"model":"claude-fable-5-1","thinking":{"type":"enabled","budget_tokens":2048},"messages":[{"role":"user","content":"hi"}]}`,
	)); !errors.Is(err, ErrAdaptiveThinkingLocked) {
		t.Fatalf("手动 budget 应拒绝: %v", err)
	}
	if err := Validate(proto.ClientProtocolAnthropicMessages, []string{"claude-fable-5-1"}, []byte(
		`{"model":"claude-fable-5-1","tool_choice":{"type":"any"},"messages":[{"role":"user","content":"hi"}]}`,
	)); !errors.Is(err, ErrForcedToolUse) {
		t.Fatalf("强制选工具应拒绝: %v", err)
	}
	if err := Validate(proto.ClientProtocolAnthropicMessages, []string{"claude-fable-5-1"}, []byte(
		`{"model":"claude-fable-5-1","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"prefill"}]}`,
	)); !errors.Is(err, ErrAssistantPrefill) {
		t.Fatalf("助手预填应拒绝: %v", err)
	}
}

func TestValidate_UnknownModelUnchanged(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"gpt-4o","temperature":0.2,"tools":[{"type":"function","function":{"name":"x"}}]}`)
	if err := Validate(proto.ClientProtocolOpenAIChat, []string{"gpt-4o"}, body); err != nil {
		t.Fatalf("无关模型不得被新合同误伤: %v", err)
	}
}

func TestFamilyDetection_PrefixAndSnapshot(t *testing.T) {
	t.Parallel()
	if !isGPT6Astra("GPT-6-Astra") || !isGPT6Astra("gpt-6-astra-2026-09-03") {
		t.Fatal("gpt-6-astra 族识别失败")
	}
	if !isClaudeFableAlwaysOn("claude-fable-5-1") || !isClaudeFableAlwaysOn("claude-mythos-5") {
		t.Fatal("fable/mythos 族识别失败")
	}
	if isGPT6Astra("gpt-5.6-sol") || isClaudeFableAlwaysOn("claude-opus-5") {
		t.Fatal("不得把相邻模型误判进更严合同")
	}
}
