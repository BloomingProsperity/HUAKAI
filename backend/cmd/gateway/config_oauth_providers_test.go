package main

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// 变异守卫：只配置一半必需凭证、或缺少 NodeSeek 配置的 subject 字段时注册 provider，
// 会让本测试失败。
func TestBuildUserOAuthServiceNewProvidersFailClosedWithoutCompleteCredentials(t *testing.T) {
	clearSocialOAuthProviderEnv(t)
	t.Setenv("HUAKAI_QQ_OAUTH_CLIENT_ID", "qq-id")
	t.Setenv("HUAKAI_DINGTALK_OAUTH_CLIENT_SECRET", "ding-secret")
	t.Setenv("HUAKAI_DISCORD_OAUTH_CLIENT_ID", "discord-id")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_CLIENT_ID", "node-id")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_CLIENT_SECRET", "node-secret")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_AUTH_URL", "https://oauth.nodeseek.example/authorize")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_TOKEN_URL", "https://oauth.nodeseek.example/token")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_USERINFO_URL", "https://api.nodeseek.example/userinfo")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_CLIENT_ID", "linuxdo-id")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_CLIENT_SECRET", "linuxdo-secret")

	svc := buildUserOAuthService(nil)
	for _, provider := range []string{
		userauth.SocialProviderQQ,
		userauth.SocialProviderDingTalk,
		userauth.SocialProviderDiscord,
		userauth.SocialProviderNodeSeek,
		userauth.SocialProviderLinuxDo,
	} {
		if _, ok := svc.Provider(provider); ok {
			t.Fatalf("%s registered with incomplete credentials/config", provider)
		}
	}
}

// 变异守卫：遗漏任何一个新 provider 的注册分支，会使得即便配置完整，
// 对应的 Provider 查找也会失败。
func TestBuildUserOAuthServiceRegistersConfiguredQQDingTalkNodeSeekAndLinuxDo(t *testing.T) {
	clearSocialOAuthProviderEnv(t)
	t.Setenv("HUAKAI_QQ_OAUTH_CLIENT_ID", "qq-id")
	t.Setenv("HUAKAI_QQ_OAUTH_CLIENT_SECRET", "qq-secret")
	t.Setenv("HUAKAI_QQ_OAUTH_REDIRECT_URI", "https://app.example/qq")
	t.Setenv("HUAKAI_DINGTALK_OAUTH_CLIENT_ID", "ding-id")
	t.Setenv("HUAKAI_DINGTALK_OAUTH_CLIENT_SECRET", "ding-secret")
	t.Setenv("HUAKAI_DINGTALK_OAUTH_REDIRECT_URI", "https://app.example/ding")
	t.Setenv("HUAKAI_DISCORD_OAUTH_CLIENT_ID", "discord-id")
	t.Setenv("HUAKAI_DISCORD_OAUTH_CLIENT_SECRET", "discord-secret")
	t.Setenv("HUAKAI_DISCORD_OAUTH_REDIRECT_URI", "https://app.example/discord")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_CLIENT_ID", "node-id")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_CLIENT_SECRET", "node-secret")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_AUTH_URL", "https://oauth.nodeseek.example/authorize")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_TOKEN_URL", "https://oauth.nodeseek.example/token")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_USERINFO_URL", "https://api.nodeseek.example/userinfo")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_SUBJECT_FIELD", "id")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_EMAIL_FIELD", "email")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_EMAIL_VERIFIED_FIELD", "email_verified")
	t.Setenv("HUAKAI_NODESEEK_OAUTH_DISPLAY_NAME_FIELD", "name")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_CLIENT_ID", "linuxdo-id")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_CLIENT_SECRET", "linuxdo-secret")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_REDIRECT_URI", "https://app.example/linuxdo")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_AUTH_URL", "https://oauth.linuxdo.example/authorize")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_TOKEN_URL", "https://oauth.linuxdo.example/token")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_USERINFO_URL", "https://api.linuxdo.example/userinfo")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_SUBJECT_FIELD", "id")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_EMAIL_FIELD", "email")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_EMAIL_VERIFIED_FIELD", "email_verified")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_DISPLAY_NAME_FIELD", "username")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_TRUST_LEVEL_FIELD", "trust_level")
	t.Setenv("HUAKAI_LINUXDO_OAUTH_MIN_TRUST_LEVEL", "2")

	svc := buildUserOAuthService(nil)
	for _, provider := range []string{
		userauth.SocialProviderQQ,
		userauth.SocialProviderDingTalk,
		userauth.SocialProviderDiscord,
		userauth.SocialProviderNodeSeek,
		userauth.SocialProviderLinuxDo,
	} {
		if _, ok := svc.Provider(provider); !ok {
			t.Fatalf("%s was not registered despite complete credentials/config", provider)
		}
	}
}

// 变异守卫：误把已移除的 provider 接进来，会让这些废弃名字
// 出现在 OAuthService.Provider 中。
func TestBuildUserOAuthServiceDoesNotWireRemovedProviders(t *testing.T) {
	clearSocialOAuthProviderEnv(t)
	t.Setenv("HUAKAI_WECHAT_OAUTH_CLIENT_ID", "wechat-id")
	t.Setenv("HUAKAI_WECHAT_OAUTH_CLIENT_SECRET", "wechat-secret")

	svc := buildUserOAuthService(nil)
	for _, provider := range []string{userauth.SocialProviderWeChat} {
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
		"HUAKAI_DISCORD_OAUTH_CLIENT_ID",
		"HUAKAI_DISCORD_OAUTH_CLIENT_SECRET",
		"HUAKAI_DISCORD_OAUTH_REDIRECT_URI",
		"HUAKAI_DISCORD_OAUTH_AUTH_URL",
		"HUAKAI_DISCORD_OAUTH_TOKEN_URL",
		"HUAKAI_DISCORD_OAUTH_USERINFO_URL",
		"HUAKAI_DISCORD_OAUTH_SCOPES",
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
		"HUAKAI_LINUXDO_OAUTH_REDIRECT_URI",
		"HUAKAI_LINUXDO_OAUTH_AUTH_URL",
		"HUAKAI_LINUXDO_OAUTH_TOKEN_URL",
		"HUAKAI_LINUXDO_OAUTH_USERINFO_URL",
		"HUAKAI_LINUXDO_OAUTH_SUBJECT_FIELD",
		"HUAKAI_LINUXDO_OAUTH_EMAIL_FIELD",
		"HUAKAI_LINUXDO_OAUTH_EMAIL_VERIFIED_FIELD",
		"HUAKAI_LINUXDO_OAUTH_DISPLAY_NAME_FIELD",
		"HUAKAI_LINUXDO_OAUTH_TRUST_LEVEL_FIELD",
		"HUAKAI_LINUXDO_OAUTH_MIN_TRUST_LEVEL",
		"HUAKAI_LINUXDO_OAUTH_SCOPES",
	} {
		t.Setenv(key, "")
	}
}
