package imagepricing

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

func TestCatalogPerImageMicroUSDUsesSizeQualityAndProviderModelLookup(t *testing.T) {
	catalog := mustCatalog(t, `{
		"providers": {
			"openai": {
				"models": {
					"dall-e-3": {
						"pricing_scheme": "per_image",
						"image_base_micro_usd": "1000",
						"image_size_multipliers": {"1024x1024": "1", "1024x1792": "2.0"},
						"image_quality_multipliers": {"standard": "1", "hd": "1.25", "hd@1024x1792": "1.5"},
						"image_amount_range": {"min": 1, "max": 1},
						"image_prompt_max_chars": 4000
					}
				}
			}
		}
	}`)

	got, err := catalog.PerImageMicroUSD("openai", []string{"dall-e-3"}, "1024x1792", "hd")
	if err != nil {
		t.Fatalf("PerImageMicroUSD() error = %v", err)
	}
	assertImagePricingDecimal(t, "per image", got, "3000")
}

func TestCatalogMetadataComesFromDefaultFallback(t *testing.T) {
	catalog := mustCatalog(t, `{
		"models": {
			"default": {
				"pricing_scheme": "token_image",
				"input_micro_usd": "10",
				"output_micro_usd": "20",
				"image_output_token_upper_bound": {"1024x1024": 512},
				"image_size_multipliers": {"1024x1024": "1"},
				"image_amount_range": {"min": 1, "max": 4},
				"image_prompt_max_chars": 1000
			}
		}
	}`)

	scheme, err := catalog.SchemeFor("", []string{"gpt-image-1", "default"})
	if err != nil {
		t.Fatalf("SchemeFor() error = %v", err)
	}
	if scheme != SchemeTokenImage {
		t.Fatalf("scheme=%q want %q", scheme, SchemeTokenImage)
	}
	sizes, err := catalog.AllowedSizes("", []string{"gpt-image-1", "default"})
	if err != nil {
		t.Fatalf("AllowedSizes() error = %v", err)
	}
	if len(sizes) != 1 || sizes[0] != "1024x1024" {
		t.Fatalf("sizes=%v want [1024x1024]", sizes)
	}
	rng, err := catalog.AmountRange("", []string{"gpt-image-1", "default"})
	if err != nil {
		t.Fatalf("AmountRange() error = %v", err)
	}
	if rng.Min != 1 || rng.Max != 4 {
		t.Fatalf("range=%+v want min=1 max=4", rng)
	}
	maxChars, err := catalog.PromptMaxChars("", []string{"gpt-image-1", "default"})
	if err != nil {
		t.Fatalf("PromptMaxChars() error = %v", err)
	}
	if maxChars != 1000 {
		t.Fatalf("prompt max=%d want 1000", maxChars)
	}
	estimate, err := catalog.OutputTokenUpperBound("", []string{"gpt-image-1", "default"}, "1024x1024")
	if err != nil {
		t.Fatalf("OutputTokenUpperBound() error = %v", err)
	}
	if estimate != 512 {
		t.Fatalf("output token bound=%d want 512", estimate)
	}
}

func TestCatalogMissingImagePricingReturnsError(t *testing.T) {
	catalog := mustCatalog(t, `{"models":{"text-only":{"input_micro_usd": "10"}}}`)

	if _, err := catalog.PerImageMicroUSD("", []string{"text-only"}, "1024x1024", "standard"); err == nil {
		t.Fatal("PerImageMicroUSD() error = nil want missing image pricing error")
	}
	if _, err := catalog.SchemeFor("", []string{"missing"}); err == nil {
		t.Fatal("SchemeFor() error = nil want missing model error")
	}
}

func mustCatalog(t *testing.T, raw string) *Catalog {
	t.Helper()
	c, err := NewCatalog(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	return c
}

func assertImagePricingDecimal(t *testing.T, field string, got decimal.Decimal, want string) {
	t.Helper()
	wantDecimal := decimal.RequireFromString(want)
	if !got.Equal(wantDecimal) {
		t.Fatalf("%s=%s want %s", field, got, wantDecimal)
	}
}
