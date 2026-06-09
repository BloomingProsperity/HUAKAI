package imagepricing

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

// dallEOfficialCatalog is the exact provider/model JSON seeded by migration
// 0133_openai_image_pricing into the default pricing version. Keep it in sync with
// sql/migrations/0133_openai_image_pricing.up.sql; the DB gate cross-checks the
// seeded row, and this test cross-checks that the multiplier matrix yields the
// official OpenAI per-image USD prices.
const dallEOfficialCatalog = `{"providers":{"openai":{"models":{"dall-e-3":{"pricing_scheme":"per_image","image_base_micro_usd":"40000","image_size_multipliers":{"1024x1024":"1","1024x1792":"2","1792x1024":"2"},"image_quality_multipliers":{"standard":"1","hd":"2","hd@1024x1792":"1.5","hd@1792x1024":"1.5"},"image_amount_range":{"min":1,"max":1},"image_prompt_max_chars":4000},"dall-e-2":{"pricing_scheme":"per_image","image_base_micro_usd":"16000","image_size_multipliers":{"256x256":"1","512x512":"1.125","1024x1024":"1.25"},"image_quality_multipliers":{"standard":"1"},"image_amount_range":{"min":1,"max":10},"image_prompt_max_chars":1000}}}}}`

// TestCatalog_DallEOfficialPerImagePrices verifies the seeded DALL-E matrix bills
// the official OpenAI per-image prices (micro_usd = USD/image * 1e6).
func TestCatalog_DallEOfficialPerImagePrices(t *testing.T) {
	c, err := NewCatalog(json.RawMessage(dallEOfficialCatalog))
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	cases := []struct {
		model, size, quality, wantMicroUSD, wantUSD string
	}{
		{"dall-e-3", "1024x1024", "standard", "40000", "$0.040"},
		{"dall-e-3", "1024x1024", "hd", "80000", "$0.080"},
		{"dall-e-3", "1024x1792", "standard", "80000", "$0.080"},
		{"dall-e-3", "1024x1792", "hd", "120000", "$0.120"},
		{"dall-e-3", "1792x1024", "standard", "80000", "$0.080"},
		{"dall-e-3", "1792x1024", "hd", "120000", "$0.120"},
		{"dall-e-2", "256x256", "standard", "16000", "$0.016"},
		{"dall-e-2", "512x512", "standard", "18000", "$0.018"},
		{"dall-e-2", "1024x1024", "standard", "20000", "$0.020"},
	}
	for _, tc := range cases {
		got, err := c.PerImageMicroUSD("openai", []string{tc.model}, tc.size, tc.quality)
		if err != nil {
			t.Fatalf("%s %s %s: %v", tc.model, tc.size, tc.quality, err)
		}
		want := decimal.RequireFromString(tc.wantMicroUSD)
		if !got.Equal(want) {
			t.Fatalf("%s %s %s (%s): micro_usd=%s want %s", tc.model, tc.size, tc.quality, tc.wantUSD, got, want)
		}
	}
}
