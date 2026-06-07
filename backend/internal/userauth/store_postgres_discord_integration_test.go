//go:build integration_pg

package userauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPGDiscordCompleteOAuthLinksVerifiedEmail(t *testing.T) {
	ctx := context.Background()
	pool := openUserAuthProfilePool(t, ctx)
	t.Cleanup(pool.Close)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	tenantID := seedUserAuthProfileTenant(t, ctx, pool, "discord-oauth-"+suffix)
	t.Cleanup(func() { cleanupDiscordOAuthTenant(t, ctx, pool, tenantID) })

	var sawToken, sawUser bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth2/token":
			sawToken = true
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			if r.PostForm.Get("client_id") != "discord-client" ||
				r.PostForm.Get("client_secret") != "discord-secret" ||
				r.PostForm.Get("code") != "discord-code" {
				t.Fatalf("Discord token form mismatch: %v", r.PostForm)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "discord-token"})
		case "/api/users/@me":
			sawUser = true
			if got := r.Header.Get("Authorization"); got != "Bearer discord-token" {
				t.Fatalf("Discord Authorization=%q want bearer token", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "discord-pg-subject",
				"email":       "discord-pg@example.test",
				"verified":    true,
				"global_name": "Discord PG",
				"username":    "wrong-fallback",
			})
		default:
			t.Fatalf("unexpected Discord upstream request: %s %s", r.Method, r.URL.String())
		}
	}))
	t.Cleanup(upstream.Close)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	provider, err := NewOAuthHTTPProvider(OAuthConfig{
		Provider:     SocialProviderDiscord,
		ClientID:     "discord-client",
		ClientSecret: "discord-secret",
		AuthURL:      "https://discord-oauth.example.test/oauth2/authorize",
		TokenURL:     "https://discord-oauth.example.test/api/oauth2/token",
		UserURL:      "https://discord-oauth.example.test/api/users/@me",
		RedirectURI:  "https://app.example.test/auth/discord/callback",
	}, &http.Client{Transport: rewriteOAuthHostTransport{target: upstreamURL}})
	if err != nil {
		t.Fatalf("NewOAuthHTTPProvider Discord: %v", err)
	}
	keys, err := credentialstore.NewStaticKeyProvider("userauth-pg-test", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	svc := NewService(NewPostgresStoreWithKeys(pool, keys))
	svc.Now = func() time.Time { return time.Now().UTC() }
	svc.OAuth = NewOAuthService(provider)

	init, err := svc.StartOAuth(ctx, OAuthInitInput{TenantID: tenantID, Provider: "discord"})
	if err != nil {
		t.Fatalf("StartOAuth Discord: %v", err)
	}
	user, err := svc.CompleteOAuth(ctx, OAuthCallbackInput{
		TenantID: tenantID, Provider: "discord", State: init.State, Code: "discord-code",
	})
	if err != nil {
		t.Fatalf("CompleteOAuth Discord: %v", err)
	}
	if user.TenantID != tenantID ||
		user.Email != "discord-pg@example.test" ||
		user.DisplayName != "Discord PG" ||
		user.SocialLoginProvider != SocialProviderDiscord ||
		!user.EmailVerified {
		t.Fatalf("Discord PG user mismatch: %+v", user)
	}
	var linkedUserID int64
	if err := pool.QueryRow(ctx, `
SELECT user_id FROM social_identity_links
WHERE tenant_id=$1 AND provider='discord' AND subject='discord-pg-subject'
`, tenantID).Scan(&linkedUserID); err != nil {
		t.Fatalf("read Discord social identity link: %v", err)
	}
	if linkedUserID != user.ID {
		t.Fatalf("Discord link user_id=%d want %d", linkedUserID, user.ID)
	}
	if !sawToken || !sawUser {
		t.Fatalf("Discord integration did not hit every upstream endpoint: token=%v user=%v", sawToken, sawUser)
	}
}

type rewriteOAuthHostTransport struct {
	target *url.URL
}

func (t rewriteOAuthHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	copyURL := *clone.URL
	copyURL.Scheme = t.target.Scheme
	copyURL.Host = t.target.Host
	clone.URL = &copyURL
	clone.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func cleanupDiscordOAuthTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	for _, stmt := range []string{
		`DELETE FROM oauth_flow_sessions WHERE tenant_id=$1`,
		`DELETE FROM social_identity_links WHERE tenant_id=$1`,
		`DELETE FROM users WHERE tenant_id=$1`,
		`DELETE FROM tenants WHERE id=$1`,
	} {
		if _, err := pool.Exec(ctx, stmt, tenantID); err != nil && !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("cleanup Discord OAuth tenant with %q: %v", stmt, err)
		}
	}
}
