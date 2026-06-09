package imagepricing

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

// gptImageOfficialCatalog is the exact provider/model JSON seeded by migration
// 0134_openai_gpt_image_pricing. Keep in sync with the .up.sql; the DB gate
// cross-checks the seeded row and this test cross-checks the token rates.
const gptImageOfficialCatalog = `{"providers":{"openai":{"models":{"gpt-image-1":{"pricing_scheme":"token_image","input_micro_usd":"5","output_micro_usd":"40","image_output_token_upper_bound":{"1024x1024":4160,"1024x1536":6240,"1536x1024":6208,"auto":6240},"image_size_multipliers":{"1024x1024":"1","1024x1536":"1","1536x1024":"1","auto":"1"},"image_amount_range":{"min":1,"max":10},"image_prompt_max_chars":32000},"gpt-image-1.5":{"pricing_scheme":"token_image","input_micro_usd":"5","output_micro_usd":"32","image_output_token_upper_bound":{"1024x1024":4160,"1024x1536":6240,"1536x1024":6208,"auto":6240},"image_size_multipliers":{"1024x1024":"1","1024x1536":"1","1536x1024":"1","auto":"1"},"image_amount_range":{"min":1,"max":10},"image_prompt_max_chars":32000}}}}}`

// TestCatalog_GptImageOfficialTokenRates verifies the seeded gpt-image models use
// the token_image scheme with the official OpenAI per-1M-token rates (text input /
// image output). For generation the upstream-reported input_tokens are text only,
// so input_micro_usd is the text rate; settle bills actual tokens, the upper bound
// only sizes the reservation hold.
func TestCatalog_GptImageOfficialTokenRates(t *testing.T) {
	c, err := NewCatalog(json.RawMessage(gptImageOfficialCatalog))
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	cases := []struct {
		model, wantInput, wantOutput string
	}{
		{"gpt-image-1", "5", "40"},
		{"gpt-image-1.5", "5", "32"},
	}
	for _, tc := range cases {
		scheme, err := c.SchemeFor("openai", []string{tc.model})
		if err != nil {
			t.Fatalf("%s SchemeFor: %v", tc.model, err)
		}
		if scheme != SchemeTokenImage {
			t.Fatalf("%s scheme=%q want token_image", tc.model, scheme)
		}
		rates, err := c.TokenRates("openai", []string{tc.model})
		if err != nil {
			t.Fatalf("%s TokenRates: %v", tc.model, err)
		}
		if !rates.Input.Equal(decimal.RequireFromString(tc.wantInput)) {
			t.Fatalf("%s input rate=%s want %s ($%s/1M text input)", tc.model, rates.Input, tc.wantInput, tc.wantInput)
		}
		if !rates.Output.Equal(decimal.RequireFromString(tc.wantOutput)) {
			t.Fatalf("%s output rate=%s want %s ($%s/1M image output)", tc.model, rates.Output, tc.wantOutput, tc.wantOutput)
		}
		// the reservation upper bound must resolve for the default size
		if _, err := c.OutputTokenUpperBound("openai", []string{tc.model}, "1024x1024"); err != nil {
			t.Fatalf("%s OutputTokenUpperBound: %v", tc.model, err)
		}
	}
}
