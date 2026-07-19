package codeximport

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestParseOfficialAuthJSONForcesModeAndStripsUntrustedOverrides(t *testing.T) {
	expiresAt := time.Date(2099, 7, 17, 12, 0, 0, 0, time.UTC)
	idToken := testJWT(t, map[string]any{
		"email": "codex@example.test",
		"sub":   "user-from-token",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "workspace-from-token",
		},
	})
	accessToken := testJWT(t, map[string]any{"exp": expiresAt.Unix()})
	secret := "refresh-secret-sentinel"
	input := `{
		"auth_mode":"chatgpt",
			"OPENAI_API_KEY":"stale-api-key",
			"chatgpt_plan_type":"Pro",
		"last_refresh":"2099-07-17T11:00:00Z",
		"tokens":{
			"id_token":` + quoted(idToken) + `,
			"access_token":` + quoted(accessToken) + `,
			"refresh_token":` + quoted(secret) + `,
			"account_id":"workspace-from-file",
			"vendor":"anthropic",
			"auth_mode":"api_key",
			"oauth_token_endpoint":"https://attacker.test/token",
			"client_secret":"attacker-secret"
		}
	}`
	candidates, err := Parse(input)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("Parse candidates=%d err=%v", len(candidates), err)
	}
	got := candidates[0]
	if got.Vendor != credentialstore.VendorOpenAI || got.AuthMode != credentialstore.AuthModeCodexCLIOAuth {
		t.Fatalf("mode=%s/%s", got.Vendor, got.AuthMode)
	}
	var payload map[string]any
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["access_token"] != accessToken || payload["session_token"] != accessToken || payload["refresh_token"] != secret {
		t.Fatalf("token payload 未归一：%v", payload)
	}
	if payload["expires_at"] != expiresAt.Format(time.RFC3339) {
		t.Fatalf("expires_at=%v want %s", payload["expires_at"], expiresAt.Format(time.RFC3339))
	}
	for _, forbidden := range []string{"vendor", "auth_mode", "oauth_token_endpoint", "client_secret", "OPENAI_API_KEY", "last_refresh"} {
		if _, exists := payload[forbidden]; exists {
			t.Fatalf("未批准字段 %q 进入凭据：%v", forbidden, payload)
		}
	}
	if got.ExternalAccountID != "workspace-from-token" || got.ExternalSubjectID != "user-from-token" || got.ExternalAccountEmail != "codex@example.test" {
		t.Fatalf("identity=%+v", got)
	}
	if got.AccountIDSource != accountident.SourceImportPayload {
		t.Fatalf("导入文件身份不得冒充实时受信响应：source=%q", got.AccountIDSource)
	}
	if payload["chatgpt_plan_type"] != "Pro" || got.Subscription.Label() != "openai:pro" || got.Subscription.Status != "observed" {
		t.Fatalf("auth.json 外层套餐未进入凭据候选项：payload=%v subscription=%+v", payload, got.Subscription)
	}
	redacted, _ := json.Marshal(got.RedactedContext)
	for _, forbidden := range []string{secret, accessToken, "attacker.test", "attacker-secret", "user-from-token"} {
		if strings.Contains(string(redacted), forbidden) {
			t.Fatalf("RedactedContext 泄漏 %q：%s", forbidden, redacted)
		}
	}
}

func TestParseBatchShapesAndAccessTokenAliases(t *testing.T) {
	input := "raw-access-a\n" +
		`{"accessToken":"access-b","refreshToken":"refresh-b","accountId":"account-b"}` + "\n" +
		`{"access_token":"access-c"}{"tokens":{"access_token":"access-d"}}`
	candidates, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 4 {
		t.Fatalf("candidate count=%d want 4", len(candidates))
	}
	for index, candidate := range candidates {
		if candidate.Vendor != credentialstore.VendorOpenAI || candidate.AuthMode != credentialstore.AuthModeCodexCLIOAuth {
			t.Fatalf("candidate[%d] mode=%s/%s", index, candidate.Vendor, candidate.AuthMode)
		}
		var payload map[string]any
		if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(payload["access_token"].(string)) == "" || payload["session_token"] != payload["access_token"] {
			t.Fatalf("candidate[%d] payload=%v", index, payload)
		}
	}
	if candidates[1].ExternalAccountID != "account-b" || candidates[1].AccountIDSource != accountident.SourceImportPayload {
		t.Fatalf("alias identity=%+v", candidates[1])
	}

	array, err := Parse(`["array-access-a",{"access_token":"array-access-b"}]`)
	if err != nil || len(array) != 2 {
		t.Fatalf("array candidates=%d err=%v", len(array), err)
	}
}

func TestParseRejectsAgentIdentityAPIKeyMalformedAndOversizedMaterial(t *testing.T) {
	cases := []string{
		`{"agent_identity":{"agent_runtime_id":"runtime","agent_private_key":"private"},"tokens":{"access_token":"access"}}`,
		`{"auth_mode":"agentIdentity","tokens":{"access_token":"stale-access"}}`,
		`{"personal_access_token":"personal-token"}`,
		`{"auth_mode":"personalAccessToken","tokens":{"access_token":"stale-access"}}`,
		`{"auth_mode":"apiKey","tokens":{"access_token":"stale-access"}}`,
		`{"auth_mode":"unknownMode","tokens":{"access_token":"stale-access"}}`,
		`{"OPENAI_API_KEY":"sk-only"}`,
		`{"tokens":"not-an-object"}`,
		`{"tokens":{"refresh_token":"refresh-only"}}`,
		`{"tokens":{"access_token":"broken"}`,
		`123`,
		"access token with spaces",
		strings.Repeat("x", maxTokenBytes+1),
	}
	for _, input := range cases {
		if _, err := Parse(input); !errors.Is(err, credentialacq.ErrInvalidImportBody) {
			t.Fatalf("input=%q err=%v want ErrInvalidImportBody", input, err)
		}
	}
}

func testJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return header + "." + payload + ".signature"
}

func quoted(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
