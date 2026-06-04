package userauth

import "testing"

// TestNewOAuthHTTPProviderRejectsUnsafeEndpoints guards social OAuth token-exchange/JWKS/
// GitHub user&emails calls carry OAuth codes, client_secret and bearer tokens, so an
// operator-configurable endpoint that is plain-http or points at a private/loopback/metadata literal
// IP must be refused at construction — preventing those credentials from being dialed to
// internal/metadata hosts (defense-in-depth alongside the dial-time SSRF client wired in
// buildOAuthProvider).
//
// Mutation check: delete the validateOAuthEndpointURL loop in NewOAuthHTTPProvider and every reject
// case constructs successfully → red. The accepted case proves we did not over-reject legitimate
// https public config.
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
		// special-use / non-public ranges that a naive loopback+private+link-local check misses but the
		// shared auth.IsPublicOAuthIP policy denies: CGNAT 100.64/10 + benchmark 198.18/15.
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
