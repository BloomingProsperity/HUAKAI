package userauth

import "testing"

// TestNewOAuthHTTPProviderRejectsUnsafeEndpoints 守护一点: 社交 OAuth 的
// token-exchange/JWKS/GitHub user&emails 调用携带 OAuth code、client_secret 和 bearer token,
// 因此运营者可配置的 endpoint 若是 plain-http, 或指向 private/loopback/metadata 这类字面
// IP, 必须在构造时被拒 —— 防止那些凭证被拨号到内网/metadata 主机 (与
// buildOAuthProvider 接入的拨号期 SSRF client 一起构成纵深防御)。
//
// 变异检查: 删掉 NewOAuthHTTPProvider 里的 validateOAuthEndpointURL 循环, 那么每个 reject
// case 都会构造成功 → 红。被接受的 case 证明我们没有过度拒绝合法的
// https 公网配置。
func TestNewOAuthHTTPProviderRejectsUnsafeEndpoints(t *testing.T) {
	base := func() OAuthConfig {
		return OAuthConfig{
			Provider: "google", ClientID: "cid",
			AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
			JWKSURL:  "https://www.googleapis.com/oauth2/v3/certs",
		}
	}
	reject := []struct {
		name   string
		mutate func(*OAuthConfig)
	}{
		{"token_plain_http", func(c *OAuthConfig) { c.TokenURL = "http://oauth2.googleapis.com/token" }},
		{"token_loopback", func(c *OAuthConfig) { c.TokenURL = "https://127.0.0.1/token" }},
		{"token_metadata", func(c *OAuthConfig) { c.TokenURL = "https://169.254.169.254/token" }},
		{"jwks_private", func(c *OAuthConfig) { c.JWKSURL = "https://10.0.0.5/certs" }},
		{"auth_link_local", func(c *OAuthConfig) { c.AuthURL = "https://169.254.1.2/auth" }},
		// special-use / 非公网网段: 朴素的 loopback+private+link-local 检查会漏掉,
		// 但共享的 auth.IsPublicOAuthIP 策略会拒绝它们: CGNAT 100.64/10 + benchmark 198.18/15。
		{"token_cgnat", func(c *OAuthConfig) { c.TokenURL = "https://100.100.100.200/token" }},
		{"jwks_benchmark", func(c *OAuthConfig) { c.JWKSURL = "https://198.18.0.1/certs" }},
	}
	for _, tc := range reject {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(&cfg)
			if _, err := NewOAuthHTTPProvider(cfg, nil); err == nil {
				t.Fatalf("%s: unsafe OAuth endpoint must be rejected at construction", tc.name)
			}
		})
	}
	if _, err := NewOAuthHTTPProvider(base(), nil); err != nil {
		t.Fatalf("legitimate https public endpoints must be accepted; got %v", err)
	}
}
