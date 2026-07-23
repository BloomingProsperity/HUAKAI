package credentialacq

import "testing"

// TestMergeOAuthClientConfig_PublicCLIIgnoresIdentityOverride 判别性守卫:公开 CLI 客户端的
// 整套身份(client_id/secret、授权与令牌端点、redirect_uri、scope、source)已在提供商侧注册为
// 固定值,任何 per-request override 必须被忽略——redirect 被改会 redirect_mismatch + 开放重定向,
// client_id/secret/端点被改则冒用他人客户端身份。删除 mergeOAuthClientConfig 的 public_cli 守卫
// 会使本测试转红。
func TestMergeOAuthClientConfig_PublicCLIIgnoresIdentityOverride(t *testing.T) {
	const (
		regID       = "builtin-client-id"
		regSecret   = "builtin-secret"
		regAuthURL  = "https://accounts.example.test/authorize"
		regTokenURL = "https://oauth2.example.test/token"
		regRedirect = "http://127.0.0.1:1455/auth/callback"
	)
	base := OAuthClientConfig{
		ClientID: regID, ClientSecret: regSecret, AuthURL: regAuthURL,
		TokenURL: regTokenURL, RedirectURI: regRedirect,
		Scopes: []string{"scope.a"}, Source: ClientSourcePublicCLI,
	}
	got := mergeOAuthClientConfig(base, OAuthClientConfig{
		ClientID: "attacker-id", ClientSecret: "attacker-secret",
		AuthURL: "https://evil.test/authorize", TokenURL: "https://evil.test/token",
		RedirectURI: "http://localhost:54545/steal", Scopes: []string{"scope.evil"},
		Source: ClientSourceOperatorConfig,
	})
	if got.ClientID != regID || got.ClientSecret != regSecret ||
		got.AuthURL != regAuthURL || got.TokenURL != regTokenURL ||
		got.RedirectURI != regRedirect || got.Source != ClientSourcePublicCLI ||
		len(got.Scopes) != 1 || got.Scopes[0] != "scope.a" {
		t.Fatalf("公开 CLI 身份 override 未被完整忽略:%+v", got)
	}
}

// TestMergeOAuthClientConfig_NonPublicAppliesOverride 守卫非公开源(operator config)不被误伤:
// 各字段 override 正常生效。
func TestMergeOAuthClientConfig_NonPublicAppliesOverride(t *testing.T) {
	base := OAuthClientConfig{
		ClientID: "base-id", RedirectURI: "http://127.0.0.1:1455/auth/callback",
		Source: ClientSourceOperatorConfig,
	}
	custom := "https://ops.example.test/callback"
	got := mergeOAuthClientConfig(base, OAuthClientConfig{ClientID: "ops-id", RedirectURI: custom})
	if got.ClientID != "ops-id" || got.RedirectURI != custom {
		t.Fatalf("非公开源 override 应生效:client_id=%q redirect=%q", got.ClientID, got.RedirectURI)
	}
}
