package credentialacq

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestDefaultExchangerRegistryIncludesAntigravityOAuthAlias(t *testing.T) {
	// 回归保护:Antigravity 获取必须能通过
	// vendor 原生的 antigravity/oauth 键到达,而不只是旧的
	// gemini/antigravity credentialstore mode。
	registry := DefaultExchangerRegistry()
	exc, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth))
	if !ok {
		t.Fatal("default registry missing antigravity/oauth exchanger")
	}
	if _, ok := exc.(authorizationCodeOAuthExchanger); !ok {
		t.Fatalf("antigravity/oauth exchanger type=%T", exc)
	}
}

func TestXAIOAuthExchangerRegistered(t *testing.T) {
	registry := DefaultExchangerRegistry()
	exc, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorGrok, credentialstore.AuthModeXAIOAuth))
	if !ok {
		t.Fatal("default registry missing grok/xai_oauth exchanger")
	}
	if _, ok := exc.(xaiDeviceCodeExchanger); !ok {
		t.Fatalf("grok/xai_oauth exchanger type=%T，期望 xaiDeviceCodeExchanger", exc)
	}
}

func TestXAIOAuthConfigEndpointsAndClient(t *testing.T) {
	const wantClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	const wantScope = "openid profile email offline_access grok-cli:access api:access"
	now := time.Date(2026, 6, 7, 8, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != xaiOAuthDeviceURL {
			t.Fatalf("请求了非固定 xAI 设备端点：%s", r.URL)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") {
			t.Fatalf("content-type=%q，期望表单", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.PostForm.Get("client_id") != wantClientID || r.PostForm.Get("scope") != wantScope {
			t.Fatalf("xAI 公共客户端合同被改写：%v", r.PostForm)
		}
		return jsonHTTPResponse(t, map[string]any{
			"device_code": "xai-device", "user_code": "XAI-1234",
			"verification_uri": "https://auth.x.ai/activate", "expires_in": 900, "interval": 5,
		}), nil
	})}
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", []byte(strings.Repeat("x", 32)))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresSessionStoreWithKeys(newTestSessionDB(now), keys).WithNow(func() time.Time { return now })
	start, err := newXAIDeviceCodeExchanger().StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 701,
		Vendor: "attacker", AuthMode: "attacker",
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		ClientID: "attacker", AuthURL: "https://attacker.example/device", TokenURL: "https://attacker.example/token",
		Scopes: []string{"attacker"}, Source: ClientSourceOperatorConfig, HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("启动 xAI 设备码：%v", err)
	}
	if start.AuthType != AuthTypeDeviceCode || start.UserCode != "XAI-1234" || start.AuthorizeURL != "https://auth.x.ai/activate" {
		t.Fatalf("设备码启动结果错误：%+v", start)
	}
	if start.Session.Vendor != credentialstore.VendorGrok || start.Session.AuthMode != credentialstore.AuthModeXAIOAuth ||
		start.Session.ClientIdentitySource != ClientSourcePublicCLI {
		t.Fatalf("xAI 会话未锁定公共客户端身份：%+v", start.Session)
	}
}

// TestXAIDevicePollVerifiesOIDCIdentity 守护设备码换令牌后的稳定账号身份来源。
func TestXAIOAuthExchangeVerifiesOIDCIdentity(t *testing.T) {
	now := time.Date(2026, 7, 21, 18, 0, 0, 0, time.UTC)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var rawIDToken, rawAccessToken string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/oauth2/device/code":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.PostForm.Get("client_id") != xaiOAuthClientID || r.PostForm.Get("scope") != xaiOAuthScope {
				t.Fatalf("设备码启动表单错误：%v", r.PostForm)
			}
			return jsonHTTPResponse(t, map[string]any{
				"device_code": "xai-device", "user_code": "XAI-CODE",
				"verification_uri": "https://auth.x.ai/activate", "expires_in": 900, "interval": 5,
			}), nil
		case "/oauth2/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.PostForm.Get("grant_type") != oauthDeviceCodeGrantType || r.PostForm.Get("device_code") != "xai-device" {
				t.Fatalf("设备码轮询表单错误：%v", r.PostForm)
			}
			body, _ := json.Marshal(map[string]any{
				"access_token": rawAccessToken, "refresh_token": "xai-refresh",
				"id_token": rawIDToken, "expires_in": 3600,
			})
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		case "/.well-known/jwks.json":
			body, _ := json.Marshal(map[string]any{"keys": []map[string]any{{
				"kid": "xai-key", "kty": "EC", "crv": "P-256", "alg": "ES256", "use": "sig",
				"x": base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))),
				"y": base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32))),
			}}})
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		default:
			return nil, errors.New("unexpected OAuth request path: " + r.URL.Path)
		}
	})}
	keys, err := credentialstore.NewStaticKeyProvider("xai-oidc-test", []byte(strings.Repeat("z", 32)))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresSessionStoreWithKeys(newTestSessionDB(now), keys).WithNow(func() time.Time { return now })
	start, err := newXAIDeviceCodeExchanger().StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 702,
		Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": xaiOIDCIssuer, "aud": xaiOAuthClientID, "sub": "xai-subject-1",
		"email": "xai-owner@example.test",
		"iat":   now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	token.Header["kid"] = "xai-key"
	rawIDToken, err = token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	accessToken := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": xaiOIDCIssuer, "aud": xaiOAuthClientID, "sub": "xai-subject-1", "team_id": "xai-team-1",
		"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	accessToken.Header["kid"] = "xai-key"
	rawAccessToken, err = accessToken.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	validAccessToken := rawAccessToken
	candidate, err := PollDeviceCodeToken(
		context.Background(), start.Session, OAuthClientConfig{},
		WithDeviceCodeHTTPClient(client), WithDeviceCodeNow(func() time.Time { return now }), WithDeviceCodeSingleAttempt(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.ExternalSubjectID != "xai-subject-1" || candidate.ExternalAccountID != "xai-team-1" ||
		candidate.ExternalAccountEmail != "xai-owner@example.test" || candidate.AccountIDSource != accountident.SourceXAIOIDCSubject {
		t.Fatalf("xAI OIDC 身份未接入候选项：%+v", candidate)
	}
	var payload map[string]any
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["refresh_token"] != "xai-refresh" || payload["client_id_source"] != ClientSourcePublicCLI {
		t.Fatalf("xAI 刷新材料或客户端来源丢失：%v", payload)
	}
	missingScopeToken := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": xaiOIDCIssuer, "aud": xaiOAuthClientID, "sub": "xai-subject-1",
		"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	missingScopeToken.Header["kid"] = "xai-key"
	rawAccessToken, err = missingScopeToken.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PollDeviceCodeToken(
		context.Background(), start.Session, OAuthClientConfig{},
		WithDeviceCodeHTTPClient(client), WithDeviceCodeNow(func() time.Time { return now }), WithDeviceCodeSingleAttempt(),
	); !errors.Is(err, ErrInvalidTokenShape) {
		t.Fatalf("缺少账号范围的 xAI OAuth err=%v，期望 ErrInvalidTokenShape", err)
	}
	mismatchedSubjectToken := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": xaiOIDCIssuer, "aud": xaiOAuthClientID, "sub": "other-subject", "team_id": "xai-team-1",
		"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	mismatchedSubjectToken.Header["kid"] = "xai-key"
	rawAccessToken, err = mismatchedSubjectToken.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PollDeviceCodeToken(
		context.Background(), start.Session, OAuthClientConfig{},
		WithDeviceCodeHTTPClient(client), WithDeviceCodeNow(func() time.Time { return now }), WithDeviceCodeSingleAttempt(),
	); !errors.Is(err, ErrInvalidTokenShape) {
		t.Fatalf("跨主体 xAI token err=%v，期望 ErrInvalidTokenShape", err)
	}
	wrongAudienceToken := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": xaiOIDCIssuer, "aud": "other-client", "sub": "xai-subject-1", "team_id": "xai-team-1",
		"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	wrongAudienceToken.Header["kid"] = "xai-key"
	rawAccessToken, err = wrongAudienceToken.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PollDeviceCodeToken(
		context.Background(), start.Session, OAuthClientConfig{},
		WithDeviceCodeHTTPClient(client), WithDeviceCodeNow(func() time.Time { return now }), WithDeviceCodeSingleAttempt(),
	); !errors.Is(err, ErrInvalidTokenShape) {
		t.Fatalf("错误 audience 的 xAI token err=%v，期望 ErrInvalidTokenShape", err)
	}
	rawAccessToken = validAccessToken
	recovered, validated, err := PollDeviceCodeFlow(
		context.Background(), store, start.Session,
		func(context.Context, Session) (CredentialCandidate, error) { return candidate, nil },
		nil, "owner", "req-xai-recovery",
	)
	if err != nil {
		t.Fatalf("设备码候选加密暂存与恢复：%v", err)
	}
	if validated.Status != StatusValidated || recovered.ExternalAccountID != "xai-team-1" || recovered.ExternalSubjectID != "xai-subject-1" {
		t.Fatalf("加密恢复后丢失已验签身份：flow=%+v candidate=%+v", validated, recovered)
	}
}

func TestAntigravityOAuthCallbackUsesPinnedPublicCLIProfile(t *testing.T) {
	now := time.Date(2026, 5, 25, 8, 0, 0, 0, time.UTC)
	var gotForm url.Values
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/token" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") {
			t.Fatalf("content-type=%q, want form", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"ag-access","refresh_token":"ag-refresh","expires_in":3600,"token_type":"Bearer"}`)),
		}, nil
	})}
	exchanger := authorizationCodeOAuthExchanger{
		vendor: credentialstore.VendorAntigravity, authMode: credentialstore.AuthModeOAuth,
		shape: TokenShapeAnySessionOrAccess, config: AntigravityPublicCLIConfig(),
		client: client, now: func() time.Time { return now },
	}
	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger("antigravity/oauth", exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}

	keys, err := credentialstore.NewStaticKeyProvider("test-v1", []byte(strings.Repeat("a", 32)))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresSessionStoreWithKeys(newTestSessionDB(now), keys).WithNow(func() time.Time { return now })
	start, err := exchanger.StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 501,
		Vendor: "antigravity", AuthMode: "oauth",
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		AuthURL: "https://attacker.example.test/authorize", TokenURL: "https://attacker.example.test/token",
		ClientID: "attacker-client", ClientSecret: "attacker-secret",
		RedirectURI: "https://attacker.example.test/callback", Scopes: []string{"attacker-scope"},
		Source: ClientSourceOperatorConfig,
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	candidate, session, err := CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "ag-auth-code", registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	if session.Status != StatusValidated {
		t.Fatalf("status=%s want %s", session.Status, StatusValidated)
	}
	for key, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "ag-auth-code",
		"client_id":     AntigravityPublicCLIConfig().ClientID,
		"client_secret": AntigravityPublicCLIConfig().ClientSecret,
		"redirect_uri":  AntigravityOAuthRedirectURI,
		"code_verifier": start.CodeVerifier,
	} {
		if got := gotForm.Get(key); got != want {
			t.Fatalf("form[%s]=%q want %q; full form=%v", key, got, want, gotForm)
		}
	}
	if !strings.HasPrefix(start.AuthorizeURL, AntigravityOAuthAuthURL+"?") || strings.Contains(start.AuthorizeURL, "attacker") {
		t.Fatalf("授权地址未锁定公开客户端合同：%s", start.AuthorizeURL)
	}
	if candidate.Vendor != "antigravity" || candidate.AuthMode != "oauth" || candidate.ProviderAccountID != 501 {
		t.Fatalf("candidate target=%s/%s account=%d", candidate.Vendor, candidate.AuthMode, candidate.ProviderAccountID)
	}
	if err := NewFinalizer(nil, credentialstore.DefaultHandlerRegistry(), nil, nil).ValidateCandidate(candidate); err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload["session_token"] != "ag-access" || payload["refresh_token"] != "ag-refresh" {
		t.Fatalf("payload=%v, want access copied to session_token and refresh preserved", payload)
	}
	if payload["client_id_source"] != ClientSourcePublicCLI {
		t.Fatalf("client_id_source=%v, want %s", payload["client_id_source"], ClientSourcePublicCLI)
	}
	if candidate.RedactedContext[RedactedKeyClientIdentitySource] != ClientSourcePublicCLI {
		t.Fatalf("redacted client source=%v, want %s", candidate.RedactedContext[RedactedKeyClientIdentitySource], ClientSourcePublicCLI)
	}
}

func TestGeminiOperatorOAuthCallbackPostsAuthorizationCodeToConfiguredTokenEndpoint(t *testing.T) {
	now := time.Date(2026, 5, 25, 8, 10, 0, 0, time.UTC)
	var gotForm url.Values
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/oauth2/token" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"gem-access","refresh_token":"gem-refresh","scope":"profile email","expires_in":1800}`)),
		}, nil
	})}
	exchanger := authorizationCodeOAuthExchanger{
		vendor: credentialstore.VendorGemini, authMode: credentialstore.AuthModeOAuth,
		shape: TokenShapeAnySessionOrAccess, client: client, now: func() time.Time { return now },
	}
	registry := NewExchangerRegistry()
	if err := registry.RegisterExchanger("gemini/oauth", exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}

	keys, err := credentialstore.NewStaticKeyProvider("test-v1", []byte(strings.Repeat("b", 32)))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresSessionStoreWithKeys(newTestSessionDB(now), keys).WithNow(func() time.Time { return now })
	start, err := exchanger.StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 502,
		Vendor: "gemini", AuthMode: "oauth",
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		AuthURL: "https://google.example.test/oauth2/auth", TokenURL: "https://google.example.test/oauth2/token",
		ClientID: "gem-client", RedirectURI: "http://127.0.0.1:1455/auth/callback",
		Scopes: []string{"profile", "email"}, Source: ClientSourceOperatorConfig,
	})
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}

	candidate, _, err := CompleteOAuthCallbackWithRegistry(context.Background(), store, start.Session.ID, start.State, "gem-auth-code", registry)
	if err != nil {
		t.Fatalf("CompleteOAuthCallbackWithRegistry: %v", err)
	}
	if gotForm.Get("code") != "gem-auth-code" || gotForm.Get("client_id") != "gem-client" || gotForm.Get("code_verifier") != start.CodeVerifier {
		t.Fatalf("form=%v, want code/client_id/code_verifier from flow", gotForm)
	}
	if candidate.Vendor != "gemini" || candidate.AuthMode != "oauth" {
		t.Fatalf("candidate mode=%s/%s, want gemini/oauth", candidate.Vendor, candidate.AuthMode)
	}
	if err := NewFinalizer(nil, credentialstore.DefaultHandlerRegistry(), nil, nil).ValidateCandidate(candidate); err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload["access_token"] != "gem-access" || payload["refresh_token"] != "gem-refresh" {
		t.Fatalf("payload=%v, want exchanged Google token material", payload)
	}
	if payload["client_id_source"] != ClientSourceOperatorConfig {
		t.Fatalf("client_id_source=%v, want %s", payload["client_id_source"], ClientSourceOperatorConfig)
	}
	if candidate.RedactedContext[RedactedKeyClientIdentitySource] != ClientSourceOperatorConfig {
		t.Fatalf("redacted client source=%v, want %s", candidate.RedactedContext[RedactedKeyClientIdentitySource], ClientSourceOperatorConfig)
	}
}

// TestValidateOAuthModeConsistencyAcceptsHealthyDefaults 守护:生产环境的默认
// registry + mode plan 必须通过 boot 期的一致性闸门。变异:把任一 OAuth mode 用 fake
// 重新注册(见 reject 测试),此基线就会变红 —— 证明该闸门确实在跑。
func TestValidateOAuthModeConsistencyAcceptsHealthyDefaults(t *testing.T) {
	if err := ValidateOAuthModeConsistency(DefaultModePlans(), DefaultExchangerRegistry()); err != nil {
		t.Fatalf("healthy default registry must pass the OAuth consistency gate: %v", err)
	}
}

// TestValidateOAuthModeConsistencyRejectsFakeExchanger 守护:任何 FlowKindOAuth mode 上的
// fake exchanger 都必须在 boot 期被抓住并指名。变异:删除 ValidateOAuthModeConsistency 中的
// pkceFakeExchanger 类型断言,本测试就会变红(注入的 fake 会通过)。
func TestValidateOAuthModeConsistencyRejectsFakeExchanger(t *testing.T) {
	registry := DefaultExchangerRegistry()
	key := exchangerKey(credentialstore.VendorGemini, credentialstore.AuthModeAntigravity)
	if err := registry.RegisterOrReplaceExchanger(key, NewPKCEFakeExchanger(TokenShapeAnySessionOrAccess)); err != nil {
		t.Fatalf("inject fake: %v", err)
	}
	err := ValidateOAuthModeConsistency(DefaultModePlans(), registry)
	if err == nil {
		t.Fatal("consistency gate must reject a fake exchanger registered on an OAuth mode")
	}
	if !strings.Contains(err.Error(), key) {
		t.Fatalf("gate error must name the offending mode key %q: %v", key, err)
	}
}

// TestValidateOAuthModeConsistencyRejectsMissingExchanger 守护:一个没有注册 exchanger 的
// FlowKindOAuth ModePlan 必须在 boot 期被抓住,而不是仅在真实回调时才静默暴露
// ErrOAuthExchangerMissing。
func TestValidateOAuthModeConsistencyRejectsMissingExchanger(t *testing.T) {
	plans := []ModePlan{{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth, Kind: FlowKindOAuth}}
	if err := ValidateOAuthModeConsistency(plans, NewExchangerRegistry()); err == nil {
		t.Fatal("consistency gate must reject an OAuth mode with no registered exchanger")
	}
}

func TestGeminiAntigravityUsesPinnedOAuthAndRejectsStorelessExchange(t *testing.T) {
	registry := DefaultExchangerRegistry()
	exc, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeAntigravity))
	if !ok {
		t.Fatal("default registry missing gemini/antigravity exchanger")
	}
	authCode, ok := exc.(authorizationCodeOAuthExchanger)
	if !ok {
		t.Fatalf("gemini/antigravity exchanger type=%T", exc)
	}
	if err := validateAntigravityPublicCLIConfig(authCode.config); err != nil {
		t.Fatalf("gemini/antigravity 未使用固定公开客户端：%v", err)
	}
	_, err := registry.Exchange(context.Background(), Session{
		TenantID: 1, ProviderAccountID: 7, Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeAntigravity, ActorID: "op",
	}, `{"session_token":"attacker-forged"}`)
	if !errors.Is(err, ErrOAuthRequiresCallback) {
		t.Fatalf("无 PKCE 会话的直接交换必须拒绝，got %v", err)
	}
}

func TestCopilotOAuthModeUsesDeviceCodeExchanger(t *testing.T) {
	registry := DefaultExchangerRegistry()
	exchanger, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorCopilot, credentialstore.AuthModeCopilotOAuth))
	if !ok {
		t.Fatal("默认注册表缺少 copilot/copilot_oauth")
	}
	if _, ok := exchanger.(copilotDeviceCodeExchanger); !ok {
		t.Fatalf("copilot/copilot_oauth exchanger=%T，期望设备码实现", exchanger)
	}
}

func TestValidateOAuthModeConsistencyRejectsDisabledExchanger(t *testing.T) {
	registry := DefaultExchangerRegistry()
	key := credentialstore.ModeKey(credentialstore.VendorCopilot, credentialstore.AuthModeCopilotOAuth)
	if err := registry.RegisterOrReplaceExchanger(key, newFailClosedExchanger("测试停用")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOAuthModeConsistency(DefaultModePlans(), registry); err == nil || !strings.Contains(err.Error(), key) {
		t.Fatalf("启动闸必须拒绝稳定失败的 OAuth 入口：%v", err)
	}
}

// TestNoFakeExchangerRemainsInDefaultRegistry 守护:生产环境默认 registry 中不得再有
// 任何可达的 pkceFakeExchanger(孤儿 cursor/windsurf fake 已移除,
// gemini/antigravity 已迁移为真实 PKCE 获取器)。变异:还原任意 NewPKCEFakeExchanger
// 注册,此扫描就会发现它 → 变红。
func TestNoFakeExchangerRemainsInDefaultRegistry(t *testing.T) {
	registry := DefaultExchangerRegistry()
	for _, name := range registry.Names() {
		exc, _ := registry.Lookup(name)
		if _, isFake := exc.(pkceFakeExchanger); isFake {
			t.Fatalf("default registry still exposes a fake exchanger at %q (orphaned/dangerous wiring)", name)
		}
	}
}
