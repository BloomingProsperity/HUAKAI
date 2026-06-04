package modelfallback

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestResolverDisabledWhenConfigEmptyOrNotEnabled(t *testing.T) {
	for _, raw := range []string{"", `{"enabled":false,"general":{"gpt-4":["gpt-4-mini"]}}`} {
		resolver, err := ParseConfig(raw)
		if err != nil {
			t.Fatalf("ParseConfig(%q): %v", raw, err)
		}
		if resolver.Enabled() {
			t.Fatalf("resolver for %q enabled; want disabled", raw)
		}
		if next := resolver.Resolve("gpt-4", General, []string{"gpt-4"}); next != "" {
			t.Fatalf("disabled resolver returned %q; want empty", next)
		}
	}
}

func TestResolvePrefersSpecificClassAndFallsBackToGeneralWildcard(t *testing.T) {
	resolver, err := ParseConfig(`{
		"enabled": true,
		"max_depth": 2,
		"general": {"gpt-4":["gpt-4-mini"], "*":["default-backup"]},
		"context_window": {"gpt-4":["gpt-4-128k"]},
		"content_policy": {"gpt-4":["policy-safe"]}
	}`)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	if got := resolver.Resolve("gpt-4", ContextWindowExceeded, []string{"gpt-4"}); got != "gpt-4-128k" {
		t.Fatalf("context fallback=%q want gpt-4-128k", got)
	}
	if got := resolver.Resolve("gpt-4", ContentPolicy, []string{"gpt-4"}); got != "policy-safe" {
		t.Fatalf("content fallback=%q want policy-safe", got)
	}
	if got := resolver.Resolve("unknown-model", ContextWindowExceeded, []string{"unknown-model"}); got != "default-backup" {
		t.Fatalf("class without specific wildcard fallback=%q want default-backup", got)
	}
	if got := resolver.Resolve("gpt-4", General, []string{"gpt-4"}); got != "gpt-4-mini" {
		t.Fatalf("general fallback=%q want gpt-4-mini", got)
	}
}

func TestResolveSkipsAlreadyTriedAndNeverReturnsOriginal(t *testing.T) {
	resolver, err := ParseConfig(`{
		"enabled": true,
		"general": {"gpt-4":["gpt-4","gpt-4-mini","gpt-4o"], "*":["default-backup"]}
	}`)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	if got := resolver.Resolve("gpt-4", General, []string{"gpt-4", "gpt-4-mini"}); got != "gpt-4o" {
		t.Fatalf("next=%q want gpt-4o after skipping original and tried fallback", got)
	}
	if got := resolver.Resolve("gpt-4", General, []string{"gpt-4", "gpt-4-mini", "gpt-4o"}); got != "" {
		t.Fatalf("exhausted chain returned %q; want empty", got)
	}
}

func TestMaxDepthDefaultsAndClampsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{name: "default", raw: `{"enabled":true,"general":{"*":["b"]}}`, want: 2},
		{name: "explicit", raw: `{"enabled":true,"max_depth":4,"general":{"*":["b"]}}`, want: 4},
		{name: "invalid", raw: `{"enabled":true,"max_depth":-1,"general":{"*":["b"]}}`, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver, err := ParseConfig(tt.raw)
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}
			if got := resolver.MaxDepth(); got != tt.want {
				t.Fatalf("MaxDepth=%d want %d", got, tt.want)
			}
		})
	}
}

func TestClassForFailureUsesRealClientErrorAndGatewayClasses(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		endClass    gateway.StreamEndClass
		upstream    gateway.ErrorClass
		abortReason string
		want        ErrorClass
	}{
		{name: "request too large client code", code: "upstream_" + string(gateway.ErrorClassRequestTooLarge), want: ContextWindowExceeded},
		{name: "request too large upstream class", upstream: gateway.ErrorClassRequestTooLarge, want: ContextWindowExceeded},
		{name: "request too large abort reason", abortReason: "request_too_large", want: ContextWindowExceeded},
		{name: "platform policy client code", code: "upstream_" + string(gateway.ErrorClassPlatformPolicy), want: ContentPolicy},
		{name: "platform policy upstream class", upstream: gateway.ErrorClassPlatformPolicy, want: ContentPolicy},
		{name: "no capacity is general", code: clienterr.CodeNoCapacity, endClass: gateway.UpstreamError5xx, want: General},
		{name: "stream timeout is general", code: clienterr.CodeStreamForwardError, endClass: gateway.FirstTokenTimeout, want: General},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassForFailure(tt.code, tt.endClass, tt.upstream, tt.abortReason)
			if got != tt.want {
				t.Fatalf("ClassForFailure=%q want %q", got, tt.want)
			}
		})
	}
}

func TestDeriveLogicalRequestIDIsStablePerModelAndKeepsBaseSeparate(t *testing.T) {
	base := "idem-key-123"
	a := DeriveLogicalRequestID(base, "gpt-4-mini")
	b := DeriveLogicalRequestID(base, "claude-x")
	if a == "" || b == "" {
		t.Fatalf("derived ids must be non-empty: a=%q b=%q", a, b)
	}
	if a == base || b == base || a == b {
		t.Fatalf("derived ids must differ from base and each other: base=%q a=%q b=%q", base, a, b)
	}
	if again := DeriveLogicalRequestID(base, "gpt-4-mini"); again != a {
		t.Fatalf("derived id unstable: first=%q again=%q", a, again)
	}
}

func TestFromSettingsUsesPlatformSettingDefaultClosedAndJSONOptIn(t *testing.T) {
	ctx := context.Background()
	settings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)

	if resolver := FromSettings(ctx, settings); resolver.Enabled() {
		t.Fatal("absent model_fallback_chains setting enabled fallback; want default closed")
	}
	_, err := settings.Upsert(ctx, platformsettings.UpsertInput{
		Key:       platformsettings.KeyModelFallbackChains,
		Value:     `{"enabled":true,"general":{"gpt-4":["gpt-4-mini"]}}`,
		UpdatedBy: "test",
	})
	if err != nil {
		t.Fatalf("Upsert model_fallback_chains: %v", err)
	}
	resolver := FromSettings(ctx, settings)
	if !resolver.Enabled() {
		t.Fatal("JSON opt-in setting did not enable fallback")
	}
	if got := resolver.Resolve("gpt-4", General, []string{"gpt-4"}); got != "gpt-4-mini" {
		t.Fatalf("settings resolver next=%q want gpt-4-mini", got)
	}
}
