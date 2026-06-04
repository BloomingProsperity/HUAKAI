package userauth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type oauthRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn oauthRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func oauthStubResponse(status int, body, contentType string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// Mutation guards: JSON-only token parsing breaks querystring_token; skipping
// callback unwrap breaks /me; using nickname instead of openid breaks subject.
func TestOAuthQQAuthorizationAndIdentityExtraction(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		tokenBody   string
		contentType string
		wantToken   string
	}{
		{
			name:        "querystring_token",
			tokenBody:   "access_token=query-token&expires_in=777",
			contentType: "application/x-www-form-urlencoded",
			wantToken:   "query-token",
		},
		{
			name:        "json_token",
			tokenBody:   `{"access_token":"json-token","token_type":"Bearer"}`,
			contentType: "application/json",
			wantToken:   "json-token",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sawTokenForm, sawOpenID, sawUserInfo bool
			client := &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Host + req.URL.Path {
				case "graph.qq.com/oauth2.0/token":
					sawTokenForm = true
					if err := req.ParseForm(); err != nil {
						t.Fatalf("parse QQ token form: %v", err)
					}
					if req.PostForm.Get("client_id") != "qq-appid" ||
						req.PostForm.Get("client_secret") != "qq-secret" ||
						req.PostForm.Get("code") != "qq-code" ||
						req.PostForm.Get("redirect_uri") != "https://app.example/cb" {
						t.Fatalf("QQ token form mismatch: %v", req.PostForm)
					}
					return oauthStubResponse(200, tc.tokenBody, tc.contentType), nil
				case "graph.qq.com/oauth2.0/me":
					sawOpenID = true
					if got := req.URL.Query().Get("access_token"); got != tc.wantToken {
						t.Fatalf("QQ /me access_token=%q want %q", got, tc.wantToken)
					}
					return oauthStubResponse(200,
						`callback( {"client_id":"qq-appid","openid":"qq-openid"} );`,
						"application/javascript"), nil
				case "graph.qq.com/user/get_user_info":
					sawUserInfo = true
					q := req.URL.Query()
					if q.Get("access_token") != tc.wantToken ||
						q.Get("oauth_consumer_key") != "qq-appid" ||
						q.Get("openid") != "qq-openid" {
						t.Fatalf("QQ userinfo query mismatch: %s", req.URL.RawQuery)
					}
					return oauthStubResponse(200, `{"ret":0,"nickname":"QQ User"}`, "application/json"), nil
				default:
					t.Fatalf("unexpected QQ request: %s %s", req.Method, req.URL.String())
					return nil, nil
				}
			})}
			provider, err := NewOAuthHTTPProvider(OAuthConfig{
				Provider:     SocialProviderQQ,
				ClientID:     "qq-appid",
				ClientSecret: "qq-secret",
				RedirectURI:  "https://app.example/cb",
			}, client)
			if err != nil {
				t.Fatalf("NewOAuthHTTPProvider QQ: %v", err)
			}
			challenge := OAuthFlowChallenge{
				State: "qq-state", PKCEChallenge: "pkce-challenge",
				Nonce: "nonce", RedirectURI: "https://app.example/cb",
			}
			authURL, err := provider.AuthorizationURL(challenge)
			if err != nil {
				t.Fatalf("AuthorizationURL QQ: %v", err)
			}
			parsed, err := url.Parse(authURL)
			if err != nil {
				t.Fatalf("parse QQ auth URL: %v", err)
			}
			if parsed.Host != "graph.qq.com" || parsed.Path != "/oauth2.0/authorize" {
				t.Fatalf("QQ auth endpoint mismatch: %s", authURL)
			}
			q := parsed.Query()
			if q.Get("client_id") != "qq-appid" ||
				q.Get("redirect_uri") != "https://app.example/cb" ||
				q.Get("state") != "qq-state" {
				t.Fatalf("QQ auth query missing client_id/redirect/state: %s", parsed.RawQuery)
			}
			identity, err := provider.ExchangeVerifiedIdentity(ctx, OAuthFlowSession{
				Provider: SocialProviderQQ, RedirectURI: "https://app.example/cb",
			}, "qq-code")
			if err != nil {
				t.Fatalf("QQ ExchangeVerifiedIdentity: %v", err)
			}
			if identity.Provider != SocialProviderQQ ||
				identity.Subject != "qq-openid" ||
				identity.DisplayName != "QQ User" ||
				identity.EmailVerified {
				t.Fatalf("QQ identity mismatch: %+v", identity)
			}
			if NormalizeEmail(identity.Email) == "" {
				t.Fatalf("QQ missing synthetic email for pending-email path: %+v", identity)
			}
			if !sawTokenForm || !sawOpenID || !sawUserInfo {
				t.Fatalf("QQ flow did not hit every endpoint: token=%v openid=%v user=%v",
					sawTokenForm, sawOpenID, sawUserInfo)
			}
		})
	}
}

// Mutation guard: using openId instead of unionId fails because the fixture
// sets those to different values.
func TestOAuthDingTalkUsesUnionIDAsSubject(t *testing.T) {
	var sawToken, sawUser bool
	client := &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host + req.URL.Path {
		case "api.dingtalk.com/v1.0/oauth2/userAccessToken":
			sawToken = true
			raw, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read DingTalk token body: %v", err)
			}
			body := string(raw)
			for _, snippet := range []string{
				`"clientId":"ding-app"`,
				`"clientSecret":"ding-secret"`,
				`"code":"ding-code"`,
				`"grantType":"authorization_code"`,
			} {
				if !strings.Contains(body, snippet) {
					t.Fatalf("DingTalk token body missing %s: %s", snippet, body)
				}
			}
			return oauthStubResponse(200, `{"accessToken":"ding-token"}`, "application/json"), nil
		case "api.dingtalk.com/v1.0/contact/users/me":
			sawUser = true
			if got := req.Header.Get("x-acs-dingtalk-access-token"); got != "ding-token" {
				t.Fatalf("DingTalk access header=%q want ding-token", got)
			}
			return oauthStubResponse(200, `{
				"unionId":"stable-union",
				"openId":"wrong-open-id",
				"nick":"Ding User",
				"email":"ding@example.test",
				"emailVerified":true
			}`, "application/json"), nil
		default:
			t.Fatalf("unexpected DingTalk request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	})}
	provider, err := NewOAuthHTTPProvider(OAuthConfig{
		Provider:     SocialProviderDingTalk,
		ClientID:     "ding-app",
		ClientSecret: "ding-secret",
		RedirectURI:  "https://app.example/ding",
	}, client)
	if err != nil {
		t.Fatalf("NewOAuthHTTPProvider DingTalk: %v", err)
	}
	authURL, err := provider.AuthorizationURL(OAuthFlowChallenge{
		State: "ding-state", PKCEChallenge: "pkce", RedirectURI: "https://app.example/ding",
	})
	if err != nil {
		t.Fatalf("AuthorizationURL DingTalk: %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse DingTalk auth URL: %v", err)
	}
	if parsed.Host != "login.dingtalk.com" || parsed.Path != "/oauth2/auth" {
		t.Fatalf("DingTalk auth endpoint mismatch: %s", authURL)
	}
	q := parsed.Query()
	if q.Get("client_id") != "ding-app" ||
		q.Get("redirect_uri") != "https://app.example/ding" ||
		q.Get("state") != "ding-state" ||
		q.Get("scope") != "openid" ||
		q.Get("prompt") != "consent" {
		t.Fatalf("DingTalk auth query mismatch: %s", parsed.RawQuery)
	}
	identity, err := provider.ExchangeVerifiedIdentity(context.Background(),
		OAuthFlowSession{Provider: SocialProviderDingTalk, RedirectURI: "https://app.example/ding"},
		"ding-code")
	if err != nil {
		t.Fatalf("DingTalk ExchangeVerifiedIdentity: %v", err)
	}
	if identity.Provider != SocialProviderDingTalk ||
		identity.Subject != "stable-union" ||
		identity.Subject == "wrong-open-id" ||
		identity.Email != "ding@example.test" ||
		!identity.EmailVerified {
		t.Fatalf("DingTalk identity mismatch: %+v", identity)
	}
	if !sawToken || !sawUser {
		t.Fatalf("DingTalk flow did not hit every endpoint: token=%v user=%v", sawToken, sawUser)
	}
}

// Mutation guard: ignoring the configured subject field fails because id and
// sub intentionally carry different values.
func TestOAuthNodeSeekGenericProviderUsesConfiguredSubjectField(t *testing.T) {
	var sawToken, sawUser bool
	client := &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host + req.URL.Path {
		case "oauth.nodeseek.example/token":
			sawToken = true
			if err := req.ParseForm(); err != nil {
				t.Fatalf("parse NodeSeek token form: %v", err)
			}
			if req.PostForm.Get("client_id") != "node-client" ||
				req.PostForm.Get("client_secret") != "node-secret" ||
				req.PostForm.Get("code") != "node-code" {
				t.Fatalf("NodeSeek token form mismatch: %v", req.PostForm)
			}
			return oauthStubResponse(200, `{"access_token":"node-token"}`, "application/json"), nil
		case "api.nodeseek.example/userinfo":
			sawUser = true
			if got := req.Header.Get("Authorization"); got != "Bearer node-token" {
				t.Fatalf("NodeSeek Authorization=%q want bearer token", got)
			}
			return oauthStubResponse(200, `{
				"id":"node-stable-id",
				"sub":"wrong-subject",
				"email":"node@example.test",
				"email_verified":true,
				"name":"Node User"
			}`, "application/json"), nil
		default:
			t.Fatalf("unexpected NodeSeek request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	})}
	provider, err := NewOAuthHTTPProvider(OAuthConfig{
		Provider:           SocialProviderNodeSeek,
		ClientID:           "node-client",
		ClientSecret:       "node-secret",
		AuthURL:            "https://oauth.nodeseek.example/authorize",
		TokenURL:           "https://oauth.nodeseek.example/token",
		UserURL:            "https://api.nodeseek.example/userinfo",
		RedirectURI:        "https://app.example/node",
		SubjectField:       "id",
		EmailField:         "email",
		EmailVerifiedField: "email_verified",
		DisplayNameField:   "name",
		Scopes:             []string{"profile", "email"},
	}, client)
	if err != nil {
		t.Fatalf("NewOAuthHTTPProvider NodeSeek: %v", err)
	}
	authURL, err := provider.AuthorizationURL(OAuthFlowChallenge{
		State: "node-state", PKCEChallenge: "pkce", RedirectURI: "https://app.example/node",
	})
	if err != nil {
		t.Fatalf("AuthorizationURL NodeSeek: %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse NodeSeek auth URL: %v", err)
	}
	if parsed.Host != "oauth.nodeseek.example" || parsed.Path != "/authorize" {
		t.Fatalf("NodeSeek auth endpoint mismatch: %s", authURL)
	}
	q := parsed.Query()
	if q.Get("client_id") != "node-client" ||
		q.Get("redirect_uri") != "https://app.example/node" ||
		q.Get("state") != "node-state" {
		t.Fatalf("NodeSeek auth query missing client_id/redirect/state: %s", parsed.RawQuery)
	}
	identity, err := provider.ExchangeVerifiedIdentity(context.Background(),
		OAuthFlowSession{Provider: SocialProviderNodeSeek, RedirectURI: "https://app.example/node"},
		"node-code")
	if err != nil {
		t.Fatalf("NodeSeek ExchangeVerifiedIdentity: %v", err)
	}
	if identity.Provider != SocialProviderNodeSeek ||
		identity.Subject != "node-stable-id" ||
		identity.Subject == "wrong-subject" ||
		identity.Email != "node@example.test" ||
		!identity.EmailVerified ||
		identity.DisplayName != "Node User" {
		t.Fatalf("NodeSeek identity mismatch: %+v", identity)
	}
	if !sawToken || !sawUser {
		t.Fatalf("NodeSeek flow did not hit every endpoint: token=%v user=%v", sawToken, sawUser)
	}
}

// Mutation guards: removing provider errcode logging makes required substrings
// disappear; logging raw upstream/request payloads leaks the forbidden sentinels.
func TestOAuthProviderUpstreamErrorsLogSanitizedDetails(t *testing.T) {
	tests := []struct {
		name       string
		cfg        OAuthConfig
		client     *http.Client
		code       string
		wantLog    []string
		forbidLog  []string
		wantStatus int
	}{
		{
			name: "qq_ret_msg",
			cfg: OAuthConfig{
				Provider: SocialProviderQQ, ClientID: "qq-appid", ClientSecret: "qq-secret-sentinel",
			},
			client: &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Host + req.URL.Path {
				case "graph.qq.com/oauth2.0/token":
					return oauthStubResponse(200, `{"access_token":"qq-token-sentinel"}`, "application/json"), nil
				case "graph.qq.com/oauth2.0/me":
					return oauthStubResponse(200,
						`callback( {"client_id":"qq-appid","openid":"qq-openid-sentinel"} );`,
						"application/javascript"), nil
				case "graph.qq.com/user/get_user_info":
					return oauthStubResponse(200, `{"ret":100001,"msg":"qq upstream says no"}`, "application/json"), nil
				default:
					return nil, nil
				}
			})},
			code:       "qq-code-sentinel",
			wantStatus: 200,
			wantLog:    []string{SocialProviderQQ, "100001", "qq upstream says no", `"status":200`},
			forbidLog:  []string{"qq-token-sentinel", "qq-secret-sentinel", "qq-code-sentinel", "qq-openid-sentinel", "access_token", "client_secret"},
		},
		{
			name: "dingtalk_code_message",
			cfg: OAuthConfig{
				Provider: SocialProviderDingTalk, ClientID: "ding-app", ClientSecret: "ding-secret-sentinel",
				RedirectURI: "https://app.example/ding",
			},
			client: &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host+req.URL.Path != "api.dingtalk.com/v1.0/oauth2/userAccessToken" {
					return nil, nil
				}
				return oauthStubResponse(400,
					`{"code":"InvalidCode","message":"ding upstream says no","accessToken":"ding-token-sentinel"}`,
					"application/json"), nil
			})},
			code:       "ding-code-sentinel",
			wantStatus: 400,
			wantLog:    []string{SocialProviderDingTalk, "InvalidCode", "ding upstream says no", `"status":400`},
			forbidLog:  []string{"ding-token-sentinel", "ding-secret-sentinel", "ding-code-sentinel", "accessToken", "clientSecret"},
		},
		{
			name: "generic_error_field",
			cfg: OAuthConfig{
				Provider: SocialProviderNodeSeek, ClientID: "node-client", ClientSecret: "node-secret-sentinel",
				AuthURL: "https://oauth.nodeseek.example/authorize", TokenURL: "https://oauth.nodeseek.example/token",
				UserURL: "https://api.nodeseek.example/userinfo", SubjectField: "id",
			},
			client: &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host+req.URL.Path != "oauth.nodeseek.example/token" {
					return nil, nil
				}
				return oauthStubResponse(400,
					`{"error":"invalid_grant","error_description":"node upstream says no","access_token":"node-token-sentinel"}`,
					"application/json"), nil
			})},
			code:       "node-code-sentinel",
			wantStatus: 400,
			wantLog:    []string{SocialProviderNodeSeek, "invalid_grant", "node upstream says no", `"status":400`},
			forbidLog:  []string{"node-token-sentinel", "node-secret-sentinel", "node-code-sentinel", "access_token", "client_secret"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previousLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previousLogger) })

			provider, err := NewOAuthHTTPProvider(tc.cfg, tc.client)
			if err != nil {
				t.Fatalf("NewOAuthHTTPProvider: %v", err)
			}
			_, err = provider.ExchangeVerifiedIdentity(context.Background(), OAuthFlowSession{
				Provider: tc.cfg.Provider, RedirectURI: tc.cfg.RedirectURI,
			}, tc.code)
			if !errors.Is(err, ErrSocialLoginRejected) {
				t.Fatalf("ExchangeVerifiedIdentity err=%v want ErrSocialLoginRejected", err)
			}
			logText := logs.String()
			for _, want := range tc.wantLog {
				if !strings.Contains(logText, want) {
					t.Fatalf("provider error log missing %q in %s", want, logText)
				}
			}
			for _, forbidden := range tc.forbidLog {
				if strings.Contains(logText, forbidden) {
					t.Fatalf("provider error log leaked %q in %s", forbidden, logText)
				}
			}
		})
	}
}

// Mutation guards: removing state consumption calls provider exchange on an
// attacker state; bypassing ensureSocialLoginUserAllowed accepts disabled users.
func TestOAuthNewProviderSecurityRegressionGuards(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.Now = func() time.Time { return now }
	provider := &fakeOAuthProvider{
		provider: SocialProviderQQ,
		identity: VerifiedIdentity{
			Provider: SocialProviderQQ, Subject: "qq-subject",
			Email: "qq@example.test", EmailVerified: true,
		},
	}
	svc.OAuth = NewOAuthService(provider)
	init, err := svc.StartOAuth(ctx, OAuthInitInput{TenantID: 1, Provider: SocialProviderQQ})
	if err != nil {
		t.Fatalf("StartOAuth QQ: %v", err)
	}
	if _, err := svc.CompleteOAuth(ctx, OAuthCallbackInput{
		TenantID: 1, Provider: SocialProviderQQ, State: "attacker-state", Code: "qq-code",
	}); !errors.Is(err, ErrOAuthFlowNotFound) {
		t.Fatalf("state mismatch err=%v want ErrOAuthFlowNotFound", err)
	}
	if provider.exchanges != 0 {
		t.Fatalf("state mismatch exchanged provider code %d times; want 0", provider.exchanges)
	}
	disabled := User{
		ID: 1001, TenantID: 1, Email: "disabled@example.test",
		EmailVerified: true, Status: UserStatusDisabled,
	}
	store.users[disabled.ID] = disabled
	store.byEmail[emailKey(1, disabled.Email)] = disabled.ID
	store.socialLinks[emailKey(1, SocialProviderQQ+":disabled-subject")] = disabled.ID
	svc.OAuth = NewOAuthService(&fakeOAuthProvider{
		provider: SocialProviderQQ,
		identity: VerifiedIdentity{
			Provider: SocialProviderQQ, Subject: "disabled-subject",
			Email: "disabled@example.test", EmailVerified: true,
		},
	})
	if _, err := svc.CompleteOAuth(ctx, OAuthCallbackInput{
		TenantID: 1, Provider: SocialProviderQQ, State: init.State, Code: "qq-code",
	}); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("disabled linked user err=%v want ErrUserDisabled", err)
	}
}

// Mutation guard: swallowing LinkSocialIdentity's unique-constraint rejection
// would let an already-linked provider subject attach to a second local user.
func TestOAuthLinkCollisionRejectsAccountTakeover(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 4, 12, 15, 0, 0, time.UTC)
	base := newMemoryAuthStore(now)
	store := &linkCollisionStore{memoryAuthStore: base}
	svc := NewService(store)
	svc.Now = func() time.Time { return now }
	existing, err := base.CreateUser(ctx, CreateUserParams{
		TenantID: 1, Email: "target@example.test",
		EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if existing.ID == 0 {
		t.Fatal("test user missing id")
	}
	store.rejectProvider = SocialProviderQQ
	store.rejectSubject = "already-linked-elsewhere"
	if _, err := svc.applyVerifiedSocialIdentity(ctx, 1, VerifiedIdentity{
		Provider: SocialProviderQQ, Subject: "already-linked-elsewhere",
		Email: "target@example.test", EmailVerified: true,
	}); !errors.Is(err, ErrSocialLoginRejected) {
		t.Fatalf("link collision err=%v want ErrSocialLoginRejected", err)
	}
}

// Mutation guard: deleting NewOAuthHTTPProvider endpoint validation makes the
// loopback token URL construct successfully.
func TestOAuthNewProviderEndpointGuardRejectsInternalAddresses(t *testing.T) {
	_, err := NewOAuthHTTPProvider(OAuthConfig{
		Provider:     SocialProviderNodeSeek,
		ClientID:     "node-client",
		ClientSecret: "node-secret",
		AuthURL:      "https://oauth.nodeseek.example/authorize",
		TokenURL:     "https://127.0.0.1/token",
		UserURL:      "https://api.nodeseek.example/userinfo",
		SubjectField: "id",
	}, nil)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("internal NodeSeek token endpoint err=%v want ErrInvalidInput", err)
	}
}

// Mutation guard: registering empty provider slots makes StartOAuth proceed
// instead of returning ErrOAuthProviderMissing.
func TestOAuthNewProvidersFailClosedWhenNotRegistered(t *testing.T) {
	svc := NewService(newMemoryAuthStore(time.Date(2026, 6, 4, 12, 30, 0, 0, time.UTC)))
	svc.OAuth = NewOAuthService()
	for _, provider := range []string{SocialProviderQQ, SocialProviderDingTalk, SocialProviderNodeSeek} {
		if _, err := svc.StartOAuth(context.Background(), OAuthInitInput{
			TenantID: 1, Provider: provider,
		}); !errors.Is(err, ErrOAuthProviderMissing) {
			t.Fatalf("%s unregistered err=%v want ErrOAuthProviderMissing", provider, err)
		}
	}
}

type linkCollisionStore struct {
	*memoryAuthStore
	rejectProvider string
	rejectSubject  string
}

func (s *linkCollisionStore) LinkSocialIdentity(ctx context.Context,
	tenantID, userID int64, provider, subject string,
) (User, error) {
	if normalizeSocialProvider(provider) == s.rejectProvider &&
		strings.TrimSpace(subject) == s.rejectSubject {
		return User{}, ErrSocialLoginRejected
	}
	return s.memoryAuthStore.LinkSocialIdentity(ctx, tenantID, userID, provider, subject)
}
