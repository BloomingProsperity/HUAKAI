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
		"cursor/oauth",
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

func TestDefaultVendorHandlersIncludesCursorOAuth(t *testing.T) {
	registry := DefaultHandlerRegistry()
	handler, ok := registry.Lookup(VendorCursor, AuthModeOAuth)
	if !ok {
		t.Fatalf("missing handler %s", ModeKey(VendorCursor, AuthModeOAuth))
	}
	spec, ok := handler.(handlerSpec)
	if !ok {
		t.Fatalf("handler type=%T, want handlerSpec", handler)
	}
	if spec.vendor != VendorCursor || spec.authMode != AuthModeOAuth {
		t.Fatalf("handler identity=%s/%s, want %s/%s", spec.vendor, spec.authMode, VendorCursor, AuthModeOAuth)
	}
	if spec.runtimeKind != RuntimeSessionToken {
		t.Fatalf("runtime kind=%q want %q", spec.runtimeKind, RuntimeSessionToken)
	}
	for _, field := range []string{"session_token", "access_token", "refresh_token"} {
		if !containsString(spec.anyOf, field) {
			t.Fatalf("cursor oauth anyOf=%v missing %q", spec.anyOf, field)
		}
	}
	if !spec.sessionFirst {
		t.Fatal("cursor oauth handler must prefer session_token over access_token")
	}
}

func TestCursorOAuthHandlerRuntimeMaterialAcceptsSessionTokenFirst(t *testing.T) {
	registry := DefaultHandlerRegistry()
	handler, ok := registry.Lookup(VendorCursor, AuthModeOAuth)
	if !ok {
		t.Fatalf("missing handler %s", ModeKey(VendorCursor, AuthModeOAuth))
	}

	got, err := handler.RuntimeMaterial([]byte(`{"session_token":"S","access_token":"A"}`))
	if err != nil {
		t.Fatalf("RuntimeMaterial: %v", err)
	}
	if got.Kind != RuntimeSessionToken {
		t.Fatalf("kind=%q want %q", got.Kind, RuntimeSessionToken)
	}
	if got.Value != "S" {
		t.Fatalf("value=%q want session token", got.Value)
	}
}

func TestCursorRuntimeMaterialSurfacesCursorExtras(t *testing.T) {
	registry := DefaultHandlerRegistry()
	handler, err := registry.MustLookup(VendorCursor, AuthModeOAuth)
	if err != nil {
		t.Fatal(err)
	}

	material, err := handler.RuntimeMaterial([]byte(`{"session_token":"S","cursor_checksum":"chk","cursor_client_version":"0.43.6","cookie":"WorkosX=Y","user_agent":"UA/1"}`))
	if err != nil {
		t.Fatalf("RuntimeMaterial: %v", err)
	}

	wantExtra := map[string]string{
		"cursor_checksum":       "chk",
		"cursor_client_version": "0.43.6",
		"cookie":                "WorkosX=Y",
		"user_agent":            "UA/1",
	}
	for key, want := range wantExtra {
		if got := material.Extra[key]; got != want {
			t.Fatalf("material.Extra[%q]=%q want %q (extra=%v)", key, got, want, material.Extra)
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func splitModeKey(key string) (string, string) {
	for i, r := range key {
		if r == '/' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}
