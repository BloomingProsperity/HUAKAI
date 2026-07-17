package credentialstore

import (
	"errors"
	"strings"
	"testing"
)

func TestDefaultHandlerRegistryCoversRefreshableModes(t *testing.T) {
	registry := DefaultHandlerRegistry()
	want := []string{
		"anthropic/api_key",
		"anthropic/claude_ai_oauth",
		"anthropic/claude_code",
		"anthropic/claude_setup_token",
		"anthropic/bedrock",
		"anthropic/vertex_anthropic",
		"openai/api_key",
		"openai/chatgpt_oauth",
		"openai/codex_cli_oauth",
		"openai/codex_agent_identity",
		"openai/codex_web_oauth",
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
		// 官 key 厂商(2026-07-02 接入,迁移 0169 放行存储)。
		"grok/api_key",
		"deepseek/api_key",
		"kimi/api_key",
		"qwen/api_key",
		"glm/api_key",
		"yi/api_key",
		"baichuan/api_key",
		"doubao/api_key",
		"minimax/api_key",
		"ernie/api_key",
		"hunyuan/api_key",
		"step/api_key",
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
		{VendorAnthropic, AuthModeClaudeCode, `{"session_token":"anthropic-session","access_token":"anthropic-access","auth_mode":"claude_code"}`, RuntimeSessionToken, "anthropic-session"},
		{VendorAnthropic, AuthModeClaudeSetupToken, `{"setup_token":"anthropic-long-lived"}`, RuntimeOAuthAccessToken, "anthropic-long-lived"},
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

func TestClaudeSetupTokenIsStaticAccessToken(t *testing.T) {
	handler, err := DefaultHandlerRegistry().MustLookup(VendorAnthropic, AuthModeClaudeSetupToken)
	if err != nil {
		t.Fatal(err)
	}
	if handler.Refreshable() || handler.AllowGrace() {
		t.Fatalf("setup token refreshable=%v allow_grace=%v，期望静态长期 access token", handler.Refreshable(), handler.AllowGrace())
	}
	if err := handler.ValidatePayload([]byte(`{"access_token":"wrong-shape"}`)); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("普通 access_token 不得冒充 setup_token: %v", err)
	}
}

// TestAzureAPIKeyFailsClosedToPreventOpenAILeak 咬住 S0:azure_api_key(无 api_key、无
// access_token)必须 fail-closed 物化——否则会被物化成普通 APIKey,由 OpenAI adapter
// 发往 api.openai.com(Bearer),把 Azure 密钥外发给 OpenAI。Entra access_token 仍走
// passthrough(尊重 base_url,发往 operator 自配 endpoint,不外发)。api_key 正常。
// 变异:恢复 firstField(fields,"api_key","azure_api_key")而去掉 fail-close → azure_api_key
// 物化成 APIKey(value=Azure 密钥),第一条断言红。
func TestAzureAPIKeyFailsClosedToPreventOpenAILeak(t *testing.T) {
	handler, err := DefaultHandlerRegistry().MustLookup(VendorOpenAI, AuthModeAzure)
	if err != nil {
		t.Fatalf("azure handler lookup: %v", err)
	}
	// azure_api_key 单独存在 → 必须拒绝物化,且错误不回显密钥。
	if _, err := handler.RuntimeMaterial([]byte(`{"azure_api_key":"azkey-secret","base_url":"https://x.openai.azure.com"}`)); err == nil {
		t.Fatal("azure_api_key 应 fail-closed 拒绝物化,却成功(密钥会外发到 OpenAI)")
	} else if strings.Contains(err.Error(), "azkey-secret") {
		t.Fatalf("错误信息不得回显密钥: %v", err)
	}
	// Entra access_token → passthrough(不外发,尊重 base_url)。
	got, err := handler.RuntimeMaterial([]byte(`{"access_token":"entra-tok","base_url":"https://x.openai.azure.com"}`))
	if err != nil {
		t.Fatalf("Entra access_token 应可物化: %v", err)
	}
	if got.Kind != RuntimeUpstreamPassthrough || got.Value != "Bearer entra-tok" {
		t.Fatalf("Entra 应为 passthrough Bearer,得 kind=%q value=%q", got.Kind, got.Value)
	}
	// 普通 api_key(非 azure)正常物化。
	got, err = handler.RuntimeMaterial([]byte(`{"api_key":"sk-openai"}`))
	if err != nil || got.Kind != RuntimeAPIKey || got.Value != "sk-openai" {
		t.Fatalf("普通 api_key 应正常物化,得 kind=%q value=%q err=%v", got.Kind, got.Value, err)
	}
}

func TestOpenAICodexRuntimeMaterialSurfacesAccountHeaders(t *testing.T) {
	registry := DefaultHandlerRegistry()
	cases := []struct {
		name string
		mode string
	}{
		{name: "chatgpt oauth", mode: AuthModeChatGPTOAuth},
		{name: "codex cli oauth", mode: AuthModeCodexCLIOAuth},
		{name: "codex web oauth", mode: AuthModeCodexWebOAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, err := registry.MustLookup(VendorOpenAI, tc.mode)
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			raw := `{
				"access_token":"access-for-codex",
				"account_id":"acct_primary",
				"chatgpt_account_id":"acct_header",
				"codex_version":"0.99.0",
				"originator":"codex_cli_rs",
				"oai_device_id":"device_1"
			}`
			material, err := handler.RuntimeMaterial([]byte(raw))
			if err != nil {
				t.Fatalf("RuntimeMaterial: %v", err)
			}
			if material.Kind != RuntimeSessionToken || material.Value != "access-for-codex" {
				t.Fatalf("material kind/value=%q/%q want session access token", material.Kind, material.Value)
			}
			for key, want := range map[string]string{
				"account_id":         "acct_primary",
				"chatgpt_account_id": "acct_header",
				"codex_version":      "0.99.0",
				"originator":         "codex_cli_rs",
				"oai_device_id":      "device_1",
			} {
				if got := material.Extra[key]; got != want {
					t.Fatalf("Extra[%s]=%q want %q; extra=%+v", key, got, want, material.Extra)
				}
			}
		})
	}
}

// TestVertexRuntimeMaterialSurfacesLocation 抓的回归:Vertex 模式的
// RuntimeMaterial 必须把 location 透到 Extra,供 vertex.PassthroughAdapter 拼
// region-templated URL。location 不在白名单 → adapter 拿不到 → URL 区域错。
// 判别性:断言 Extra["location"]==us-east5 + Extra["project_id"]==my-proj;
// Mutation:从 RuntimeMaterial 的 Extra 白名单删 "location" → Extra 无 location
// 断言红（URL host 会回落默认 us-central1，区域错路由）。
func TestVertexRuntimeMaterialSurfacesLocation(t *testing.T) {
	registry := DefaultHandlerRegistry()
	cases := []struct {
		vendor string
		mode   string
	}{
		{VendorGemini, AuthModeVertexSA},
		{VendorAnthropic, AuthModeVertexAnthropic},
	}
	for _, tc := range cases {
		t.Run(tc.vendor+"/"+tc.mode, func(t *testing.T) {
			handler, err := registry.MustLookup(tc.vendor, tc.mode)
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			raw := `{"access_token":"vertex-access","project_id":"my-proj","location":"us-east5"}`
			material, err := handler.RuntimeMaterial([]byte(raw))
			if err != nil {
				t.Fatalf("RuntimeMaterial: %v", err)
			}
			if material.Kind != RuntimeUpstreamPassthrough {
				t.Fatalf("kind=%q want %q", material.Kind, RuntimeUpstreamPassthrough)
			}
			if material.Value != "Bearer vertex-access" {
				t.Fatalf("Value=%q want Bearer vertex-access (v2 vertex_sa 必须产可转发主值)", material.Value)
			}
			if got := material.Extra["location"]; got != "us-east5" {
				t.Fatalf("Extra[location]=%q want us-east5 (location 必须透到 adapter)", got)
			}
			if got := material.Extra["project_id"]; got != "my-proj" {
				t.Fatalf("Extra[project_id]=%q want my-proj", got)
			}
		})
	}
}

func TestXAIOAuthHandlerSpec(t *testing.T) {
	// 变异:删除 grok/xai_oauth 的 handlerSpec、将其改为 session-token runtime,
	// 或接受仅含 session_token 的 payload,本测试都必须变红。
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
	// 变异:从 Kimi handler 的 anyOf 列表中移除 refreshable 或去掉
	// access_token/refresh_token;任一回归都必须使本测试变红。
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
