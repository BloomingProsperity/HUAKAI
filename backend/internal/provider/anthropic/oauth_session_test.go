package anthropic

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestOAuthSessionAdapterBuildRequestUsesBearerAndNeverAPIKey(t *testing.T) {
	adapter := &OAuthSessionAdapter{}
	req, err := adapter.BuildRequest(context.Background(), provider.BuildInput{
		InboundBody: []byte(`{"model":"claude-3-5-sonnet-20241022","messages":[]}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeOAuthAccessToken,
			Value: "anthropic-oauth-access",
			Extra: map[string]string{
				"auth_mode":         credentialstore.AuthModeClaudeAIOAuth,
				"anthropic_version": "2024-10-22",
				"anthropic_beta":    "tools-2024-04-04",
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer anthropic-oauth-access" {
		t.Fatalf("Authorization=%q", got)
	}
	if got := req.Header.Get("X-API-Key"); got != "" {
		t.Fatalf("X-API-Key must stay empty for OAuth session, got %q", got)
	}
	if got := req.Header.Get("Anthropic-Version"); got != "2024-10-22" {
		t.Fatalf("Anthropic-Version=%q", got)
	}
	if got := req.Header.Get("Anthropic-Beta"); got != "tools-2024-04-04" {
		t.Fatalf("Anthropic-Beta=%q", got)
	}
	body, _ := io.ReadAll(req.Body)
	if !strings.Contains(string(body), "claude-3-5-sonnet") {
		t.Fatalf("body was not passed through: %s", string(body))
	}
}

func TestOAuthSessionAdapterRejectsAPIKeyCredential(t *testing.T) {
	_, err := (&OAuthSessionAdapter{}).BuildRequest(context.Background(), provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential:  provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant-api03-wrong"},
	})
	if err == nil {
		t.Fatal("API key credential must not be accepted by OAuth session adapter")
	}
	if strings.Contains(err.Error(), "X-API-Key") {
		t.Fatalf("error should not suggest API-key path: %v", err)
	}
}

func TestOAuthSessionAdapterRejectsExpiredAccessToken(t *testing.T) {
	_, err := (&OAuthSessionAdapter{}).BuildRequest(context.Background(), provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeOAuthAccessToken,
			Value: "stale-token",
			Extra: map[string]string{"expires_at": time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC).Format(time.RFC3339)},
		},
	})
	if !errors.Is(err, credentialstore.ErrCredentialExpired) {
		t.Fatalf("err=%v want %v", err, credentialstore.ErrCredentialExpired)
	}
}
