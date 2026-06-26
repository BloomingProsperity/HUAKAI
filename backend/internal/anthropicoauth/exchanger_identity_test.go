package anthropicoauth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

// TestExchangeCapturesUpstreamAccountIdentity 证明：当响应携带 account.uuid 时，
// token-exchange 路径会把上游账户身份（account.uuid + email）写入 candidate 的 RedactedContext；
// 并且证明：当响应不含 account.uuid 时，仍会产出一个有效的 candidate，既无上游 id 也无密钥泄露。
// 两条路径在同一个 test 中运行，因此任一路径出现回归都能被看到：这是自证的「正确 vs 降级」对照。
func TestExchangeCapturesUpstreamAccountIdentity(t *testing.T) {
	t.Run("with_account_uuid", func(t *testing.T) {
		candidate := runAnthropicExchange(t, map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"id_token":      "id-1",
			"expires_in":    3600,
			"account":       map[string]any{"uuid": "acc-uuid-1", "email_address": "owner@example.test"},
		})
		gotID, _ := candidate.RedactedContext[credentialacq.RedactedKeyUpstreamAccountID].(string)
		if gotID != "acc-uuid-1" {
			// MUTATION: 删掉新增的 Account.UUID json 字段（或不再调用
			// ExtractAnthropic），此 id 就会为空 -> 变红。
			t.Fatalf("upstream_account_id = %q, want acc-uuid-1", gotID)
		}
		if candidate.ExternalAccountID != "acc-uuid-1" {
			t.Fatalf("ExternalAccountID = %q, want acc-uuid-1", candidate.ExternalAccountID)
		}
		gotEmail, _ := candidate.RedactedContext[credentialacq.RedactedKeyUpstreamAccountEmail].(string)
		if gotEmail != "owner@example.test" {
			t.Fatalf("upstream_account_email = %q, want owner@example.test", gotEmail)
		}
		if src, _ := candidate.RedactedContext[credentialacq.RedactedKeyAccountIDSource].(string); src == "" {
			t.Fatalf("account_id_source must be recorded, got empty")
		}
		assertNoSecretLeak(t, candidate)
	})

	t.Run("without_account_uuid_degrades", func(t *testing.T) {
		candidate := runAnthropicExchange(t, map[string]any{
			"access_token":  "access-2",
			"refresh_token": "refresh-2",
			"id_token":      "id-2",
			"expires_in":    3600,
			"account":       map[string]any{"email_address": "owner2@example.test"},
		})
		// 降级路径：仍是有效的 candidate，但不会凭空捏造上游 id。
		if candidate.ExternalAccountID != "" {
			t.Fatalf("ExternalAccountID = %q, want empty when account.uuid absent", candidate.ExternalAccountID)
		}
		if _, present := candidate.RedactedContext[credentialacq.RedactedKeyUpstreamAccountID]; present {
			t.Fatalf("upstream_account_id key must be absent when no account.uuid was returned")
		}
		if len(candidate.Payload) == 0 {
			t.Fatalf("degraded path must still return a usable candidate payload")
		}
		assertNoSecretLeak(t, candidate)
	})
}

// runAnthropicExchange 针对一个返回 respBody 的桩 token endpoint 驱动
// StartOAuthFlow + CompleteOAuthCallback，并返回最终得到的 credential candidate。
func runAnthropicExchange(t *testing.T, respBody map[string]any) credentialacq.CredentialCandidate {
	t.Helper()
	now := time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC)
	store := testStore(t, now)
	var start credentialacq.OAuthStartResult
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var got map[string]string
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if got["code_verifier"] != start.CodeVerifier {
			return jsonHTTPResponse(t, http.StatusBadRequest, map[string]any{"error": "bad_verifier"}), nil
		}
		return jsonHTTPResponse(t, http.StatusOK, respBody), nil
	})}
	exchanger := Exchanger{Config: OAuthConfig("https://huakai.example.test/callback"), HTTPClient: client, Now: func() time.Time { return now }}
	registry := credentialacq.NewExchangerRegistry()
	if err := RegisterInto(registry, exchanger); err != nil {
		t.Fatalf("RegisterInto: %v", err)
	}
	start = startAnthropicFlow(t, store, exchanger, 401)

	candidate, session, err := credentialacq.CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "auth-code", registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	if session.Status != credentialacq.StatusValidated {
		t.Fatalf("status=%s want validated", session.Status)
	}
	return candidate
}

// assertNoSecretLeak 证明 RedactedContext 中的身份元数据既不含密钥标记，
// 也不含任何原始 token 子串 —— 只有提取出来的非密钥 id/email。
func assertNoSecretLeak(t *testing.T, candidate credentialacq.CredentialCandidate) {
	t.Helper()
	raw, err := json.Marshal(candidate.RedactedContext)
	if err != nil {
		t.Fatalf("marshal redacted context: %v", err)
	}
	s := string(raw)
	if strings.Contains(s, "[REDACTED]") {
		t.Fatalf("redacted context tripped secret scrubber: %s", s)
	}
	// 原始的 access/refresh token 以及 id_token JWT 绝不应出现在非密钥的元数据 context 中。
	// 这些 fixture 使用了有辨识度的 token 取值，不会与别处断言的合法 account uuid / email 冲突。
	for _, secret := range []string{"access-1", "access-2", "refresh-1", "refresh-2", `"id_token"`} {
		if strings.Contains(s, secret) {
			t.Fatalf("redacted context leaked secret substring %q: %s", secret, s)
		}
	}
}
