package credentialstore

import (
	"errors"
	"testing"
)

func TestDefaultHandlerRegistryCoversRefreshableModes(t *testing.T) {
	registry := DefaultHandlerRegistry()
	want := []string{
		"anthropic/api_key",
		"anthropic/claude_ai_oauth",
		"anthropic/claude_code",
		"anthropic/bedrock",
		"anthropic/vertex_anthropic",
		"openai/api_key",
		"openai/chatgpt_oauth",
		"openai/codex_cli_oauth",
		"openai/azure",
		"openai/refresh_token",
		"gemini/aistudio_api_key",
		"gemini/vertex_sa",
		"gemini/code_assist",
		"gemini/google_one",
		"gemini/antigravity",
		"gemini/oauth",
		"copilot/copilot_oauth",
		"antigravity/oauth",
		"windsurf/oauth",
		"grok/xai_oauth",
		"kimi/kimi_oauth",
	}
	if got := registry.Names(); len(got) != len(want) {
		t.Fatalf("handler count=%d want %d: %v", len(got), len(want), got)
	}
	for _, key := range want {
		vendor, mode := splitModeKey(key)
		if _, ok := registry.Lookup(vendor, mode); !ok {
			t.Fatalf("missing handler %s", key)
		}
	}
}

func TestModeValidationRejectsWrongVendorMode(t *testing.T) {
	registry := DefaultHandlerRegistry()
	if _, err := registry.MustLookup("anthropic", "chatgpt_oauth"); !errors.Is(err, ErrUnknownMode) {
		t.Fatalf("err=%v want ErrUnknownMode", err)
	}
	handler, err := registry.MustLookup("openai", "api_key")
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.ValidatePayload([]byte(`{"access_token":"tok"}`)); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("err=%v want ErrInvalidPayload", err)
	}
}

func TestRuntimeMaterialMappings(t *testing.T) {
	registry := DefaultHandlerRegistry()
	cases := []struct {
		vendor string
		mode   string
		raw    string
		kind   string
		value  string
	}{
		{VendorAnthropic, AuthModeAPIKey, `{"api_key":"sk-ant"}`, RuntimeAPIKey, "sk-ant"},
		{VendorAnthropic, AuthModeClaudeAIOAuth, `{"access_token":"anthropic-access","refresh_token":"anthropic-refresh","auth_mode":"claude_ai_oauth"}`, RuntimeOAuthAccessToken, "anthropic-access"},
		{VendorAnthropic, AuthModeBedrock, `{"aws_access_key_id":"ak","aws_secret_access_key":"sec","aws_region":"us-east-1"}`, RuntimeAWSSigV4, "sec"},
		{VendorOpenAI, AuthModeRefreshToken, `{"access_token":"tok","refresh_token":"rt"}`, RuntimeUpstreamPassthrough, "Bearer tok"},
		{VendorGemini, AuthModeAntigravity, `{"session_token":"sess"}`, RuntimeSessionToken, "sess"},
		{VendorAntigravity, AuthModeOAuth, `{"session_token":"ag-sess","refresh_token":"ag-refresh"}`, RuntimeSessionToken, "ag-sess"},
		{VendorWindsurf, AuthModeOAuth, `{"session_token":"ws-sess"}`, RuntimeSessionToken, "ws-sess"},
		{VendorCopilot, AuthModeCopilotOAuth, `{"github_access_token":"gh","session_token":"sess","endpoint_api":"https://copilot-proxy.test"}`, RuntimeSessionToken, "sess"},
		{VendorGrok, AuthModeXAIOAuth, `{"access_token":"xai-access","refresh_token":"xai-refresh"}`, RuntimeOAuthAccessToken, "xai-access"},
		{VendorKimi, AuthModeKimiOAuth, `{"access_token":"kimi-access","refresh_token":"kimi-refresh"}`, RuntimeUpstreamPassthrough, "Bearer kimi-access"},
	}
	for _, tc := range cases {
		handler, err := registry.MustLookup(tc.vendor, tc.mode)
		if err != nil {
			t.Fatalf("%s/%s lookup: %v", tc.vendor, tc.mode, err)
		}
		got, err := handler.RuntimeMaterial([]byte(tc.raw))
		if err != nil {
			t.Fatalf("%s/%s RuntimeMaterial: %v", tc.vendor, tc.mode, err)
		}
		if got.Kind != tc.kind || got.Value != tc.value {
			t.Fatalf("%s/%s got kind=%q value=%q want %q/%q", tc.vendor, tc.mode, got.Kind, got.Value, tc.kind, tc.value)
		}
	}
}

func TestXAIOAuthHandlerSpec(t *testing.T) {
	// Mutation: remove the grok/xai_oauth handlerSpec, make it session-token
	// runtime, or accept session_token-only payloads and this test must go red.
	handler, ok := DefaultHandlerRegistry().Lookup(VendorGrok, AuthModeXAIOAuth)
	if !ok {
		t.Fatal("missing grok/xai_oauth handler")
	}
	spec, ok := handler.(handlerSpec)
	if !ok {
		t.Fatalf("handler type=%T, want handlerSpec", handler)
	}
	if spec.runtimeKind != RuntimeOAuthAccessToken {
		t.Fatalf("runtime kind=%q want %q", spec.runtimeKind, RuntimeOAuthAccessToken)
	}
	if !spec.refreshable {
		t.Fatal("grok/xai_oauth should be refreshable")
	}
	if len(spec.anyOf) != 2 || spec.anyOf[0] != "access_token" || spec.anyOf[1] != "refresh_token" {
		t.Fatalf("anyOf=%v want [access_token refresh_token]", spec.anyOf)
	}
	if err := handler.ValidatePayload([]byte(`{"access_token":"xai-access"}`)); err != nil {
		t.Fatalf("access_token payload rejected: %v", err)
	}
	if err := handler.ValidatePayload([]byte(`{"refresh_token":"xai-refresh"}`)); err != nil {
		t.Fatalf("refresh_token payload rejected: %v", err)
	}
	if err := handler.ValidatePayload([]byte(`{"session_token":"xai-session"}`)); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("err=%v want ErrInvalidPayload for session_token-only payload", err)
	}
	material, err := handler.RuntimeMaterial([]byte(`{"access_token":"xai-access","refresh_token":"xai-refresh"}`))
	if err != nil {
		t.Fatalf("RuntimeMaterial: %v", err)
	}
	if material.Kind != RuntimeOAuthAccessToken || material.Value != "xai-access" {
		t.Fatalf("material=%+v want oauth access token xai-access", material)
	}
}

func TestKimiHandlerSpecRefreshable(t *testing.T) {
	// Mutation: remove refreshable or drop access_token/refresh_token from the
	// Kimi handler anyOf list; this test must go RED on either regression.
	registry := DefaultHandlerRegistry()
	handler, err := registry.MustLookup(VendorKimi, AuthModeKimiOAuth)
	if err != nil {
		t.Fatalf("Kimi handler lookup: %v", err)
	}
	if !handler.Refreshable() {
		t.Fatal("Kimi OAuth handler must be refreshable")
	}
	for _, raw := range []string{
		`{"access_token":"kimi-access"}`,
		`{"refresh_token":"kimi-refresh"}`,
	} {
		if err := handler.ValidatePayload([]byte(raw)); err != nil {
			t.Fatalf("Kimi handler rejected payload %s: %v", raw, err)
		}
	}
	material, err := handler.RuntimeMaterial([]byte(`{"access_token":"kimi-access","refresh_token":"kimi-refresh"}`))
	if err != nil {
		t.Fatalf("Kimi RuntimeMaterial: %v", err)
	}
	if material.Kind != RuntimeUpstreamPassthrough || material.Value != "Bearer kimi-access" {
		t.Fatalf("Kimi material kind=%q value=%q, want upstream_passthrough Bearer access token", material.Kind, material.Value)
	}
	if material.Extra["auth_header"] != "Authorization" {
		t.Fatalf("Kimi auth_header extra=%q want Authorization", material.Extra["auth_header"])
	}
}

func splitModeKey(key string) (string, string) {
	for i, r := range key {
		if r == '/' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}
