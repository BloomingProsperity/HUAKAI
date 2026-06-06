package platformsettings_test

import (
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/modelfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestValidateModelFallbackChains_RejectsUnknownBucket(t *testing.T) {
	// Mutation: keep the generic validateJSONObjectValue dispatch and this
	// object-shaped config is accepted instead of failing loud at write time.
	value := `{"enabled":true,"max_depth":2,"foo":{"gpt-4o":["gpt-4o-mini"]}}`
	_, err := platformsettings.ValidateValue(platformsettings.KeyModelFallbackChains, value)
	if !errors.Is(err, platformsettings.ErrInvalidValue) {
		t.Fatalf("ValidateValue unknown bucket err=%v want ErrInvalidValue", err)
	}
}

func TestValidateModelFallbackChains_RejectsNonStringArrayChain(t *testing.T) {
	// Mutation: skip typed chain assertion and malformed chain payloads pass,
	// letting runtime normalization silently erase the intended fallback.
	cases := []struct {
		name  string
		value string
	}{
		{name: "number", value: `{"enabled":true,"general":{"gpt-4o":42}}`},
		{name: "object", value: `{"enabled":true,"general":{"gpt-4o":{"next":"gpt-4o-mini"}}}`},
		{name: "empty_array", value: `{"enabled":true,"general":{"gpt-4o":[]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := platformsettings.ValidateValue(platformsettings.KeyModelFallbackChains, tc.value)
			if !errors.Is(err, platformsettings.ErrInvalidValue) {
				t.Fatalf("ValidateValue %s err=%v want ErrInvalidValue", tc.name, err)
			}
		})
	}
}

func TestValidateModelFallbackChains_RejectsCycleOrSelfRef(t *testing.T) {
	// Mutation: skip bucket-local cycle checks and admin save accepts chains
	// that can only fall back to already-tried models.
	cases := []struct {
		name  string
		value string
	}{
		{name: "self_ref", value: `{"enabled":true,"general":{"gpt-4o":["gpt-4o","gpt-4o-mini"]}}`},
		{name: "two_node_cycle", value: `{"enabled":true,"general":{"model-a":["model-b"],"model-b":["model-a"]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := platformsettings.ValidateValue(platformsettings.KeyModelFallbackChains, tc.value)
			if !errors.Is(err, platformsettings.ErrInvalidValue) {
				t.Fatalf("ValidateValue %s err=%v want ErrInvalidValue", tc.name, err)
			}
		})
	}
}

func TestValidateModelFallbackChains_RejectsEmptyModelName(t *testing.T) {
	// Mutation: trim-free or missing empty-name checks allow runtime
	// normalization to discard a configured source or fallback target.
	cases := []struct {
		name  string
		value string
	}{
		{name: "empty_source", value: `{"enabled":true,"general":{"  ":["gpt-4o-mini"]}}`},
		{name: "empty_target", value: `{"enabled":true,"general":{"gpt-4o":["  "]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := platformsettings.ValidateValue(platformsettings.KeyModelFallbackChains, tc.value)
			if !errors.Is(err, platformsettings.ErrInvalidValue) {
				t.Fatalf("ValidateValue %s err=%v want ErrInvalidValue", tc.name, err)
			}
		})
	}
}

func TestValidateModelFallbackChains_AcceptsValid(t *testing.T) {
	// Mutation: over-tight validation rejects supported per-error-class buckets
	// or breaks the runtime parser shape used by modelfallback.ParseConfig.
	value := `{"enabled":true,"max_depth":3,"general":{"gpt-4o":["gpt-4o-mini","gpt-4.1-mini"]},"context_window":{"gpt-4o":["gpt-4.1"]},"content_policy":{"*":["policy-safe-model"]}}`
	got, err := platformsettings.ValidateValue(platformsettings.KeyModelFallbackChains, value)
	if err != nil {
		t.Fatalf("ValidateValue valid config: %v", err)
	}
	if got != value {
		t.Fatalf("normalized=%q want original config", got)
	}
	resolver, err := modelfallback.ParseConfig(got)
	if err != nil {
		t.Fatalf("ParseConfig after ValidateValue: %v", err)
	}
	if !resolver.Enabled() || resolver.MaxDepth() != 3 {
		t.Fatalf("resolver enabled=%v max_depth=%d want true/3", resolver.Enabled(), resolver.MaxDepth())
	}
	if next := resolver.Resolve("gpt-4o", modelfallback.General, nil); next != "gpt-4o-mini" {
		t.Fatalf("general fallback=%q want gpt-4o-mini", next)
	}
	if next := resolver.Resolve("gpt-4o", modelfallback.ContextWindowExceeded, nil); next != "gpt-4.1" {
		t.Fatalf("context fallback=%q want gpt-4.1", next)
	}
	if next := resolver.Resolve("any-model", modelfallback.ContentPolicy, nil); next != "policy-safe-model" {
		t.Fatalf("content-policy wildcard fallback=%q want policy-safe-model", next)
	}
}

func TestValidateModelFallbackChains_DepthBounds(t *testing.T) {
	// Mutation: skip max_depth bounds and oversized or negative fallback depth
	// is stored, making retry behavior depend on runtime defaulting/tolerance.
	cases := []struct {
		name  string
		value string
	}{
		{name: "zero", value: `{"enabled":true,"max_depth":0,"general":{"gpt-4o":["gpt-4o-mini"]}}`},
		{name: "negative", value: `{"enabled":true,"max_depth":-1,"general":{"gpt-4o":["gpt-4o-mini"]}}`},
		{name: "too_large", value: `{"enabled":true,"max_depth":11,"general":{"gpt-4o":["gpt-4o-mini"]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := platformsettings.ValidateValue(platformsettings.KeyModelFallbackChains, tc.value)
			if !errors.Is(err, platformsettings.ErrInvalidValue) {
				t.Fatalf("ValidateValue %s err=%v want ErrInvalidValue", tc.name, err)
			}
		})
	}
}
