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
	if got := req.URL.String(); got != "https://api.anthropic.com/v1/messages?beta=true" {
		t.Fatalf("URL=%q want official default beta=true", got)
	}
	body, _ := io.ReadAll(req.Body)
	if !strings.Contains(string(body), "claude-3-5-sonnet") {
		t.Fatalf("body was not passed through: %s", string(body))
	}
}

// TestOAuthSessionAdapterBetaQueryTriState 咬住官方默认、自定义缺省与显式覆盖三态。
// 变异：删除默认注入、对 custom 无条件注入、或字符串拼接重复 beta，均会变红。
func TestOAuthSessionAdapterBetaQueryTriState(t *testing.T) {
	tests := []struct {
		name     string
		adapter  *OAuthSessionAdapter
		cred     provider.Credential
		wantURL  string
		wantBeta []string
	}{
		{
			name:    "官方缺省加 beta",
			adapter: &OAuthSessionAdapter{},
			cred:    provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: "token"},
			wantURL: "https://api.anthropic.com/v1/messages?beta=true", wantBeta: []string{"true"},
		},
		{
			name:    "官方显式 false 关闭",
			adapter: &OAuthSessionAdapter{},
			cred:    provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: "token", Extra: map[string]string{"claude_beta_query": "false"}},
			wantURL: "https://api.anthropic.com/v1/messages",
		},
		{
			name:    "自定义 base_url 缺省不添加",
			adapter: &OAuthSessionAdapter{},
			cred: provider.Credential{Type: provider.CredentialTypeUpstreamPassthrough, Value: "Bearer token", Extra: map[string]string{
				"base_url": "https://api.anthropic.com/v1/messages?tenant=7",
			}},
			wantURL: "https://api.anthropic.com/v1/messages?tenant=7",
		},
		{
			name:    "自定义显式 true 合并且保留 query",
			adapter: &OAuthSessionAdapter{},
			cred: provider.Credential{Type: provider.CredentialTypeUpstreamPassthrough, Value: "Bearer token", Extra: map[string]string{
				"base_url": "https://api.anthropic.com/v1/messages?tenant=7", "claude_beta_query": "true",
			}},
			wantURL: "https://api.anthropic.com/v1/messages?beta=true&tenant=7", wantBeta: []string{"true"},
		},
		{
			name:    "已有 beta 不覆盖也不重复",
			adapter: &OAuthSessionAdapter{Endpoint: "https://api.anthropic.com/v1/messages?beta=operator&tenant=7"},
			cred:    provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: "token", Extra: map[string]string{"claude_beta_query": "true"}},
			wantURL: "https://api.anthropic.com/v1/messages?beta=operator&tenant=7", wantBeta: []string{"operator"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := tc.adapter.BuildRequest(context.Background(), provider.BuildInput{
				InboundBody: []byte(`{"model":"claude","max_tokens":1,"messages":[]}`), Credential: tc.cred,
			})
			if err != nil {
				t.Fatalf("BuildRequest: %v", err)
			}
			if got := req.URL.String(); got != tc.wantURL {
				t.Fatalf("URL=%q want %q", got, tc.wantURL)
			}
			if got := req.URL.Query()["beta"]; !equalStrings(got, tc.wantBeta) {
				t.Fatalf("beta values=%v want %v", got, tc.wantBeta)
			}
		})
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
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
