package credentialacq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestDefaultExchangerRegistryIncludesAntigravityOAuthAlias(t *testing.T) {
	// 回归保护:Antigravity 获取必须能通过
	// vendor 原生的 antigravity/oauth 键到达,而不只是旧的
	// gemini/antigravity credentialstore mode。
	registry := DefaultExchangerRegistry()
	candidate, err := registry.Exchange(context.Background(), Session{
		TenantID: 1, ProviderAccountID: 42, Vendor: "antigravity", AuthMode: "oauth", ActorID: "owner",
	}, `{"session_token":"ag-session"}`)
	if err != nil {
		t.Fatalf("Exchange antigravity/oauth: %v", err)
	}
	if candidate.Vendor != "antigravity" || candidate.AuthMode != "oauth" {
		t.Fatalf("candidate mode=%s/%s, want antigravity/oauth", candidate.Vendor, candidate.AuthMode)
	}
	var payload map[string]string
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["session_token"] != "ag-session" {
		t.Fatalf("session_token=%q, want ag-session", payload["session_token"])
	}
}

func TestXAIOAuthExchangerRegistered(t *testing.T) {
	// 变异:去掉 grok/xai_oauth 的注册行,此 lookup 就必须变红。
	registry := DefaultExchangerRegistry()
	exc, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorGrok, credentialstore.AuthModeXAIOAuth))
	if !ok {
		t.Fatal("default registry missing grok/xai_oauth exchanger")
	}
	authCode, ok := exc.(authorizationCodeOAuthExchanger)
	if !ok {
		t.Fatalf("grok/xai_oauth exchanger type=%T, want authorizationCodeOAuthExchanger", exc)
	}
	if authCode.vendor != credentialstore.VendorGrok || authCode.authMode != credentialstore.AuthModeXAIOAuth {
		t.Fatalf("exchanger target=%s/%s, want grok/xai_oauth", authCode.vendor, authCode.authMode)
	}
	if authCode.shape != TokenShapeAccessRefresh {
		t.Fatalf("token shape=%s, want %s", authCode.shape, TokenShapeAccessRefresh)
	}
}

func TestXAIOAuthConfigEndpointsAndClient(t *testing.T) {
	// 变异:改动 xAI 的 client_id、scope、auth host、token host,或在 StartOAuthFlow 期间
	// 停止套用已注册的默认值,本测试就必须变红。
	const wantClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	const wantScope = "openid profile email offline_access grok-cli:access api:access"
	registry := DefaultExchangerRegistry()
	exc, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorGrok, credentialstore.AuthModeXAIOAuth))
	if !ok {
		t.Fatal("default registry missing grok/xai_oauth exchanger")
	}
	authCode, ok := exc.(authorizationCodeOAuthExchanger)
	if !ok {
		t.Fatalf("grok/xai_oauth exchanger type=%T, want authorizationCodeOAuthExchanger", exc)
	}
	cfg := authCode.config
	if cfg.ClientID != wantClientID {
		t.Fatalf("client_id=%q want %q", cfg.ClientID, wantClientID)
	}
	if got := strings.Join(cfg.Scopes, " "); got != wantScope {
		t.Fatalf("scope=%q want %q", got, wantScope)
	}
	if cfg.AuthURL != "https://auth.x.ai/oauth/authorize" {
		t.Fatalf("auth_url=%q", cfg.AuthURL)
	}
	if cfg.TokenURL != "https://auth.x.ai/oauth/token" {
		t.Fatalf("token_url=%q", cfg.TokenURL)
	}
	if cfg.Source != ClientSourceOperatorConfig {
		t.Fatalf("source=%q want %q", cfg.Source, ClientSourceOperatorConfig)
	}

	now := time.Date(2026, 6, 7, 8, 0, 0, 0, time.UTC)
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", []byte(strings.Repeat("x", 32)))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresSessionStoreWithKeys(newTestSessionDB(now), keys).WithNow(func() time.Time { return now })
	start, err := authCode.StartOAuthFlow(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 701,
		Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}, OAuthClientConfig{RedirectURI: "https://huakai.example.test/admin/v1/credentials/oauth-callback"})
	if err != nil {
		t.Fatalf("StartOAuthFlow with xAI registered defaults: %v", err)
	}
	authorize, err := url.Parse(start.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	if authorize.Scheme != "https" || authorize.Hostname() != "auth.x.ai" {
		t.Fatalf("authorize endpoint=%s, want https://auth.x.ai", start.AuthorizeURL)
	}
	query := authorize.Query()
	if query.Get("client_id") != wantClientID || query.Get("scope") != wantScope {
		t.Fatalf("authorize query client_id=%q scope=%q", query.Get("client_id"), query.Get("scope"))
	}
	if query.Get("redirect_uri") != "https://huakai.example.test/admin/v1/credentials/oauth-callback" {
		t.Fatalf("redirect_uri=%q", query.Get("redirect_uri"))
	}
}

func TestXAIOAuthSSRFHost(t *testing.T) {
	// 变异:为 grok/xai_oauth 放行任意非 x.ai 的 OAuth host,本
	// 测试就会因接受攻击者端点而变红。
	const wantClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	wantScopes := strings.Fields("openid profile email offline_access grok-cli:access api:access")
	base := OAuthClientConfig{
		Source: ClientSourceOperatorConfig, ClientID: wantClientID,
		AuthURL: "https://auth.x.ai/oauth/authorize", TokenURL: "https://auth.x.ai/oauth/token",
		RedirectURI: "https://huakai.example.test/admin/v1/credentials/oauth-callback",
		Scopes:      wantScopes,
	}
	if err := validateOperatorPKCEConfig(credentialstore.VendorGrok, credentialstore.AuthModeXAIOAuth, base); err != nil {
		t.Fatalf("valid xAI OAuth config rejected: %v", err)
	}
	badAuth := base
	badAuth.AuthURL = "https://auth.x.ai.attacker.example/oauth/authorize"
	if err := validateOperatorPKCEConfig(credentialstore.VendorGrok, credentialstore.AuthModeXAIOAuth, badAuth); !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled for non-x.ai auth host", err)
	}
	badToken := base
	badToken.TokenURL = "https://attacker.example/oauth/token"
	if err := validateOperatorPKCEConfig(credentialstore.VendorGrok, credentialstore.AuthModeXAIOAuth, badToken); !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled for non-x.ai token host", err)
	}
}

func TestAntigravityOAuthCallbackPostsAuthorizationCodeToConfiguredTokenEndpoint(t *testing.T) {
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
		shape: TokenShapeAnySessionOrAccess, client: client, now: func() time.Time { return now },
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
		AuthURL: "https://antigravity.example.test/authorize", TokenURL: "https://antigravity.example.test/token",
		ClientID: "ag-client", RedirectURI: "http://127.0.0.1:1455/auth/callback",
		Scopes: []string{"scope-a", "scope-b"}, Source: ClientSourceOperatorConfig,
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
		"client_id":     "ag-client",
		"redirect_uri":  "http://127.0.0.1:1455/auth/callback",
		"code_verifier": start.CodeVerifier,
	} {
		if got := gotForm.Get(key); got != want {
			t.Fatalf("form[%s]=%q want %q; full form=%v", key, got, want, gotForm)
		}
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
	payload := string(candidate.Payload)
	if !strings.Contains(payload, "gem-access") || !strings.Contains(payload, "gem-refresh") {
		t.Fatalf("payload=%s, want exchanged Google token material", payload)
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

// TestGeminiAntigravityAcquisitionFailsClosedNotFake 守护核心的信任边界修复:
// gemini/antigravity 获取绝不能再把 JSON-token 形状的回调码当作真实
// 凭据接受;它必须以 ErrFeatureDisabled fail-closed,与已暂停的 refresh 侧一致。
// 变异:为此 mode 还原 NewPKCEFakeExchanger,伪造的 blob 就会被接受(err==nil)——
// 本测试随之变红,证明它守护的是真实的伪造凭据接受,而非一个装样子的错误。
func TestGeminiAntigravityAcquisitionFailsClosedNotFake(t *testing.T) {
	registry := DefaultExchangerRegistry()
	_, err := registry.Exchange(context.Background(), Session{
		TenantID: 1, ProviderAccountID: 7, Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeAntigravity, ActorID: "op",
	}, `{"session_token":"attacker-forged"}`)
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("gemini/antigravity acquisition must fail-closed with ErrFeatureDisabled, got %v", err)
	}
}

// TestCopilotOAuthAcquisitionFailsClosed 守护:copilot/copilot_oauth 被宣告为一个
// OAuth mode,但其回调获取尚未实现;它必须以明确的
// ErrFeatureDisabled fail-closed,而非此前模糊的 ErrOAuthExchangerMissing。
func TestCopilotOAuthAcquisitionFailsClosed(t *testing.T) {
	registry := DefaultExchangerRegistry()
	_, err := registry.Exchange(context.Background(), Session{
		TenantID: 1, ProviderAccountID: 8, Vendor: credentialstore.VendorCopilot, AuthMode: credentialstore.AuthModeCopilotOAuth, ActorID: "op",
	}, `{"access_token":"x"}`)
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("copilot/copilot_oauth acquisition must fail-closed with ErrFeatureDisabled, got %v", err)
	}
}

// TestNoFakeExchangerRemainsInDefaultRegistry 守护:生产环境默认 registry 中不得再有
// 任何可达的 pkceFakeExchanger(孤儿 cursor/windsurf fake 已移除,
// gemini/antigravity 已迁移为 fail-closed)。变异:还原任意 NewPKCEFakeExchanger
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
