//go:build e2e_upstream

package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

const (
	upstreamE2EAccountTypeOAuth = "oauth"

	upstreamE2EAnthropicKeyEnv            = "HUAKAI_E2E_ANTHROPIC_KEY"
	upstreamE2EAnthropicModelEnv          = "HUAKAI_E2E_ANTHROPIC_MODEL"
	upstreamE2EClaudeAICredentialJSONEnv  = "HUAKAI_E2E_CLAUDE_AI_OAUTH_CREDENTIAL_JSON"
	upstreamE2EClaudeCodeCredentialEnv    = "HUAKAI_E2E_CLAUDE_CODE_CREDENTIAL_JSON"
	upstreamE2EClaudeModelEnv             = "HUAKAI_E2E_CLAUDE_MODEL"
	upstreamE2EGeminiKeyEnv               = "HUAKAI_E2E_GEMINI_KEY"
	upstreamE2EGeminiModelEnv             = "HUAKAI_E2E_GEMINI_MODEL"
	upstreamE2EGeminiCodeCredentialEnv    = "HUAKAI_E2E_GEMINI_CODE_ASSIST_CREDENTIAL_JSON"
	upstreamE2EGeminiCodeModelEnv         = "HUAKAI_E2E_GEMINI_CODE_ASSIST_MODEL"
	upstreamE2EAntigravityCredentialEnv   = "HUAKAI_E2E_ANTIGRAVITY_CREDENTIAL_JSON"
	upstreamE2EAntigravityModelEnv        = "HUAKAI_E2E_ANTIGRAVITY_MODEL"
	upstreamE2EKimiKeyEnv                 = "HUAKAI_E2E_KIMI_KEY"
	upstreamE2EKimiCredentialJSONEnv      = "HUAKAI_E2E_KIMI_CREDENTIAL_JSON"
	upstreamE2EKimiModelEnv               = "HUAKAI_E2E_KIMI_MODEL"
	upstreamE2EGeminiCodeAssistAdapterEnv = "HUAKAI_ENABLE_GEMINI_CODE_ASSIST_ADAPTER=true"
	upstreamE2EAntigravityAdapterEnv      = "HUAKAI_ENABLE_ANTIGRAVITY_SESSION_ADAPTER=true"
)

var upstreamE2ESecretEnvNames = []string{
	upstreamE2EARKKeyEnv,
	upstreamE2EHunyuanKeyEnv,
	upstreamE2EAnthropicKeyEnv,
	upstreamE2EClaudeAICredentialJSONEnv,
	upstreamE2EClaudeCodeCredentialEnv,
	upstreamE2EGeminiKeyEnv,
	upstreamE2EGeminiCodeCredentialEnv,
	upstreamE2EAntigravityCredentialEnv,
	upstreamE2EKimiKeyEnv,
	upstreamE2EKimiCredentialJSONEnv,
}

func TestAccountFamilyLive_AnthropicAPIKey(t *testing.T) {
	runUpstreamE2E(t, upstreamE2ECase{
		slug:            "anthropic-api-key",
		vendor:          credentialstore.VendorAnthropic,
		protocolFamily:  registrydefault.ProtocolAnthropicMessages,
		model:           requiredUpstreamE2EModel(t, upstreamE2EAnthropicModelEnv),
		clientShape:     upstreamE2EClientAnthropic,
		keyEnv:          upstreamE2EAnthropicKeyEnv,
		authMode:        credentialstore.AuthModeAPIKey,
		accountType:     upstreamE2EAccountTypeAPIKey,
		skipConcurrency: true,
	})
}

func TestAccountFamilyLive_ClaudeAIOAuth(t *testing.T) {
	runUpstreamE2E(t, upstreamE2ECase{
		slug:                 "claude-ai-oauth",
		vendor:               credentialstore.VendorAnthropic,
		protocolFamily:       registrydefault.ProtocolAnthropicClaudeSession,
		model:                requiredUpstreamE2EModel(t, upstreamE2EClaudeModelEnv),
		clientShape:          upstreamE2EClientAnthropic,
		officialClaudeClient: true,
		credentialJSONEnv:    upstreamE2EClaudeAICredentialJSONEnv,
		authMode:             credentialstore.AuthModeClaudeAIOAuth,
		accountType:          upstreamE2EAccountTypeOAuth,
		skipConcurrency:      true,
	})
}

func TestAccountFamilyLive_ClaudeCode(t *testing.T) {
	runUpstreamE2E(t, upstreamE2ECase{
		slug:                 "claude-code",
		vendor:               credentialstore.VendorAnthropic,
		protocolFamily:       registrydefault.ProtocolAnthropicClaudeSession,
		model:                requiredUpstreamE2EModel(t, upstreamE2EClaudeModelEnv),
		clientShape:          upstreamE2EClientAnthropic,
		officialClaudeClient: true,
		credentialJSONEnv:    upstreamE2EClaudeCodeCredentialEnv,
		authMode:             credentialstore.AuthModeClaudeCode,
		accountType:          upstreamE2EAccountTypeOAuth,
		skipConcurrency:      true,
	})
}

func TestAccountFamilyLive_GeminiAIStudio(t *testing.T) {
	runUpstreamE2E(t, upstreamE2ECase{
		slug:            "gemini-aistudio",
		vendor:          credentialstore.VendorGemini,
		protocolFamily:  registrydefault.ProtocolGeminiMessages,
		model:           requiredUpstreamE2EModel(t, upstreamE2EGeminiModelEnv),
		keyEnv:          upstreamE2EGeminiKeyEnv,
		authMode:        credentialstore.AuthModeAIStudioAPIKey,
		accountType:     upstreamE2EAccountTypeAPIKey,
		skipConcurrency: true,
	})
}

func TestAccountFamilyLive_GeminiCodeAssist(t *testing.T) {
	runUpstreamE2E(t, upstreamE2ECase{
		slug:              "gemini-code-assist",
		vendor:            credentialstore.VendorGemini,
		protocolFamily:    registrydefault.ProtocolGeminiCodeAssist,
		model:             requiredUpstreamE2EModel(t, upstreamE2EGeminiCodeModelEnv),
		credentialJSONEnv: upstreamE2EGeminiCodeCredentialEnv,
		authMode:          credentialstore.AuthModeCodeAssist,
		accountType:       upstreamE2EAccountTypeOAuth,
		gatewayEnv:        []string{upstreamE2EGeminiCodeAssistAdapterEnv},
		skipConcurrency:   true,
	})
}

func TestAccountFamilyLive_Antigravity(t *testing.T) {
	runUpstreamE2E(t, upstreamE2ECase{
		slug:              "antigravity",
		vendor:            credentialstore.VendorAntigravity,
		protocolFamily:    registrydefault.ProtocolAntigravitySession,
		model:             requiredUpstreamE2EModel(t, upstreamE2EAntigravityModelEnv),
		credentialJSONEnv: upstreamE2EAntigravityCredentialEnv,
		authMode:          credentialstore.AuthModeOAuth,
		accountType:       upstreamE2EAccountTypeOAuth,
		gatewayEnv:        []string{upstreamE2EAntigravityAdapterEnv},
		skipConcurrency:   true,
	})
}

func TestAccountFamilyLive_KimiAPIKey(t *testing.T) {
	runUpstreamE2E(t, upstreamE2ECase{
		slug:            "kimi-api-key",
		vendor:          credentialstore.VendorKimi,
		protocolFamily:  registrydefault.ProtocolKimiChat,
		model:           requiredUpstreamE2EModel(t, upstreamE2EKimiModelEnv),
		keyEnv:          upstreamE2EKimiKeyEnv,
		authMode:        credentialstore.AuthModeAPIKey,
		accountType:     upstreamE2EAccountTypeAPIKey,
		skipConcurrency: true,
	})
}

func TestAccountFamilyLive_KimiOAuth(t *testing.T) {
	runUpstreamE2E(t, upstreamE2ECase{
		slug:              "kimi-oauth",
		vendor:            credentialstore.VendorKimi,
		protocolFamily:    registrydefault.ProtocolKimiChat,
		model:             requiredUpstreamE2EModel(t, upstreamE2EKimiModelEnv),
		credentialJSONEnv: upstreamE2EKimiCredentialJSONEnv,
		authMode:          credentialstore.AuthModeKimiOAuth,
		accountType:       upstreamE2EAccountTypeOAuth,
		skipConcurrency:   true,
	})
}

func requiredUpstreamE2EModel(t *testing.T, envName string) string {
	t.Helper()
	model := strings.TrimSpace(os.Getenv(envName))
	if model == "" {
		t.Skip(envName + " 未设")
	}
	return model
}

func TestAccountFamilyLive_ClaudeSessionsUseOfficialMessagesIngress(t *testing.T) {
	seed := &upstreamE2ESeed{
		testCase: upstreamE2ECase{
			model:                "claude-live-test",
			clientShape:          upstreamE2EClientAnthropic,
			officialClaudeClient: true,
		},
		bearer: "gateway-customer-key",
	}
	req, err := newUpstreamE2ERequest(t.Context(), "127.0.0.1:8080", seed, "logical-test")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.Path != "/v1/messages" {
		t.Fatalf("path=%q want /v1/messages", req.URL.Path)
	}
	wantHeaders := map[string]string{
		"Anthropic-Version":           "2023-06-01",
		"User-Agent":                  "claude-cli/2.0.0",
		"X-App":                       "cli",
		"X-Stainless-Lang":            "js",
		"X-Stainless-Runtime":         "node",
		"X-Stainless-Package-Version": "2.0.0",
		"X-Stainless-Retry-Count":     "0",
	}
	for name, want := range wantHeaders {
		if got := req.Header.Get(name); got != want {
			t.Fatalf("%s=%q want %q", name, got, want)
		}
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var payload struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v body=%s", err, body)
	}
	if payload.Model != "claude-live-test" || payload.MaxTokens != 16 ||
		len(payload.Messages) != 1 || payload.Messages[0].Role != "user" ||
		strings.TrimSpace(payload.Messages[0].Content) == "" {
		t.Fatalf("official messages body=%s", body)
	}
}

func TestAccountFamilyLive_CredentialHandlersAndRedaction(t *testing.T) {
	cases := []struct {
		name     string
		tc       upstreamE2ECase
		raw      string
		secrets  []string
		wantKind string
	}{
		{
			name: "anthropic api key",
			tc: upstreamE2ECase{
				slug: "anthropic-api-key", vendor: credentialstore.VendorAnthropic,
				authMode: credentialstore.AuthModeAPIKey, accountType: upstreamE2EAccountTypeAPIKey,
				keyEnv: upstreamE2EAnthropicKeyEnv,
			},
			raw: `anthropic-live-test-secret`,
			secrets: []string{
				"anthropic-live-test-secret",
			},
			wantKind: credentialstore.RuntimeAPIKey,
		},
		{
			name: "claude ai oauth",
			tc: upstreamE2ECase{
				slug: "claude-ai-oauth", vendor: credentialstore.VendorAnthropic,
				authMode: credentialstore.AuthModeClaudeAIOAuth, accountType: upstreamE2EAccountTypeOAuth,
				credentialJSONEnv: upstreamE2EClaudeAICredentialJSONEnv,
			},
			raw: `{"access_token":"claude-access-secret","refresh_token":"claude-refresh-secret"}`,
			secrets: []string{
				"claude-access-secret", "claude-refresh-secret",
			},
			wantKind: credentialstore.RuntimeOAuthAccessToken,
		},
		{
			name: "claude code",
			tc: upstreamE2ECase{
				slug: "claude-code", vendor: credentialstore.VendorAnthropic,
				authMode: credentialstore.AuthModeClaudeCode, accountType: upstreamE2EAccountTypeOAuth,
				credentialJSONEnv: upstreamE2EClaudeCodeCredentialEnv,
			},
			raw: `{"session_token":"claude-session-secret","refresh_token":"claude-code-refresh-secret"}`,
			secrets: []string{
				"claude-session-secret", "claude-code-refresh-secret",
			},
			wantKind: credentialstore.RuntimeSessionToken,
		},
		{
			name: "gemini api key",
			tc: upstreamE2ECase{
				slug: "gemini-aistudio", vendor: credentialstore.VendorGemini,
				authMode: credentialstore.AuthModeAIStudioAPIKey, accountType: upstreamE2EAccountTypeAPIKey,
				keyEnv: upstreamE2EGeminiKeyEnv,
			},
			raw: `gemini-live-test-secret`,
			secrets: []string{
				"gemini-live-test-secret",
			},
			wantKind: credentialstore.RuntimeAPIKey,
		},
		{
			name: "gemini code assist",
			tc: upstreamE2ECase{
				slug: "gemini-code-assist", vendor: credentialstore.VendorGemini,
				authMode: credentialstore.AuthModeCodeAssist, accountType: upstreamE2EAccountTypeOAuth,
				credentialJSONEnv: upstreamE2EGeminiCodeCredentialEnv,
			},
			raw: `{"access_token":"gemini-code-secret","refresh_token":"gemini-code-refresh-secret","project_id":"project-test"}`,
			secrets: []string{
				"gemini-code-secret", "gemini-code-refresh-secret",
			},
			wantKind: credentialstore.RuntimeSessionToken,
		},
		{
			name: "antigravity oauth",
			tc: upstreamE2ECase{
				slug: "antigravity", vendor: credentialstore.VendorAntigravity,
				authMode: credentialstore.AuthModeOAuth, accountType: upstreamE2EAccountTypeOAuth,
				credentialJSONEnv: upstreamE2EAntigravityCredentialEnv,
			},
			raw: `{"access_token":"antigravity-secret","refresh_token":"antigravity-refresh-secret","project_id":"project-test"}`,
			secrets: []string{
				"antigravity-secret", "antigravity-refresh-secret",
			},
			wantKind: credentialstore.RuntimeSessionToken,
		},
		{
			name: "kimi oauth",
			tc: upstreamE2ECase{
				slug: "kimi-oauth", vendor: credentialstore.VendorKimi,
				authMode: credentialstore.AuthModeKimiOAuth, accountType: upstreamE2EAccountTypeOAuth,
				credentialJSONEnv: upstreamE2EKimiCredentialJSONEnv,
			},
			raw: `{"access_token":"kimi-access-secret","refresh_token":"kimi-refresh-secret"}`,
			secrets: []string{
				"kimi-access-secret", "kimi-refresh-secret",
			},
			wantKind: credentialstore.RuntimeUpstreamPassthrough,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			envName := tc.tc.keyEnv
			if envName == "" {
				envName = tc.tc.credentialJSONEnv
			}
			t.Setenv(envName, tc.raw)
			credential := loadUpstreamE2ECredential(t, tc.tc)
			handler, err := credentialstore.DefaultHandlerRegistry().MustLookup(tc.tc.vendor, tc.tc.authMode)
			if err != nil {
				t.Fatalf("lookup handler: %v", err)
			}
			if err := handler.ValidatePayload(credential.payload); err != nil {
				t.Fatalf("validate payload: %v", err)
			}
			material, err := handler.RuntimeMaterial(credential.payload)
			if err != nil {
				t.Fatalf("runtime material: %v", err)
			}
			if material.Kind != tc.wantKind {
				t.Fatalf("runtime kind=%q want %q", material.Kind, tc.wantKind)
			}
			redacted := redactUpstreamE2ESecrets("payload=" + tc.raw + " material=" + material.Value)
			for _, secret := range tc.secrets {
				if strings.Contains(redacted, secret) {
					t.Fatalf("redaction leaked secret %q in %q", secret, redacted)
				}
			}
		})
	}

	t.Run("嵌套 camelCase 凭据脱敏", func(t *testing.T) {
		raw := `{"oauth":{"accessToken":"camel-access-secret","refreshToken":"camel-refresh-secret","clientSecret":"camel-client-secret","token":"generic-token-secret","setupToken":"setup-token-secret"},"service_account":{"private_key":"private-key-secret","awsSecretAccessKey":"aws-secret"}}`
		t.Setenv(upstreamE2EAntigravityCredentialEnv, raw)
		redacted := redactUpstreamE2ESecrets("payload=" + raw)
		for _, secret := range []string{
			"camel-access-secret", "camel-refresh-secret", "camel-client-secret",
			"generic-token-secret", "setup-token-secret", "private-key-secret", "aws-secret",
		} {
			if strings.Contains(redacted, secret) {
				t.Fatalf("嵌套 camelCase 凭据脱敏泄漏 %q: %q", secret, redacted)
			}
		}
	})

	t.Run("子进程环境移除真实凭据", func(t *testing.T) {
		t.Setenv(upstreamE2EAntigravityCredentialEnv, `{"access_token":"child-env-secret"}`)
		prefix := upstreamE2EAntigravityCredentialEnv + "="
		for _, item := range upstreamE2EChildEnv() {
			if strings.HasPrefix(item, prefix) {
				t.Fatalf("真实凭据环境变量进入了网关或 sidecar 子进程: %s", upstreamE2EAntigravityCredentialEnv)
			}
		}
	})
}
