package modelrate

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

func TestApplyOverridesWritesProviderAndTopLevelAndKeepsOfficialBuckets(t *testing.T) {
	official := json.RawMessage(`{
		"models": {
			"gpt-5.6": {"input_micro_usd":"4","output_micro_usd":"20","cache_read_micro_usd":"0.4","model_multiplier":"1"}
		},
		"providers": {
			"openai": {
				"models": {
					"gpt-5.6": {"input_micro_usd":"4","output_micro_usd":"20","cache_read_micro_usd":"0.4"}
				}
			}
		}
	}`)
	input := decimal.RequireFromString("2.5")
	cacheRead := decimal.RequireFromString("0.1")
	out, err := ApplyOverrides(official, []Override{{
		Vendor: "openai",
		Model:  "gpt-5.6",
		Rates:  RateBuckets{Input: &input, CacheRead: &cacheRead},
	}})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertRateField(t, nested(t, root, "models", "gpt-5.6"), "input_micro_usd", "2.5")
	assertRateField(t, nested(t, root, "models", "gpt-5.6"), "output_micro_usd", "20")
	assertRateField(t, nested(t, root, "models", "gpt-5.6"), "cache_read_micro_usd", "0.1")
	assertRateField(t, nested(t, root, "models", "gpt-5.6"), "model_multiplier", "1")
	assertRateField(t, nested(t, root, "providers", "openai", "models", "gpt-5.6"), "input_micro_usd", "2.5")
	assertRateField(t, nested(t, root, "providers", "openai", "models", "gpt-5.6"), "output_micro_usd", "20")

	// 判别:若实现只改顶层 models,providers 路径仍是官方 4。
	if got := fieldString(t, nested(t, root, "providers", "openai", "models", "gpt-5.6"), "input_micro_usd"); got == "4" {
		t.Fatalf("provider path still official input=4; overlay missed providers.*.models")
	}
}

func TestApplyOverridesCreatesMissingModelInsteadOfSilentSkip(t *testing.T) {
	official := json.RawMessage(`{"models":{}}`)
	output := decimal.RequireFromString("9")
	out, err := ApplyOverrides(official, []Override{{
		Vendor: "anthropic",
		Model:  "claude-custom",
		Rates:  RateBuckets{Output: &output},
	}})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertRateField(t, nested(t, root, "models", "claude-custom"), "output_micro_usd", "9")
	assertRateField(t, nested(t, root, "providers", "anthropic", "models", "claude-custom"), "output_micro_usd", "9")
}

func TestBuildCatalogShowsOfficialAndEffectiveAndVendorFilter(t *testing.T) {
	official := json.RawMessage(`{
		"models": {
			"gpt-4o": {"input_micro_usd":"2.5","output_micro_usd":"10"},
			"claude-sonnet-4-5": {"input_micro_usd":"3","output_micro_usd":"15"}
		},
		"providers": {
			"openai": {"models": {"gpt-4o": {"input_micro_usd":"2.5","output_micro_usd":"10","cache_read_micro_usd":"1.25","cache_creation_micro_usd":"3.75"}}}
		}
	}`)
	input := decimal.RequireFromString("1")
	items, err := BuildCatalog(official, []Override{{
		Vendor: "openai",
		Model:  "gpt-4o",
		Rates:  RateBuckets{Input: &input},
	}}, "openai")
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d want 1 openai row", len(items))
	}
	item := items[0]
	if item.Vendor != "openai" || item.Model != "gpt-4o" || !item.Overridden || item.Source != SourceOverride {
		t.Fatalf("item=%+v", item)
	}
	if item.Official.Input.String() != "2.5" || item.Effective.Input.String() != "1" {
		t.Fatalf("official=%v effective=%v", item.Official.Input, item.Effective.Input)
	}
	if item.Official.Output.String() != "10" || item.Effective.Output.String() != "10" {
		t.Fatalf("partial override must keep official output")
	}
	if item.Official.CacheRead == nil || item.Official.CacheRead.String() != "1.25" {
		t.Fatalf("provider cache_read missing: %+v", item.Official)
	}
	if item.Official.CacheCreation == nil || item.Official.CacheCreation.String() != "3.75" {
		t.Fatalf("provider cache_creation missing: %+v", item.Official)
	}
	if item.Effective.CacheCreation == nil || item.Effective.CacheCreation.String() != "3.75" {
		t.Fatalf("partial override must keep official cache_creation")
	}

	all, err := BuildCatalog(official, nil, "")
	if err != nil {
		t.Fatalf("BuildCatalog all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all items=%d want 2 (openai gpt-4o + inferred anthropic claude)", len(all))
	}
	claude, ok := FindCatalogItem(all, "anthropic", "claude-sonnet-4-5")
	if !ok || claude.Overridden || claude.Source != SourceOfficial {
		t.Fatalf("claude item=%+v ok=%v", claude, ok)
	}
	if claude.Official.Input.String() != claude.Effective.Input.String() {
		t.Fatalf("no-override official/effective mismatch")
	}
}

func TestInferVendorGroupsFrontierPrefixes(t *testing.T) {
	cases := map[string]string{
		"gpt-5.6":          "openai",
		"o1":               "openai",
		"o1-preview":       "openai",
		"o3-mini":          "openai",
		"o10-experimental": "catalog",
		"o1preview":        "catalog",
		"claude-opus-5":    "anthropic",
		"gemini-3.6-flash": "google",
		"grok-4.6":         "xai",
		"deepseek-v4-pro":  "deepseek",
		"unknown-model-x":  "catalog",
	}
	for model, want := range cases {
		if got := InferVendor(model); got != want {
			t.Fatalf("InferVendor(%q)=%q want %q", model, got, want)
		}
	}
}

func nested(t *testing.T, root map[string]json.RawMessage, keys ...string) map[string]json.RawMessage {
	t.Helper()
	cur := root
	for i, key := range keys {
		raw, ok := cur[key]
		if !ok {
			t.Fatalf("missing key %q at %v", key, keys[:i+1])
		}
		if i == len(keys)-1 {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(raw, &obj); err != nil {
				t.Fatalf("decode %v: %v raw=%s", keys, err, raw)
			}
			return obj
		}
		next, err := decodeObject(raw)
		if err != nil {
			t.Fatalf("decode %q: %v", key, err)
		}
		cur = next
	}
	return cur
}

func assertRateField(t *testing.T, obj map[string]json.RawMessage, key, want string) {
	t.Helper()
	got := fieldString(t, obj, key)
	if got != want {
		t.Fatalf("%s=%q want %q obj=%v", key, got, want, obj)
	}
}

func fieldString(t *testing.T, obj map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, ok := obj[key]
	if !ok {
		t.Fatalf("missing %s", key)
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	return string(raw)
}
