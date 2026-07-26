package moderation

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExtractModerationInput_OnlyUsesUserTextAcrossProtocols(t *testing.T) {
	tests := []struct {
		name          string
		protocol      string
		body          string
		wantAll       string
		wantExcerpt   string
		forbiddenText string
	}{
		{
			name:     "OpenAI Chat",
			protocol: "openai_chat",
			body: `{"messages":[
				{"role":"system","content":"系统秘密"},
				{"role":"assistant","content":"模型输出"},
				{"role":"user","content":"第一条用户输入"},
				{"role":"user","content":[{"type":"text","text":"最后一条用户输入"},{"type":"image_url","image_url":{"url":"https://example.invalid/a"}}]}
			]}`,
			wantAll: "第一条用户输入\n最后一条用户输入", wantExcerpt: "最后一条用户输入",
			forbiddenText: "系统秘密",
		},
		{
			name:     "Anthropic Messages",
			protocol: "anthropic_messages",
			body: `{"system":"系统秘密","messages":[
				{"role":"assistant","content":[{"type":"text","text":"模型输出"}]},
				{"role":"user","content":[{"type":"text","text":"用户问题"},{"type":"tool_result","content":"工具结果"}]}
			]}`,
			wantAll: "用户问题", wantExcerpt: "用户问题", forbiddenText: "工具结果",
		},
		{
			name:     "OpenAI Responses",
			protocol: "openai_responses",
			body: `{"input":[
				{"type":"function_call_output","call_id":"1","output":"工具输出"},
				{"role":"assistant","content":[{"type":"output_text","text":"模型输出"}]},
				{"role":"user","content":[{"type":"input_text","text":"用户输入"}]}
			]}`,
			wantAll: "用户输入", wantExcerpt: "用户输入", forbiddenText: "工具输出",
		},
		{
			name:     "Gemini",
			protocol: "gemini",
			body: `{"contents":[
				{"role":"model","parts":[{"text":"模型输出"}]},
				{"role":"user","parts":[{"text":"用户输入一"},{"text":"用户输入二"}]}
			]}`,
			wantAll: "用户输入一\n用户输入二", wantExcerpt: "用户输入一\n用户输入二",
			forbiddenText: "模型输出",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractModerationInput(tc.protocol, []byte(tc.body))
			if err != nil {
				t.Fatalf("extractModerationInput: %v", err)
			}
			if got.AllText != tc.wantAll || got.Excerpt != tc.wantExcerpt {
				t.Fatalf("got=%+v want all=%q excerpt=%q", got, tc.wantAll, tc.wantExcerpt)
			}
			if strings.Contains(got.AllText, tc.forbiddenText) ||
				strings.Contains(got.Excerpt, tc.forbiddenText) {
				t.Fatalf("非用户内容进入审核文本: %+v", got)
			}
		})
	}
}

func TestExtractModerationInput_RejectsUnknownMalformedAndAssistantOnly(t *testing.T) {
	tests := []struct {
		protocol string
		body     string
	}{
		{protocol: "unknown", body: `{"messages":[{"role":"user","content":"hello"}]}`},
		{protocol: "openai_chat", body: `{"messages":`},
		{protocol: "openai_chat", body: `{"messages":[{"role":"assistant","content":"only model"}]}`},
		{protocol: "openai_responses", body: `{"input":[{"type":"function_call_output","output":"only tool"}]}`},
		{protocol: "gemini", body: `{"contents":[{"role":"model","parts":[{"text":"only model"}]}]}`},
	}
	for _, tc := range tests {
		if _, err := extractModerationInput(tc.protocol, []byte(tc.body)); !errors.Is(err, errModerationInput) {
			t.Fatalf("protocol=%s body=%s err=%v，期望 errModerationInput",
				tc.protocol, tc.body, err)
		}
	}
}

func TestExtractModerationInput_PreservesPureImageInputAcrossProtocols(t *testing.T) {
	const dataURL = "data:image/png;base64,aGVsbG8="
	tests := []struct {
		name     string
		protocol string
		body     string
		wantURL  string
	}{
		{
			name:     "OpenAI Chat",
			protocol: "openai_chat",
			body:     `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"` + dataURL + `"}}]}]}`,
			wantURL:  dataURL,
		},
		{
			name:     "Anthropic Messages",
			protocol: "anthropic_messages",
			body:     `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]}]}`,
			wantURL:  dataURL,
		},
		{
			name:     "OpenAI Responses",
			protocol: "openai_responses",
			body:     `{"input":[{"type":"input_image","image_url":"https://example.com/image.png"}]}`,
			wantURL:  "https://example.com/image.png",
		},
		{
			name:     "Gemini",
			protocol: "gemini",
			body:     `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]}]}`,
			wantURL:  dataURL,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractModerationInput(tc.protocol, []byte(tc.body))
			if err != nil {
				t.Fatalf("extractModerationInput: %v", err)
			}
			if got.AllText != "" || got.Excerpt != "" {
				t.Fatalf("纯图片不应伪造文本证据: %+v", got)
			}
			if len(got.ImageURLs) != 1 || got.ImageURLs[0] != tc.wantURL {
				t.Fatalf("ImageURLs=%v want [%s]", got.ImageURLs, tc.wantURL)
			}
		})
	}
}

func TestRedactModerationExcerpt_HidesRecognizedSecretsAndPersonalData(t *testing.T) {
	input := strings.Join([]string{
		"保留这段违规原文",
		"Authorization: Bearer secret-value",
		"api_key=hk_live_abcdefghijklmnopqrstuvwxyz123456",
		"token: eyJabc.def.ghi",
		"密码：do-not-store-this",
		"邮箱 user@example.com 电话 +86 138 0013 8000",
		"-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----",
		"AbCdEfGhIjKlMnOpQrStUvWxYz1234567890",
	}, "\n")

	got := redactModerationExcerpt(input)
	if !strings.Contains(got, "保留这段违规原文") {
		t.Fatalf("合法运营证据被全部删除: %q", got)
	}
	for _, secret := range []string{
		"secret-value", "hk_live_", "eyJabc", "do-not-store-this",
		"user@example.com", "138 0013 8000", "BEGIN PRIVATE KEY",
		"AbCdEfGhIjKlMnOpQrStUvWxYz1234567890",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("敏感值 %q 未脱敏: %q", secret, got)
		}
	}
}

func TestRedactModerationExcerpt_TruncatesByUnicodeRune(t *testing.T) {
	input := strings.Repeat("违", moderationExcerptRunes+20)
	got := redactModerationExcerpt(input)
	if utf8.RuneCountInString(got) != moderationExcerptRunes {
		t.Fatalf("runes=%d want %d", utf8.RuneCountInString(got), moderationExcerptRunes)
	}
	if !utf8.ValidString(got) {
		t.Fatal("截断破坏 UTF-8")
	}
}
