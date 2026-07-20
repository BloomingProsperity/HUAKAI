package credentialacq

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// 守:device-code 的 auth_url/token_url 由运维/admin 提供,start 前必须走
// validateOAuthEndpointURL 静态闸门(https + 非私网/元数据/loopback),与
// authorization_code 路径一致。当前缺陷:openai codex operator 配置与通用
// startDeviceAuthorization 仅校验非空,不校验 scheme/host,导致明文 http /
// 内网端点(PKCE code + SSRF 纵深)可被接受。
//
// 判别 mutation:若删掉这些路径里的 validateOAuthEndpointURL 调用,下列断言
// 立刻接受 attacker URL -> 红。

func TestValidateOpenAICodexOperatorDeviceCodeConfigRejectsInsecureEndpoints(t *testing.T) {
	base := func() OAuthClientConfig {
		return OAuthClientConfig{
			Source:   ClientSourceOperatorConfig,
			AuthURL:  "https://operator.openai.example.test/device",
			TokenURL: "https://operator.openai.example.test/token",
			ClientID: "operator-client",
			Scopes:   []string{"openid", "offline_access"},
		}
	}

	// sanity: 合法 https 公网配置必须通过。
	if err := validateOpenAICodexOperatorDeviceCodeConfig(base()); err != nil {
		t.Fatalf("valid https operator config rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(cfg *OAuthClientConfig)
	}{
		{"auth_url plaintext http", func(c *OAuthClientConfig) { c.AuthURL = "http://operator.openai.example.test/device" }},
		{"auth_url metadata ip", func(c *OAuthClientConfig) { c.AuthURL = "http://169.254.169.254/device" }},
		{"auth_url private ip", func(c *OAuthClientConfig) { c.AuthURL = "https://10.1.2.3/device" }},
		{"auth_url loopback name", func(c *OAuthClientConfig) { c.AuthURL = "https://localhost/device" }},
		{"token_url plaintext http", func(c *OAuthClientConfig) { c.TokenURL = "http://operator.openai.example.test/token" }},
		{"token_url metadata host", func(c *OAuthClientConfig) { c.TokenURL = "https://metadata.google.internal/token" }},
		{"token_url private ip", func(c *OAuthClientConfig) { c.TokenURL = "https://192.168.0.9/token" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mut(&cfg)
			err := validateOpenAICodexOperatorDeviceCodeConfig(cfg)
			if err == nil {
				t.Fatalf("insecure endpoint accepted (SSRF/plaintext gate missing): %+v", cfg)
			}
			if !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("err=%v want ErrFeatureDisabled", err)
			}
		})
	}
}

func TestStartDeviceAuthorizationRejectsInsecureEndpoints(t *testing.T) {
	// startDeviceAuthorization 供 通用/copilot device_code + SSO 复用。URL 校验
	// 必须在任何网络/存储访问前发生。这里传 nil store:修复后 URL 闸门先触发,
	// 返回 ErrFeatureDisabled(URL rejected);修复前非空检查通过后落到 nil-store
	// 分支,返回的不是 ErrFeatureDisabled -> 红。
	cases := []struct {
		name              string
		authURL, tokenURL string
	}{
		{"auth_url plaintext http", "http://sso.example.test/device", "https://sso.example.test/token"},
		{"auth_url private ip", "https://10.0.0.5/device", "https://sso.example.test/token"},
		{"token_url metadata ip", "https://sso.example.test/device", "http://169.254.169.254/token"},
		{"token_url loopback", "https://sso.example.test/device", "https://127.0.0.1/token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := OAuthClientConfig{
				ClientID: "sso-client",
				AuthURL:  tc.authURL,
				TokenURL: tc.tokenURL,
				Source:   ClientSourceOperatorConfig,
				Scopes:   []string{"openid"},
			}
			_, err := startDeviceAuthorization(context.Background(), nil, StartInput{TenantID: 1, ProviderAccountID: 1}, cfg, AuthTypeDeviceCode)
			if err == nil {
				t.Fatalf("insecure endpoint accepted: %+v", cfg)
			}
			if !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("err=%v want ErrFeatureDisabled (URL gate before store/network); got non-gate error", err)
			}
			if strings.Contains(err.Error(), "session store not configured") {
				t.Fatalf("URL validation must run before nil-store guard; got store error: %v", err)
			}
		})
	}
}
