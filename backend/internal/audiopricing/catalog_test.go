package audiopricing

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

func TestCatalogAudioRatesUseProviderModelLookup(t *testing.T) {
	catalog := mustAudioCatalog(t, `{
		"providers": {
			"openai": {
				"models": {
					"tts-1": {
						"pricing_scheme": "per_char",
						"input_char_micro_usd": "15"
					},
					"whisper-1": {
						"pricing_scheme": "per_second",
						"input_second_micro_usd": "100"
					}
				}
			}
		}
	}`)

	scheme, err := catalog.SchemeFor("openai", []string{"tts-1"})
	if err != nil {
		t.Fatalf("SchemeFor(tts) error = %v", err)
	}
	if scheme != SchemePerChar {
		t.Fatalf("tts scheme=%q want %q", scheme, SchemePerChar)
	}
	charRate, err := catalog.CharMicroUSD("openai", []string{"tts-1"})
	if err != nil {
		t.Fatalf("CharMicroUSD() error = %v", err)
	}
	assertAudioPricingDecimal(t, "char rate", charRate, "15")

	scheme, err = catalog.SchemeFor("openai", []string{"whisper-1"})
	if err != nil {
		t.Fatalf("SchemeFor(whisper) error = %v", err)
	}
	if scheme != SchemePerSecond {
		t.Fatalf("whisper scheme=%q want %q", scheme, SchemePerSecond)
	}
	secondRate, err := catalog.SecondMicroUSD("openai", []string{"whisper-1"})
	if err != nil {
		t.Fatalf("SecondMicroUSD() error = %v", err)
	}
	assertAudioPricingDecimal(t, "second rate", secondRate, "100")
}

func TestCatalogTokenAudioRatesComeFromDefaultFallback(t *testing.T) {
	catalog := mustAudioCatalog(t, `{
		"models": {
			"default": {
				"pricing_scheme": "token",
				"input_micro_usd": "1000",
				"output_micro_usd": "2000"
			}
		}
	}`)

	scheme, err := catalog.SchemeFor("", []string{"gpt-4o-transcribe", "default"})
	if err != nil {
		t.Fatalf("SchemeFor() error = %v", err)
	}
	if scheme != SchemeToken {
		t.Fatalf("scheme=%q want %q", scheme, SchemeToken)
	}
	rates, err := catalog.TokenRates("", []string{"gpt-4o-transcribe", "default"})
	if err != nil {
		t.Fatalf("TokenRates() error = %v", err)
	}
	assertAudioPricingDecimal(t, "input", rates.Input, "1000")
	assertAudioPricingDecimal(t, "output", rates.Output, "2000")
	if !rates.HasInput || !rates.HasOutput {
		t.Fatalf("token rates should carry input/output flags: %+v", rates)
	}
}

func TestCatalogMissingAudioRateFailsClosed(t *testing.T) {
	catalog := mustAudioCatalog(t, `{
		"models": {
			"tts-1": {"pricing_scheme": "per_char"},
			"text-only": {"input_micro_usd": "1000"}
		}
	}`)

	if _, err := catalog.CharMicroUSD("", []string{"tts-1"}); err == nil {
		t.Fatal("CharMicroUSD() error = nil want missing audio rate error")
	}
	if _, err := catalog.SchemeFor("", []string{"text-only"}); err == nil {
		t.Fatal("SchemeFor(text-only) error = nil want unsupported audio scheme error")
	}
	if _, err := catalog.SecondMicroUSD("", []string{"missing-model"}); err == nil {
		t.Fatal("SecondMicroUSD(missing) error = nil want missing model error")
	}
}

func mustAudioCatalog(t *testing.T, raw string) *Catalog {
	t.Helper()
	catalog, err := NewCatalog(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	return catalog
}

func assertAudioPricingDecimal(t *testing.T, field string, got decimal.Decimal, want string) {
	t.Helper()
	wantDecimal := decimal.RequireFromString(want)
	if !got.Equal(wantDecimal) {
		t.Fatalf("%s=%s want %s", field, got, wantDecimal)
	}
}
