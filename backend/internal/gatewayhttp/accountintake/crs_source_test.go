package accountintake

import (
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/crssource"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestCRS来源类型与凭据模式必须精确匹配(t *testing.T) {
	tests := []struct {
		name, sourceType, vendor, authMode string
	}{
		{"Claude 跨厂商", "claude", credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth},
		{"Claude 错模式", "claude", credentialstore.VendorAnthropic, credentialstore.AuthModeAPIKey},
		{"Claude Console 跨厂商", "claude_console", credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey},
		{"Claude Console 错模式", "claude_console", credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth},
		{"OpenAI OAuth 跨厂商", "openai_oauth", credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth},
		{"OpenAI OAuth 错模式", "openai_oauth", credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey},
		{"OpenAI Responses 跨厂商", "openai_responses", credentialstore.VendorAnthropic, credentialstore.AuthModeAPIKey},
		{"OpenAI Responses 错模式", "openai_responses", credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth},
		{"Gemini OAuth 跨厂商", "gemini_oauth", credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth},
		{"Gemini OAuth 错模式", "gemini_oauth", credentialstore.VendorGemini, credentialstore.AuthModeAIStudioAPIKey},
		{"Gemini Key 跨厂商", "gemini_api_key", credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey},
		{"Gemini Key 错模式", "gemini_api_key", credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := buildCRSPlanInput(
				1,
				AccountDefaults{ProviderID: 2, ChannelID: 3, AccountType: "oauth"},
				"source-ref",
				crssource.Account{
					SourceType: test.sourceType, Vendor: test.vendor, AuthMode: test.authMode,
					Credentials: map[string]any{"access_token": "claimed-token"},
				},
				false,
				time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
			)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err=%v，期望 ErrInvalidInput", err)
			}
		})
	}
}
