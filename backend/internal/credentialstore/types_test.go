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

func splitModeKey(key string) (string, string) {
	for i, r := range key {
		if r == '/' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}
