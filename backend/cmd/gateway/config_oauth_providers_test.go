package main

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// Mutation guard: registering providers with only half the required credentials
// or without NodeSeek's configured subject field makes this test fail.
func TestBuildUserOAuthServiceNewProvidersFailClosedWithoutCompleteCredentials(t *testing.T) {
	clearSocialOAuthProviderEnv(t)
	t.Setenv("HUAKAI_QQ_OAUTH_CLIENT_ID", "qq-id")
	t.Setenv("HUAKAI_DINGTALK_OAUTH_CLIENT_SECRET", "ding-secret")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_CLIENT_ID", "node-id")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_CLIENT_SECRET", "node-secret")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_AUTH_URL", "https://oauth.nodeseek.example/authorize")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_TOKEN_URL", "https://oauth.nodeseek.example/token")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_USERINFO_URL", "https://api.nodeseek.example/userinfo")

	svc := buildUserOAuthService(nil)
	for _, provider := range []string{
		userauth.SocialProviderQQ,
		userauth.SocialProviderDingTalk,
		userauth.SocialProviderNodeSeek,
	} {
		if _, ok := svc.Provider(provider); ok {
			t.Fatalf("%s registered with incomplete credentials/config", provider)
		}
	}
}

// Mutation guard: omitting any new provider registration branch makes the
// corresponding Provider lookup fail despite complete config.
func TestBuildUserOAuthServiceRegistersConfiguredQQDingTalkAndNodeSeek(t *testing.T) {
	clearSocialOAuthProviderEnv(t)
	t.Setenv("HUAKAI_QQ_OAUTH_CLIENT_ID", "qq-id")
	t.Setenv("HUAKAI_QQ_OAUTH_CLIENT_SECRET", "qq-secret")
	t.Setenv("HUAKAI_QQ_OAUTH_REDIRECT_URI", "https://app.example/qq")
	t.Setenv("HUAKAI_DINGTALK_OAUTH_CLIENT_ID", "ding-id")
	t.Setenv("HUAKAI_DINGTALK_OAUTH_CLIENT_SECRET", "ding-secret")
	t.Setenv("HUAKAI_DINGTALK_OAUTH_REDIRECT_URI", "https://app.example/ding")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_CLIENT_ID", "node-id")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_CLIENT_SECRET", "node-secret")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_AUTH_URL", "https://oauth.nodeseek.example/authorize")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_TOKEN_URL", "https://oauth.nodeseek.example/token")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_USERINFO_URL", "https://api.nodeseek.example/userinfo")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_SUBJECT_FIELD", "id")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_EMAIL_FIELD", "email")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_EMAIL_VERIFIED_FIELD", "email_verified")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_DISPLAY_NAME_FIELD", "name")

	svc := buildUserOAuthService(nil)
	for _, provider := range []string{
		userauth.SocialProviderQQ,
		userauth.SocialProviderDingTalk,
		userauth.SocialProviderNodeSeek,
	} {
		if _, ok := svc.Provider(provider); !ok {
			t.Fatalf("%s was not registered despite complete credentials/config", provider)
		}
	}
}

// Mutation guard: accidentally wiring removed providers makes these inert
// names appear in OAuthService.Provider.
func TestBuildUserOAuthServiceDoesNotWireRemovedProviders(t *testing.T) {
	clearSocialOAuthProviderEnv(t)
	t.Setenv("HUAKAI_WECHAT_OAUTH_CLIENT_ID", "wechat-id")
	t.Setenv("HUAKAI_WECHAT_OAUTH_CLIENT_SECRET", "wechat-secret")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_CLIENT_ID", "linuxdo-id")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_CLIENT_SECRET", "linuxdo-secret")

	svc := buildUserOAuthService(nil)
	for _, provider := range []string{userauth.SocialProviderWeChat, userauth.SocialProviderLinuxDo} {
		if _, ok := svc.Provider(provider); ok {
			t.Fatalf("%s must remain inert in this slice", provider)
		}
	}
}

func clearSocialOAuthProviderEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HUAKAI_GOOGLE_OAUTH_CLIENT_ID",
		"HUAKAI_GOOGLE_OAUTH_CLIENT_SECRET",
		"HUAKAI_GITHUB_OAUTH_CLIENT_ID",
		"HUAKAI_GITHUB_OAUTH_CLIENT_SECRET",
		"HUAKAI_QQ_OAUTH_CLIENT_ID",
		"HUAKAI_QQ_OAUTH_CLIENT_SECRET",
		"HUAKAI_QQ_OAUTH_REDIRECT_URI",
		"HUAKAI_QQ_OAUTH_AUTH_URL",
		"HUAKAI_QQ_OAUTH_TOKEN_URL",
		"HUAKAI_QQ_OAUTH_OPENID_URL",
		"HUAKAI_QQ_OAUTH_USER_URL",
		"HUAKAI_DINGTALK_OAUTH_CLIENT_ID",
		"HUAKAI_DINGTALK_OAUTH_CLIENT_SECRET",
		"HUAKAI_DINGTALK_OAUTH_REDIRECT_URI",
		"HUAKAI_DINGTALK_OAUTH_AUTH_URL",
		"HUAKAI_DINGTALK_OAUTH_TOKEN_URL",
		"HUAKAI_DINGTALK_OAUTH_USER_URL",
		"HUAKAI_NODESEEK_OAUTH_CLIENT_ID",
		"HUAKAI_NODESEEK_OAUTH_CLIENT_SECRET",
		"HUAKAI_NODESEEK_OAUTH_REDIRECT_URI",
		"HUAKAI_NODESEEK_OAUTH_AUTH_URL",
		"HUAKAI_NODESEEK_OAUTH_TOKEN_URL",
		"HUAKAI_NODESEEK_OAUTH_USERINFO_URL",
		"HUAKAI_NODESEEK_OAUTH_SUBJECT_FIELD",
		"HUAKAI_NODESEEK_OAUTH_EMAIL_FIELD",
		"HUAKAI_NODESEEK_OAUTH_EMAIL_VERIFIED_FIELD",
		"HUAKAI_NODESEEK_OAUTH_DISPLAY_NAME_FIELD",
		"HUAKAI_NODESEEK_OAUTH_SCOPES",
		"HUAKAI_WECHAT_OAUTH_CLIENT_ID",
		"HUAKAI_WECHAT_OAUTH_CLIENT_SECRET",
		"HUAKAI_LINUXDO_OAUTH_CLIENT_ID",
		"HUAKAI_LINUXDO_OAUTH_CLIENT_SECRET",
	} {
		t.Setenv(key, "")
	}
}
