package imagepricing

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

// gptImageOfficialCatalog 是 migration 0134_openai_gpt_image_pricing 注入的
// 完整 provider/model JSON。需与 .up.sql 保持同步;DB 门控会交叉核对注入的行,
// 本测试则交叉核对 token 费率。
const gptImageOfficialCatalog = `{"providers":{"openai":{"models":{"gpt-image-1":{"pricing_scheme":"token_image","input_micro_usd":"5","output_micro_usd":"40","image_output_token_upper_bound":{"1024x1024":4160,"1024x1536":6240,"1536x1024":6208,"auto":6240},"image_size_multipliers":{"1024x1024":"1","1024x1536":"1","1536x1024":"1","auto":"1"},"image_amount_range":{"min":1,"max":10},"image_prompt_max_chars":32000},"gpt-image-1.5":{"pricing_scheme":"token_image","input_micro_usd":"5","output_micro_usd":"32","image_output_token_upper_bound":{"1024x1024":4160,"1024x1536":6240,"1536x1024":6208,"auto":6240},"image_size_multipliers":{"1024x1024":"1","1024x1536":"1","1536x1024":"1","auto":"1"},"image_amount_range":{"min":1,"max":10},"image_prompt_max_chars":32000}}}}}`

// TestCatalog_GptImageOfficialTokenRates 验证注入的 gpt-image 模型使用
// token_image scheme,并采用官方 OpenAI 的每百万 token 费率(text input /
// image output)。对于生成,上游报告的 input_tokens 仅为文本,
// 因此 input_micro_usd 是文本费率;结算按实际 token 计费,上界仅用于
// 确定预留 hold 的大小。
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
		// 预留上界必须能为默认 size 解析出来
		if _, err := c.OutputTokenUpperBound("openai", []string{tc.model}, "1024x1024"); err != nil {
			t.Fatalf("%s OutputTokenUpperBound: %v", tc.model, err)
		}
	}
}
