package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintakehttp"
)

func TestAdminCredentialAcquisitionRoutesIntegration(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminPoolAdmin())

	oauthFlow := fx.seedOAuthFlow(t, 101)
	pasteFlow := fx.seedPasteFlow(t, 101)
	cancelFlow := fx.seedPasteFlow(t, 101)
	finalizeFlow := fx.seedPasteFlow(t, 101)
	helperCallbackFlow := fx.seedOAuthFlow(t, 101)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{
			name: "canonical create", method: http.MethodPost, path: "/v1/admin/pool-accounts/101/credential-acquisitions",
			body: `{"tenant_id":1,"vendor":"openai","auth_mode":"api_key","flow_kind":"paste"}`, want: http.StatusCreated,
		},
		{name: "canonical status", method: http.MethodGet, path: "/v1/admin/pool-accounts/101/credential-acquisitions/" + pasteFlow.ID, want: http.StatusOK},
		{
			name: "canonical callback", method: http.MethodPost, path: "/v1/admin/pool-accounts/101/credential-acquisitions/" + oauthFlow.ID + "/callback",
			body: `{"state":"` + oauthFlow.State + `","code":"auth-code"}`, want: http.StatusOK,
		},
		{name: "canonical cancel", method: http.MethodPost, path: "/v1/admin/pool-accounts/101/credential-acquisitions/" + cancelFlow.ID + "/cancel", body: `{}`, want: http.StatusOK},
		{
			name: "canonical finalize", method: http.MethodPost, path: "/v1/admin/pool-accounts/101/credential-acquisitions/" + finalizeFlow.ID + "/finalize",
			body: `{"credentials":{"api_key":"sk-test-finalize"}}`, want: http.StatusOK,
		},
		{
			name: "helper paste", method: http.MethodPost, path: "/admin/v1/credentials/paste",
			body: `{"tenant_id":1,"provider_account_id":101,"vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-test-paste"}}`, want: http.StatusCreated,
		},
		{
			name: "helper cli import", method: http.MethodPost, path: "/admin/v1/credentials/cli-import",
			body: `{"tenant_id":1,"provider_account_id":101,"content":"{\"session_token\":\"session-cli\",\"refresh_token\":\"refresh-cli\"}"}`, want: http.StatusCreated,
		},
		{
			name: "helper csv import", method: http.MethodPost, path: "/admin/v1/credentials/csv-import",
			body: `{"tenant_id":1,"provider_account_id":101,"vendor":"openai","auth_mode":"api_key","content":"api_key,vendor,auth_mode\nsk-test-csv,openai,api_key\n"}`, want: http.StatusCreated,
		},
		{
			name: "helper json import", method: http.MethodPost, path: "/admin/v1/credentials/json-import",
			body: `{"tenant_id":1,"provider_account_id":101,"vendor":"openai","auth_mode":"api_key","content":"[{\"api_key\":\"sk-test-json\",\"vendor\":\"openai\",\"auth_mode\":\"api_key\"}]"}`, want: http.StatusCreated,
		},
		{
			name: "helper oauth init", method: http.MethodPost, path: "/admin/v1/credentials/oauth-init",
			body: `{"tenant_id":1,"provider_account_id":101,"vendor":"openai","auth_mode":"chatgpt_oauth","oauth_client":{"redirect_uri":"http://localhost:1455/auth/callback"}}`, want: http.StatusCreated,
		},
		{
			name: "helper oauth callback error path", method: http.MethodGet,
			path: "/admin/v1/credentials/oauth-callback?flow_id=" + helperCallbackFlow.ID + "&state=" + helperCallbackFlow.State + "&code=auth-code",
			want: http.StatusOK,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := fx.do(t, tc.method, tc.path, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want %d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestAdminCredentialAcquisitionCanonicalCallbackUsesRegistryAndFinalizesCredential(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminPoolAdmin())
	flow := fx.seedOAuthFlow(t, 101)

	rec := fx.do(t, http.MethodPost, "/v1/admin/pool-accounts/101/credential-acquisitions/"+flow.ID+"/callback",
		`{"state":"`+flow.State+`","code":"auth-code-without-credentials"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got := fx.exchanger.callCount(); got != 1 {
		t.Fatalf("exchanger calls=%d want 1", got)
	}
	created := fx.creator.inputsSnapshot()
	if len(created) != 1 {
		t.Fatalf("created credentials=%d want 1", len(created))
	}
	got := created[0]
	if got.TenantID != 1 || got.ProviderAccountID != 101 {
		t.Fatalf("created tenant/account=%d/%d want 1/101", got.TenantID, got.ProviderAccountID)
	}
	if got.Vendor != credentialstore.VendorOpenAI || got.AuthMode != credentialstore.AuthModeChatGPTOAuth {
		t.Fatalf("created mode=%s/%s want openai/chatgpt_oauth", got.Vendor, got.AuthMode)
	}
	var payload map[string]string
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("created payload is not JSON: %v", err)
	}
	if payload["session_token"] != "registry-session" || payload["refresh_token"] != "registry-refresh" {
		t.Fatalf("created payload came from wrong source: %s", string(got.Payload))
	}
	session, err := fx.store.Get(context.Background(), flow.ID)
	if err != nil {
		t.Fatalf("get finalized flow: %v", err)
	}
	if session.Status != credentialacq.StatusFinalized || session.ResultAccountCredentialID == 0 {
		t.Fatalf("flow status=%s credential_id=%d want finalized with credential", session.Status, session.ResultAccountCredentialID)
	}
}

// 缺陷：浏览器 OAuth 回跳没有 Bearer，helper callback 若继续解析 admin token 会永远 401。
// 判别变异：恢复 callback handler 的 Bearer 闸时，本测试必须因 401 变红。
func TestOAuthBrowserCallbackCompletesWithoutBearer(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminPoolAuthStub{err: admin.ErrAdminUnauthorized})
	flow := fx.seedOAuthFlowWithActor(t, 101, "4242", admin.RolePlatformAdmin)

	rec := fx.do(t, http.MethodGet, oauthBrowserCallbackPath(flow.ID, flow.State, "browser-auth-code"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got := fx.exchanger.callCount(); got != 1 {
		t.Fatalf("exchanger calls=%d want 1", got)
	}
	created := fx.creator.inputsSnapshot()
	if len(created) != 1 {
		t.Fatalf("created credentials=%d want 1", len(created))
	}
	if created[0].ProviderAccountID != 101 || created[0].ActorID != "4242" {
		t.Fatalf("created credential account/actor=%d/%q want 101/4242", created[0].ProviderAccountID, created[0].ActorID)
	}
}

// 缺陷：browser callback 若绕过 CompleteOAuthCallback，就会跳过 state CSRF 校验。
// 判别变异：把 handler 改成直接 finalize 时，错误 state 会返回 200，本测试必须变红。
func TestOAuthBrowserCallbackRejectsStateMismatch(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminPoolAuthStub{err: admin.ErrAdminUnauthorized})
	flow := fx.seedOAuthFlowWithActor(t, 101, "4242", admin.RolePlatformAdmin)

	rec := fx.do(t, http.MethodGet, oauthBrowserCallbackPath(flow.ID, "wrong-state", "browser-auth-code"), "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "oauth_state_mismatch") {
		t.Fatalf("body=%s want oauth_state_mismatch", rec.Body.String())
	}
	if created := fx.creator.inputsSnapshot(); len(created) != 0 {
		t.Fatalf("created credentials=%d want 0 on state mismatch", len(created))
	}
}

// 缺陷：browser callback 若不复用 consumed/finalized 闸，同一 code 回跳可重放创建凭据。
// 判别变异：删除 CompleteOAuthCallback 的 consumed/finalized 闸时，第二次 GET 会触发第 2 次 exchange，callCount=2，本测试必须变红。
func TestOAuthBrowserCallbackRejectsReplay(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminPoolAuthStub{err: admin.ErrAdminUnauthorized})
	flow := fx.seedOAuthFlowWithActor(t, 101, "4242", admin.RolePlatformAdmin)
	path := oauthBrowserCallbackPath(flow.ID, flow.State, "browser-auth-code")

	first := fx.do(t, http.MethodGet, path, "")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d want 200 body=%s", first.Code, first.Body.String())
	}
	second := fx.do(t, http.MethodGet, path, "")
	if second.Code != http.StatusConflict {
		t.Fatalf("second status=%d want 409 body=%s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "credential_acquisition_replay") {
		t.Fatalf("second body=%s want credential_acquisition_replay", second.Body.String())
	}
	if got := fx.exchanger.callCount(); got != 1 {
		t.Fatalf("exchanger calls=%d want 1 (replay must be rejected before a second token exchange)", got)
	}
	if created := fx.creator.inputsSnapshot(); len(created) != 1 {
		t.Fatalf("created credentials=%d want exactly 1 after replay", len(created))
	}
}

// 缺陷：无 Bearer browser callback 的审计 actor 若取当前请求身份，会丢失发起 admin。
// 判别变异：把 actor 改成空字符串或固定值时，admin audit actor 断言必须变红。
func TestOAuthBrowserCallbackAuditsStartingAdmin(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminPoolAuthStub{err: admin.ErrAdminUnauthorized})
	flow := fx.seedOAuthFlowWithActor(t, 101, "4242", admin.RolePlatformAdmin)

	rec := fx.do(t, http.MethodGet, oauthBrowserCallbackPath(flow.ID, flow.State, "browser-auth-code"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if len(fx.adminAudit.audits) != 1 {
		t.Fatalf("admin audits=%d want 1", len(fx.adminAudit.audits))
	}
	audit := fx.adminAudit.audits[0]
	if audit.ActorID != "4242" || audit.ActorRole != admin.RolePlatformAdmin {
		t.Fatalf("admin audit actor=%q/%q want 4242/platform_admin", audit.ActorID, audit.ActorRole)
	}
	if audit.Action != credentialacq.EventCompleted {
		t.Fatalf("admin audit action=%q want %q", audit.Action, credentialacq.EventCompleted)
	}
}

func TestAdminCredentialAcquisitionCallbackMissingRegistryEntryReturns422AndAudits(t *testing.T) {
	registry := credentialacq.NewExchangerRegistry()
	openAI := &credentialAcqExchangerStub{}
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth), openAI); err != nil {
		t.Fatal(err)
	}
	fx := newCredentialAcqHTTPFixtureWithRegistry(t, adminPoolAdmin(), registry, openAI)
	flow := fx.seedRawOAuthFlow(t, 101, "cursor", "oauth")

	rec := fx.do(t, http.MethodPost, "/v1/admin/pool-accounts/101/credential-acquisitions/"+flow.ID+"/callback",
		`{"state":"`+flow.State+`","code":"cursor-auth-code"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d want 422 body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "not configured") {
		t.Fatalf("missing registry path must not use legacy not-configured fallback: %s", rec.Body.String())
	}
	if got := openAI.callCount(); got != 0 {
		t.Fatalf("openai exchanger calls=%d want 0 for cursor flow", got)
	}
	if created := fx.creator.inputsSnapshot(); len(created) != 0 {
		t.Fatalf("created credentials=%d want 0 on missing exchanger", len(created))
	}
	events := fx.audit.eventsSnapshot()
	if len(events) != 1 {
		t.Fatalf("audit events=%d want 1", len(events))
	}
	if events[0].EventType != credentialacq.EventFailed {
		t.Fatalf("event type=%s want %s", events[0].EventType, credentialacq.EventFailed)
	}
	if events[0].Payload["error_class"] != "callback_failed" {
		t.Fatalf("event payload=%v want callback_failed", events[0].Payload)
	}
	session, err := fx.store.Get(context.Background(), flow.ID)
	if err != nil {
		t.Fatalf("get failed flow: %v", err)
	}
	if session.Status != credentialacq.StatusFailed || session.ErrorClass != "exchange_failed" {
		t.Fatalf("flow status=%s error_class=%s want failed/exchange_failed", session.Status, session.ErrorClass)
	}
}

func TestAdminClaudeAIOAuthStartFailsClosedWithBuiltinProfileMissing(t *testing.T) {
	fx := newCredentialAcqHTTPFixtureWithDefaultExchangers(t, adminPoolAdmin())
	tokenCalls := 0
	restore := withAdminClaudeAIOAuthDefaultTransport(t, adminCredentialAcqRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		tokenCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"AT","refresh_token":"RT"}`)),
		}, nil
	}))
	defer restore()

	rec := fx.do(t, http.MethodPost, "/admin/v1/credentials/oauth-init",
		`{"tenant_id":1,"provider_account_id":101,"vendor":"anthropic","auth_mode":"claude_ai_oauth","oauth_client":{"token_url":"http://attacker.test/token"}}`)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("status=%d want 4xx for admin real entry built-in profile rejection body=%s", rec.Code, rec.Body.String())
	}
	fx.db.mu.Lock()
	flowCount := len(fx.db.rows)
	fx.db.mu.Unlock()
	if flowCount != 0 {
		t.Fatalf("stored flows=%d want 0 for rejected admin real entry", flowCount)
	}
	if tokenCalls != 0 {
		t.Fatalf("token endpoint calls=%d want 0 before any flow is created", tokenCalls)
	}
}

func TestAdminClaudeAIOAuthFullFlowEncryptsAndSavesCredential(t *testing.T) {
	fx := newCredentialAcqHTTPFixtureWithDefaultExchangers(t, adminPoolAdmin())
	tokenCalls := 0
	restore := withAdminClaudeAIOAuthDefaultTransport(t, adminCredentialAcqRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		tokenCalls++
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode token request JSON: %v", err)
		}
		if body["code"] != "admin-real-code" || body["code_verifier"] == "" {
			t.Fatalf("token request body=%v want callback code and stored PKCE verifier", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"AT-admin","refresh_token":"RT-admin","expires_in":3600,"token_type":"Bearer"}`)),
		}, nil
	}))
	defer restore()

	// claude_ai_oauth redirect_uri 现严格校验(loopback / 静态 allowlist 的 HTTPS admin)。
	// 本全流程测试与 redirect 校验无关,用合法 built-in loopback 即可(回调由测试直接打服务端
	// /admin/v1/credentials/oauth-callback 端点驱动,与 OAuth redirect_uri 取值无关)。
	startRec := fx.do(t, http.MethodPost, "/admin/v1/credentials/oauth-init",
		`{"tenant_id":1,"provider_account_id":101,"vendor":"anthropic","auth_mode":"claude_ai_oauth","redirect_uri":"http://localhost:54545/callback"}`)
	if startRec.Code != http.StatusCreated {
		t.Fatalf("start status=%d want 201 body=%s", startRec.Code, startRec.Body.String())
	}
	flowID, state, authorizeURL := decodeAdminClaudeAIOAuthStart(t, startRec.Body.Bytes())
	if authorizeURL == "" {
		t.Fatalf("authorize_url empty for admin real entry start body=%s", startRec.Body.String())
	}

	callbackRec := fx.do(t, http.MethodGet, "/admin/v1/credentials/oauth-callback?flow_id="+url.QueryEscape(flowID)+"&state="+url.QueryEscape(state)+"&code=admin-real-code", "")
	if callbackRec.Code != http.StatusOK {
		t.Fatalf("callback status=%d want 200 body=%s", callbackRec.Code, callbackRec.Body.String())
	}
	if tokenCalls != 1 {
		t.Fatalf("token endpoint calls=%d want 1", tokenCalls)
	}
	created := fx.creator.inputsSnapshot()
	if len(created) != 1 {
		t.Fatalf("created credentials=%d want 1", len(created))
	}
	got := created[0]
	if got.Vendor != credentialstore.VendorAnthropic || got.AuthMode != credentialstore.AuthModeClaudeAIOAuth {
		t.Fatalf("created mode=%s/%s want anthropic/claude_ai_oauth", got.Vendor, got.AuthMode)
	}
	handler, err := credentialstore.DefaultHandlerRegistry().MustLookup(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth)
	if err != nil {
		t.Fatalf("lookup handler: %v", err)
	}
	material, err := handler.RuntimeMaterial(got.Payload)
	if err != nil {
		t.Fatalf("RuntimeMaterial: %v payload=%s", err, string(got.Payload))
	}
	if material.Kind != credentialstore.RuntimeOAuthAccessToken || material.Value != "AT-admin" {
		t.Fatalf("runtime material=%s/%q want oauth_access_token/AT-admin", material.Kind, material.Value)
	}
	var payload map[string]any
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload["refresh_token"] != "RT-admin" || payload["client_identity_source"] != "approved_builtin_profile" {
		t.Fatalf("payload=%v want refresh token and approved built-in profile marker", payload)
	}
}

func TestAdminGeminiCodeAssistFullFlowUsesEnvClientSecret(t *testing.T) {
	// 缺陷：Gemini D-1=A 内置 profile 若继续信任 request client_secret，会绕过
	// 让 exchanger 或 handler
	// 使用 request secret 时，本测试必须变红。
	tokenCalls := 0
	client := &http.Client{Transport: adminCredentialAcqRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		tokenCalls++
		if r.URL.String() != "https://oauth2.googleapis.com/token" {
			t.Fatalf("token URL=%s want Google token endpoint", r.URL.String())
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if r.PostForm.Get("client_secret") != "from-env" || r.PostForm.Get("code_verifier") == "" {
			t.Fatalf("token form=%v want env secret and stored PKCE verifier", r.PostForm)
		}
		if r.PostForm.Get("client_secret") == "from-request" {
			t.Fatalf("request client_secret leaked into token form: %v", r.PostForm)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"AT-gemini-admin","refresh_token":"RT-gemini-admin","expires_in":3600}`)),
		}, nil
	})}
	registry := credentialacq.NewExchangerRegistry()
	exchanger := credentialacq.NewGeminiPublicCLIOAuthExchangerWithClientSecretAndAdminCallbackAllowlist(
		credentialstore.AuthModeCodeAssist,
		client,
		"from-env",
		[]string{"https://huakai.example/admin/v1/credentials/oauth-callback"},
	)
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist), exchanger); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}
	fx := newCredentialAcqHTTPFixtureWithRegistry(t, adminPoolAdmin(), registry, nil)

	startRec := fx.do(t, http.MethodPost, "/admin/v1/credentials/oauth-init",
		`{"tenant_id":1,"provider_account_id":101,"vendor":"gemini","auth_mode":"code_assist","oauth_client":{"client_secret":"from-request","redirect_uri":"https://huakai.example/admin/v1/credentials/oauth-callback"}}`)
	if startRec.Code != http.StatusCreated {
		t.Fatalf("start status=%d want 201 body=%s", startRec.Code, startRec.Body.String())
	}
	flowID, state, authorizeURL := decodeAdminClaudeAIOAuthStart(t, startRec.Body.Bytes())
	if authorizeURL == "" || !strings.Contains(authorizeURL, "accounts.google.com") {
		t.Fatalf("authorize_url=%q want Google authorize URL", authorizeURL)
	}

	callbackRec := fx.do(t, http.MethodGet, "/admin/v1/credentials/oauth-callback?flow_id="+url.QueryEscape(flowID)+"&state="+url.QueryEscape(state)+"&code=admin-gemini-code", "")
	if callbackRec.Code != http.StatusOK {
		t.Fatalf("callback status=%d want 200 body=%s", callbackRec.Code, callbackRec.Body.String())
	}
	if tokenCalls != 1 {
		t.Fatalf("token endpoint calls=%d want 1", tokenCalls)
	}
	created := fx.creator.inputsSnapshot()
	if len(created) != 1 {
		t.Fatalf("created credentials=%d want 1", len(created))
	}
	got := created[0]
	if got.Vendor != credentialstore.VendorGemini || got.AuthMode != credentialstore.AuthModeCodeAssist {
		t.Fatalf("created mode=%s/%s want gemini/code_assist", got.Vendor, got.AuthMode)
	}
	var payload map[string]any
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload["access_token"] != "AT-gemini-admin" || payload["refresh_token"] != "RT-gemini-admin" {
		t.Fatalf("payload=%v want exchanged Gemini token material", payload)
	}
	if strings.Contains(string(got.Payload), "from-env") || strings.Contains(string(got.Payload), "from-request") {
		t.Fatalf("payload leaked client secret: %s", got.Payload)
	}
}

func TestGeminiAdminStartFlowIgnoresClientSecretFromRequest(t *testing.T) {
	// 缺陷：admin API request body 的 client_secret 若继续传入 Gemini start config，
	// 就绕过了
	// OAuthClientConfig.ClientSecret: oauthReq.ClientSecret 时，本测试必须变红。
	guard := &geminiAdminStartConfigGuardExchanger{}
	registry := credentialacq.NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist), guard); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}
	fx := newCredentialAcqHTTPFixtureWithRegistry(t, adminPoolAdmin(), registry, nil)

	rec := fx.do(t, http.MethodPost, "/admin/v1/credentials/oauth-init",
		`{"tenant_id":1,"provider_account_id":101,"vendor":"gemini","auth_mode":"code_assist","oauth_client":{"client_id":"ignored-by-real-gemini","client_secret":"from-request","auth_url":"https://accounts.google.com/o/oauth2/v2/auth","token_url":"https://oauth2.googleapis.com/token","redirect_uri":"http://localhost:8085/oauth2callback","scopes":["https://www.googleapis.com/auth/cloud-platform"],"source":"approved_builtin_profile_gemini_public_cli"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start status=%d want 201 body=%s", rec.Code, rec.Body.String())
	}
	if guard.clientSecret != "" {
		t.Fatalf("Gemini start cfg ClientSecret=%q want empty; request body secret must be ignored", guard.clientSecret)
	}
	if guard.calls != 1 {
		t.Fatalf("guard calls=%d want 1", guard.calls)
	}
}

func TestAdminChatGPTOAuthStartFlowIgnoresClientSecretFromRequest(t *testing.T) {
	// 缺陷：ChatGPT OAuth 是 PKCE-only；admin request body 的 client_secret 若进入 StartOAuthFlow，
	// 会被内置 profile 拒绝或诱导后续路径发送 confidential-client secret。
	// 判别变异：只对 Gemini 清空 client_secret 时，本测试必须变红。
	guard := &chatGPTAdminStartConfigGuardExchanger{}
	registry := credentialacq.NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth), guard); err != nil {
		t.Fatalf("RegisterExchanger: %v", err)
	}
	fx := newCredentialAcqHTTPFixtureWithRegistry(t, adminPoolAdmin(), registry, nil)

	rec := fx.do(t, http.MethodPost, "/admin/v1/credentials/oauth-init",
		`{"tenant_id":1,"provider_account_id":101,"vendor":"openai","auth_mode":"chatgpt_oauth","oauth_client":{"client_secret":"from-request","redirect_uri":"http://localhost:1455/auth/callback"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start status=%d want 201 body=%s", rec.Code, rec.Body.String())
	}
	if guard.clientSecret != "" {
		t.Fatalf("ChatGPT start cfg ClientSecret=%q want empty; request body secret must be ignored", guard.clientSecret)
	}
	if guard.calls != 1 {
		t.Fatalf("guard calls=%d want 1", guard.calls)
	}
}

func TestAdminCredentialAcquisitionOAuthStartSelectsBootstrapTTL(t *testing.T) {
	shortTTL := 30 * time.Minute
	longTTL := 48 * time.Hour
	cases := []struct {
		name      string
		longLived bool
		wantTTL   time.Duration
	}{
		{name: "short requested false", longLived: false, wantTTL: shortTTL},
		{name: "long requested true", longLived: true, wantTTL: longTTL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newCredentialAcqHTTPFixtureWithBootstrapTTLs(t, adminPoolAdmin(), true, shortTTL, longTTL)
			before := time.Now().UTC()
			rec := fx.do(t, http.MethodPost, "/v1/admin/pool-accounts/101/credential-acquisitions",
				`{"tenant_id":1,"vendor":"openai","auth_mode":"chatgpt_oauth","flow_kind":"oauth","long_lived_requested":`+strconv.FormatBool(tc.longLived)+`,"oauth_client":{"client_id":"client-id","auth_url":"https://auth.example.test/oauth","redirect_uri":"https://huakai.example.test/callback"}}`)
			after := time.Now().UTC()
			if rec.Code != http.StatusCreated {
				t.Fatalf("status=%d want 201 body=%s", rec.Code, rec.Body.String())
			}
			var body struct {
				Flow credentialacq.Session `json:"flow"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode start response: %v body=%s", err, rec.Body.String())
			}
			if body.Flow.LongLivedRequested != tc.longLived {
				t.Fatalf("long_lived_requested=%v want %v", body.Flow.LongLivedRequested, tc.longLived)
			}
			earliest := before.Add(tc.wantTTL)
			latest := after.Add(tc.wantTTL)
			if body.Flow.ExpiresAt.Before(earliest) || body.Flow.ExpiresAt.After(latest) {
				t.Fatalf("expires_at=%s want between %s and %s", body.Flow.ExpiresAt, earliest, latest)
			}
			if tc.longLived && !body.Flow.ExpiresAt.After(after.Add(credentialacq.DefaultFlowTTL)) {
				t.Fatalf("long-lived expires_at=%s still within fallback %s", body.Flow.ExpiresAt, credentialacq.DefaultFlowTTL)
			}
		})
	}
}

func TestAdminCredentialAcquisitionRejectsTenantScopedSetupTokenMode(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminPoolAdmin())
	before := len(fx.db.rows)
	tests := []struct {
		name string
		body string
	}{
		{
			name: "按模式绕过",
			body: `{"tenant_id":1,"vendor":"anthropic","auth_mode":"claude_setup_token","flow_kind":"json_import"}`,
		},
		{
			name: "按流程绕过",
			body: `{"tenant_id":1,"vendor":"anthropic","auth_mode":"claude_code","flow_kind":"setup_token"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := fx.do(t, http.MethodPost, "/v1/admin/pool-accounts/101/credential-acquisitions", tc.body)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "credential_acquisition_feature_disabled") {
				t.Fatalf("status=%d body=%s，期望专属租户接入模式被平台入口拒绝", rec.Code, rec.Body.String())
			}
			if len(fx.db.rows) != before {
				t.Fatalf("拒绝请求后 flow 数=%d，期望 %d", len(fx.db.rows), before)
			}
		})
	}
}

func TestAdminClaudeAIOAuthRejectsFakeJSONCallback(t *testing.T) {
	fx := newCredentialAcqHTTPFixtureWithDefaultExchangers(t, adminPoolAdmin())
	fakeCode := `{"access_token":"FAKE"}`
	tokenCalls := 0
	restore := withAdminClaudeAIOAuthDefaultTransport(t, adminCredentialAcqRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		tokenCalls++
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode token request JSON: %v", err)
		}
		if body["code"] != fakeCode {
			t.Fatalf("token request code=%q want raw fake-shaped callback code", body["code"])
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"AT-real","refresh_token":"RT-real","expires_in":3600}`)),
		}, nil
	}))
	defer restore()

	startRec := fx.do(t, http.MethodPost, "/admin/v1/credentials/oauth-init",
		`{"tenant_id":1,"provider_account_id":101,"vendor":"anthropic","auth_mode":"claude_ai_oauth"}`)
	if startRec.Code != http.StatusCreated {
		t.Fatalf("start status=%d want 201 body=%s", startRec.Code, startRec.Body.String())
	}
	flowID, state, _ := decodeAdminClaudeAIOAuthStart(t, startRec.Body.Bytes())
	callbackRec := fx.do(t, http.MethodGet, "/admin/v1/credentials/oauth-callback?flow_id="+url.QueryEscape(flowID)+"&state="+url.QueryEscape(state)+"&code="+url.QueryEscape(fakeCode), "")
	if callbackRec.Code != http.StatusOK {
		t.Fatalf("callback status=%d want 200 body=%s", callbackRec.Code, callbackRec.Body.String())
	}
	if tokenCalls != 1 {
		t.Fatalf("token endpoint calls=%d want 1; fake JSON callback must not bypass exchange", tokenCalls)
	}
	created := fx.creator.inputsSnapshot()
	if len(created) != 1 {
		t.Fatalf("created credentials=%d want 1", len(created))
	}
	if strings.Contains(string(created[0].Payload), "FAKE") {
		t.Fatalf("created payload accepted fake callback JSON: %s", string(created[0].Payload))
	}
	if !strings.Contains(string(created[0].Payload), "AT-real") {
		t.Fatalf("created payload=%s want token endpoint response", string(created[0].Payload))
	}
}

func TestAdminCredentialAcquisitionRequiresAdminAuth(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminPoolAuthStub{err: admin.ErrAdminUnauthorized})
	rec := fx.do(t, http.MethodPost, "/v1/admin/pool-accounts/101/credential-acquisitions",
		`{"tenant_id":1,"vendor":"openai","auth_mode":"api_key","flow_kind":"paste"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}
}

type credentialAcqProjectResolverStub struct {
	projectRef string
	err        error
	calls      int
	token      string
}

func (s *credentialAcqProjectResolverStub) ResolveProjectID(_ context.Context, token string) (string, error) {
	s.calls++
	s.token = token
	return s.projectRef, s.err
}

func TestAdminCredentialAcquisitionFinalizeEnrichesAntigravityProject(t *testing.T) {
	resolver := &credentialAcqProjectResolverStub{projectRef: "project-from-finalize"}
	fx := newCredentialAcqHTTPFixtureWithProjectEnricher(t, adminPoolAdmin(), projectenrich.New(resolver))
	flow := fx.seedPasteFlowFor(t, 101, credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth)

	rec := fx.do(t, http.MethodPost, "/v1/admin/pool-accounts/101/credential-acquisitions/"+flow.ID+"/finalize",
		`{"credentials":{"access_token":"access-finalize","refresh_token":"refresh-finalize"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d，期望 200，body=%s", rec.Code, rec.Body.String())
	}
	created := fx.creator.inputsSnapshot()
	if len(created) != 1 {
		t.Fatalf("创建凭证数=%d，期望 1", len(created))
	}
	var payload map[string]string
	if err := json.Unmarshal(created[0].Payload, &payload); err != nil {
		t.Fatalf("解析创建载荷失败：%v", err)
	}
	if resolver.calls != 1 || resolver.token != "access-finalize" {
		t.Fatalf("resolver 调用不符：calls=%d token=%q", resolver.calls, resolver.token)
	}
	if payload["project_id"] != "project-from-finalize" || payload["project_metadata_status"] != projectenrich.StatusResolved {
		t.Fatalf("finalize 未补齐 project：%s", created[0].Payload)
	}
	events := fx.audit.eventsSnapshot()
	if len(events) != 1 || events[0].EventType != credentialacq.EventCompleted || strings.TrimSpace(events[0].RequestID) == "" {
		t.Fatalf("finalize 审计缺少完成事件或 correlation：%+v", events)
	}
}

func TestAdminCredentialAcquisitionFinalizeKeepsCredentialWhenProjectResolutionFails(t *testing.T) {
	resolver := &credentialAcqProjectResolverStub{err: errors.New("上游 project 暂不可用")}
	fx := newCredentialAcqHTTPFixtureWithProjectEnricher(t, adminPoolAdmin(), projectenrich.New(resolver))
	flow := fx.seedPasteFlowFor(t, 101, credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth)

	rec := fx.do(t, http.MethodPost, "/v1/admin/pool-accounts/101/credential-acquisitions/"+flow.ID+"/finalize",
		`{"credentials":{"access_token":"access-finalize","refresh_token":"refresh-finalize"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("project 解析失败不得阻断创建：status=%d body=%s", rec.Code, rec.Body.String())
	}
	created := fx.creator.inputsSnapshot()
	if len(created) != 1 {
		t.Fatalf("创建凭证数=%d，期望 1", len(created))
	}
	var payload map[string]string
	if err := json.Unmarshal(created[0].Payload, &payload); err != nil {
		t.Fatalf("解析待处理载荷失败：%v", err)
	}
	if resolver.calls != 1 || payload["project_id"] != "" || payload["project_metadata_status"] != projectenrich.StatusOperatorAttention {
		t.Fatalf("解析失败未保留待处理凭证：resolver=%+v payload=%s", resolver, created[0].Payload)
	}
}

func TestAdminCredentialAcquisitionRejectsPathAccountMismatch(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminPoolAdmin())
	flow := fx.seedPasteFlow(t, 202)
	rec := fx.do(t, http.MethodGet, "/v1/admin/pool-accounts/101/credential-acquisitions/"+flow.ID, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
}

type credentialAcqHTTPFixture struct {
	handler    http.Handler
	store      *credentialacq.PostgresSessionStore
	db         *credentialAcqSessionDB
	creator    *credentialAcqCreatorStub
	audit      *credentialAcqAuditStub
	adminAudit *adminPoolStoreStub
	exchanger  *credentialAcqExchangerStub
}

type seededCredentialAcqFlow struct {
	ID    string
	State string
}

func newCredentialAcqHTTPFixture(t *testing.T, auth AdminCredentialAuth) *credentialAcqHTTPFixture {
	t.Helper()
	exchanger := &credentialAcqExchangerStub{
		payload: []byte(`{"session_token":"registry-session","refresh_token":"registry-refresh"}`),
	}
	registry := credentialacq.NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth), exchanger); err != nil {
		t.Fatal(err)
	}
	return newCredentialAcqHTTPFixtureWithRegistry(t, auth, registry, exchanger)
}

func newCredentialAcqHTTPFixtureWithDefaultExchangers(t *testing.T, auth AdminCredentialAuth) *credentialAcqHTTPFixture {
	t.Helper()
	return newCredentialAcqHTTPFixtureWithRegistry(t, auth, credentialacq.DefaultExchangerRegistry(), nil)
}

func newCredentialAcqHTTPFixtureWithRegistry(t *testing.T, auth AdminCredentialAuth, registry *credentialacq.ExchangerRegistry, exchanger *credentialAcqExchangerStub) *credentialAcqHTTPFixture {
	return newCredentialAcqHTTPFixtureWithRegistryAndLongLived(t, auth, registry, exchanger, false)
}

func newCredentialAcqHTTPFixtureWithProjectEnricher(t *testing.T, auth AdminCredentialAuth, enricher projectenrich.Enricher) *credentialAcqHTTPFixture {
	t.Helper()
	return newCredentialAcqHTTPFixtureWithRegistryAndBootstrapTTLs(t, auth, credentialacq.NewExchangerRegistry(), nil, false, 0, 0, enricher)
}

func newCredentialAcqHTTPFixtureWithBootstrapTTLs(t *testing.T, auth AdminCredentialAuth, allow bool, shortTTL, longTTL time.Duration) *credentialAcqHTTPFixture {
	t.Helper()
	exchanger := &credentialAcqExchangerStub{
		payload: []byte(`{"session_token":"registry-session","refresh_token":"registry-refresh"}`),
	}
	registry := credentialacq.NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth), exchanger); err != nil {
		t.Fatal(err)
	}
	return newCredentialAcqHTTPFixtureWithRegistryAndBootstrapTTLs(t, auth, registry, exchanger, allow, shortTTL, longTTL)
}

func newCredentialAcqHTTPFixtureWithRegistryAndLongLived(t *testing.T, auth AdminCredentialAuth, registry *credentialacq.ExchangerRegistry, exchanger *credentialAcqExchangerStub, allow bool) *credentialAcqHTTPFixture {
	return newCredentialAcqHTTPFixtureWithRegistryAndBootstrapTTLs(t, auth, registry, exchanger, allow, 0, 0)
}

func newCredentialAcqHTTPFixtureWithRegistryAndBootstrapTTLs(t *testing.T, auth AdminCredentialAuth, registry *credentialacq.ExchangerRegistry, exchanger *credentialAcqExchangerStub, allow bool, shortTTL, longTTL time.Duration, projectEnrichers ...projectenrich.Enricher) *credentialAcqHTTPFixture {
	t.Helper()
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db := newCredentialAcqSessionDB(now)
	store := credentialacq.NewPostgresSessionStoreWithKeys(db, keys).WithNow(func() time.Time { return now })
	creator := &credentialAcqCreatorStub{}
	audit := &credentialAcqAuditStub{}
	adminAudit := &adminPoolStoreStub{}
	var projectEnricher projectenrich.Enricher
	if len(projectEnrichers) > 0 {
		projectEnricher = projectEnrichers[0]
	}
	deps := AdminCredentialAcquisitionDeps{
		Auth: auth, Sessions: store,
		Credentials:              creator,
		CredentialAudit:          audit,
		AuditStore:               adminAudit,
		Exchangers:               registry,
		ProjectEnricher:          projectEnricher,
		AllowLongLivedSetupToken: allow,
		BootstrapShortTTL:        shortTTL,
		BootstrapLongTTL:         longTTL,
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Route("/v1/admin/pool-accounts", func(r chi.Router) {
		MountAdminCredentialAcquisitionRoutes(r, deps)
	})
	r.Route("/admin/v1/credentials", func(r chi.Router) {
		MountAdminCredentialAcquisitionHelperRoutes(r, deps)
		accountintakehttp.Mount(r, accountintakehttp.Deps{Auth: auth})
	})
	return &credentialAcqHTTPFixture{handler: r, store: store, db: db, creator: creator, audit: audit, adminAudit: adminAudit, exchanger: exchanger}
}

func decodeAdminClaudeAIOAuthStart(t *testing.T, raw []byte) (string, string, string) {
	t.Helper()
	var body struct {
		Flow         credentialacq.Session `json:"flow"`
		State        string                `json:"state"`
		AuthorizeURL string                `json:"authorize_url"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode start response: %v body=%s", err, string(raw))
	}
	if body.Flow.ID == "" || body.State == "" {
		t.Fatalf("start response missing flow/state: %s", string(raw))
	}
	return body.Flow.ID, body.State, body.AuthorizeURL
}

func withAdminClaudeAIOAuthDefaultTransport(t *testing.T, rt http.RoundTripper) func() {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = rt
	return func() { http.DefaultTransport = old }
}

type adminCredentialAcqRoundTripFunc func(*http.Request) (*http.Response, error)

func (f adminCredentialAcqRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func (fx *credentialAcqHTTPFixture) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	fx.handler.ServeHTTP(rec, req)
	return rec
}

func (fx *credentialAcqHTTPFixture) seedPasteFlow(t *testing.T, providerAccountID int64) seededCredentialAcqFlow {
	t.Helper()
	return fx.seedPasteFlowFor(t, providerAccountID, credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey)
}

func (fx *credentialAcqHTTPFixture) seedPasteFlowFor(t *testing.T, providerAccountID int64, vendor, authMode string) seededCredentialAcqFlow {
	t.Helper()
	kind := credentialacq.FlowKindPaste
	if credentialstore.Normalize(vendor) == credentialstore.VendorAntigravity {
		kind = credentialacq.FlowKindTokenExchange
	}
	session, err := fx.store.CreateFromStart(context.Background(), credentialacq.StartInput{
		TenantID: 1, ProviderAccountID: providerAccountID,
		Vendor: vendor, AuthMode: authMode, Kind: kind,
		ActorID: "11", ActorRole: "platform_admin",
		ClientIdentitySource: credentialacq.ClientSourceNone,
	})
	if err != nil {
		t.Fatalf("seed paste flow: %v", err)
	}
	return seededCredentialAcqFlow{ID: session.ID}
}

func (fx *credentialAcqHTTPFixture) seedOAuthFlow(t *testing.T, providerAccountID int64) seededCredentialAcqFlow {
	t.Helper()
	return fx.seedRawOAuthFlow(t, providerAccountID, credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth)
}

func (fx *credentialAcqHTTPFixture) seedOAuthFlowWithActor(t *testing.T, providerAccountID int64, actorID, actorRole string) seededCredentialAcqFlow {
	t.Helper()
	return fx.seedRawOAuthFlowWithActor(t, providerAccountID, credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth, actorID, actorRole)
}

func (fx *credentialAcqHTTPFixture) seedOAuthFlowFor(t *testing.T, providerAccountID int64, vendor, authMode string) seededCredentialAcqFlow {
	t.Helper()
	result, err := credentialacq.StartOAuthFlow(context.Background(), fx.store, credentialacq.StartInput{
		TenantID: 1, ProviderAccountID: providerAccountID,
		Vendor: vendor, AuthMode: authMode,
		ActorID: "11", ActorRole: "platform_admin",
	}, credentialacq.OAuthClientConfig{
		ClientID: "client-id", AuthURL: "https://auth.example.test/oauth", RedirectURI: "https://huakai.example.test/callback",
	})
	if err != nil {
		t.Fatalf("seed oauth flow: %v", err)
	}
	return seededCredentialAcqFlow{ID: result.Session.ID, State: result.State}
}

func (fx *credentialAcqHTTPFixture) seedRawOAuthFlow(t *testing.T, providerAccountID int64, vendor, authMode string) seededCredentialAcqFlow {
	t.Helper()
	return fx.seedRawOAuthFlowWithActor(t, providerAccountID, vendor, authMode, "11", admin.RolePlatformAdmin)
}

func (fx *credentialAcqHTTPFixture) seedRawOAuthFlowWithActor(t *testing.T, providerAccountID int64, vendor, authMode, actorID, actorRole string) seededCredentialAcqFlow {
	t.Helper()
	state := "cursor-state"
	aad := credentialstore.AAD{TenantID: 1, ProviderAccountID: providerAccountID, Vendor: vendor, AuthMode: authMode}
	ciphertext, metadata, _, err := fx.store.EncryptTransientPayload(context.Background(), []byte("pkce-verifier"), aad)
	if err != nil {
		t.Fatalf("encrypt pkce verifier: %v", err)
	}
	session, err := fx.store.Create(context.Background(), credentialacq.Session{
		TenantID: 1, ProviderAccountID: providerAccountID,
		Vendor: vendor, AuthMode: authMode, Kind: credentialacq.FlowKindOAuth, Status: credentialacq.StatusStarted,
		ActorID: actorID, ActorRole: actorRole,
		StateHash: credentialacq.HashOAuthState(state), NonceHash: metadata, EncryptedPKCEVerifier: ciphertext,
		ClientIdentitySource: credentialacq.ClientSourcePublicCLI,
		RedirectURI:          "https://huakai.example.test/callback",
		AuthType:             credentialacq.AuthTypePKCE,
	})
	if err != nil {
		t.Fatalf("seed raw oauth flow: %v", err)
	}
	return seededCredentialAcqFlow{ID: session.ID, State: state}
}

func oauthBrowserCallbackPath(flowID, state, code string) string {
	v := url.Values{}
	v.Set("flow_id", flowID)
	v.Set("state", state)
	v.Set("code", code)
	return "/admin/v1/credentials/oauth-callback?" + v.Encode()
}

type credentialAcqCreatorStub struct {
	mu     sync.Mutex
	next   int64
	inputs []credentialstore.CreateCredentialInput
}

func (s *credentialAcqCreatorStub) Create(_ context.Context, in credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	s.inputs = append(s.inputs, in)
	return credentialstore.CredentialMetadata{
		ID: s.next, TenantID: in.TenantID, ProviderAccountID: in.ProviderAccountID,
		Vendor: credentialstore.Normalize(in.Vendor), AuthMode: credentialstore.Normalize(in.AuthMode),
		State: credentialstore.StateActive, Version: 1,
	}, nil
}

func (s *credentialAcqCreatorStub) inputsSnapshot() []credentialstore.CreateCredentialInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]credentialstore.CreateCredentialInput, len(s.inputs))
	copy(out, s.inputs)
	return out
}

type credentialAcqAuditStub struct {
	mu     sync.Mutex
	events []credentialstore.AuditEvent
}

func (s *credentialAcqAuditStub) InsertAuditEvent(_ context.Context, e credentialstore.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (s *credentialAcqAuditStub) eventsSnapshot() []credentialstore.AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]credentialstore.AuditEvent, len(s.events))
	copy(out, s.events)
	return out
}

type credentialAcqExchangerStub struct {
	mu      sync.Mutex
	calls   []credentialAcqExchangeCall
	payload []byte
	err     error
}

type credentialAcqExchangeCall struct {
	FlowID string
	Vendor string
	Mode   string
	Code   string
}

type geminiAdminStartConfigGuardExchanger struct {
	calls        int
	clientSecret string
}

func (s *geminiAdminStartConfigGuardExchanger) StartOAuthFlow(ctx context.Context, store *credentialacq.PostgresSessionStore, in credentialacq.StartInput, cfg credentialacq.OAuthClientConfig) (credentialacq.OAuthStartResult, error) {
	s.calls++
	s.clientSecret = cfg.ClientSecret
	if strings.TrimSpace(cfg.ClientSecret) != "" {
		return credentialacq.OAuthStartResult{}, errors.New("gemini request client_secret reached StartOAuthFlow")
	}
	registry := credentialacq.NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(in.Vendor, in.AuthMode), credentialacq.NewPKCEFakeExchanger(credentialacq.TokenShapeAnySessionOrAccess)); err != nil {
		return credentialacq.OAuthStartResult{}, err
	}
	return credentialacq.StartOAuthFlowWithRegistry(ctx, store, in, cfg, registry)
}

func (s *geminiAdminStartConfigGuardExchanger) ExchangeOAuthCode(context.Context, credentialacq.Session, string) (credentialacq.CredentialCandidate, error) {
	return credentialacq.CredentialCandidate{}, errors.New("not used")
}

type chatGPTAdminStartConfigGuardExchanger struct {
	calls        int
	clientSecret string
}

func (s *chatGPTAdminStartConfigGuardExchanger) StartOAuthFlow(ctx context.Context, store *credentialacq.PostgresSessionStore, in credentialacq.StartInput, cfg credentialacq.OAuthClientConfig) (credentialacq.OAuthStartResult, error) {
	s.calls++
	s.clientSecret = cfg.ClientSecret
	if strings.TrimSpace(cfg.ClientSecret) != "" {
		return credentialacq.OAuthStartResult{}, errors.New("chatgpt request client_secret reached StartOAuthFlow")
	}
	registry := credentialacq.NewExchangerRegistry()
	if err := registry.RegisterExchanger(credentialstore.ModeKey(in.Vendor, in.AuthMode), credentialacq.NewPKCEFakeExchanger(credentialacq.TokenShapeAnySessionOrAccess)); err != nil {
		return credentialacq.OAuthStartResult{}, err
	}
	return credentialacq.StartOAuthFlowWithRegistry(ctx, store, in, cfg, registry)
}

func (s *chatGPTAdminStartConfigGuardExchanger) ExchangeOAuthCode(context.Context, credentialacq.Session, string) (credentialacq.CredentialCandidate, error) {
	return credentialacq.CredentialCandidate{}, errors.New("not used")
}

func (s *credentialAcqExchangerStub) StartOAuthFlow(ctx context.Context, store *credentialacq.PostgresSessionStore, in credentialacq.StartInput, cfg credentialacq.OAuthClientConfig) (credentialacq.OAuthStartResult, error) {
	return credentialacq.StartOAuthFlowWithRegistry(ctx, store, in, cfg, credentialacq.NewExchangerRegistry())
}

func (s *credentialAcqExchangerStub) ExchangeOAuthCode(_ context.Context, session credentialacq.Session, code string) (credentialacq.CredentialCandidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, credentialAcqExchangeCall{
		FlowID: session.ID, Vendor: session.Vendor, Mode: session.AuthMode, Code: code,
	})
	if s.err != nil {
		return credentialacq.CredentialCandidate{}, s.err
	}
	payload := s.payload
	if len(payload) == 0 {
		payload = []byte(`{"session_token":"registry-session","refresh_token":"registry-refresh"}`)
	}
	return credentialacq.CredentialCandidate{
		TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
		Vendor: session.Vendor, AuthMode: session.AuthMode, Payload: payload, ActorID: session.ActorID,
	}, nil
}

func (s *credentialAcqExchangerStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

type credentialAcqSessionDB struct {
	mu   sync.Mutex
	now  time.Time
	rows map[string]credentialacq.Session
}

func newCredentialAcqSessionDB(now time.Time) *credentialAcqSessionDB {
	return &credentialAcqSessionDB{now: now, rows: map[string]credentialacq.Session{}}
}

func (db *credentialAcqSessionDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (db *credentialAcqSessionDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("credential acquisition test db: Query not implemented")
}

func (db *credentialAcqSessionDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	db.mu.Lock()
	defer db.mu.Unlock()
	switch {
	case strings.Contains(sql, "INSERT INTO credential_acquisition_flow_sessions"):
		row := credentialacq.Session{
			ID: argString(args[0]), TenantID: argInt64(args[1]), ProviderAccountID: argInt64(args[2]),
			Vendor: argString(args[3]), AuthMode: argString(args[4]), Kind: credentialacq.FlowKind(argString(args[5])), Status: credentialacq.FlowStatus(argString(args[6])),
			ActorID: argString(args[7]), ActorRole: argString(args[8]),
			StateHash: argBytes(args[9]), NonceHash: argBytes(args[10]), EncryptedPKCEVerifier: argBytes(args[11]),
			ClientIdentitySource: argString(args[12]), AuthType: credentialacq.AuthTypePKCE, RedirectURI: argString(args[13]),
			LongLivedRequested: argBool(args[16]), IdempotencyKeyHash: argBytes(args[17]),
			ExpiresAt: argTime(args[18]), CreatedAt: db.now, UpdatedAt: db.now,
		}
		_ = json.Unmarshal(argBytes(args[14]), &row.RequestedScopes)
		_ = json.Unmarshal(argBytes(args[15]), &row.RedactedContext)
		db.rows[row.ID] = row
		return credentialAcqRow{session: row}
	case strings.Contains(sql, "FROM credential_acquisition_flow_sessions") && strings.Contains(sql, "WHERE id = $1::uuid"):
		row, ok := db.rows[argString(args[0])]
		if !ok {
			return credentialAcqRow{err: pgx.ErrNoRows}
		}
		return credentialAcqRow{session: row}
	case strings.Contains(sql, "SET status = $2"):
		row, ok := db.rows[argString(args[0])]
		if !ok {
			return credentialAcqRow{err: pgx.ErrNoRows}
		}
		row.Status = credentialacq.FlowStatus(argString(args[1]))
		row.ErrorClass = argString(args[2])
		row.ErrorMessageRedacted = argString(args[3])
		row.UpdatedAt = db.now
		db.rows[row.ID] = row
		return credentialAcqRow{session: row}
	case strings.Contains(sql, "SET status = 'cancelled'"):
		row, ok := db.rows[argString(args[0])]
		if !ok || row.Status == credentialacq.StatusFinalized || row.Status == credentialacq.StatusCancelled || row.Status == credentialacq.StatusExpired {
			return credentialAcqRow{err: pgx.ErrNoRows}
		}
		row.Status = credentialacq.StatusCancelled
		row.CancelledAt = db.now
		row.UpdatedAt = db.now
		db.rows[row.ID] = row
		return credentialAcqRow{session: row}
	case strings.Contains(sql, "SET consumed_at = NOW()"):
		row, ok := db.rows[argString(args[0])]
		// mirror BeginFinalize 的真 SQL predicate —— callback 式 OAuth(非
		// device_code/sso)未到 validated 不可 finalize。复用生产导出 helper credentialacq.RequiresCallbackValidation,
		// 与真 SQL / credentialacq fake 同源,避免 handler 测试 double 漂移、给"started PKCE OAuth 可 finalize"假信心。
		if !ok || !row.ConsumedAt.IsZero() || row.Status == credentialacq.StatusFinalized || row.Status == credentialacq.StatusCancelled || row.Status == credentialacq.StatusExpired || !row.ExpiresAt.After(db.now) ||
			(credentialacq.RequiresCallbackValidation(row.Kind, row.AuthType) && row.Status != credentialacq.StatusValidated) {
			return credentialAcqRow{err: pgx.ErrNoRows}
		}
		row.ConsumedAt = db.now
		row.UpdatedAt = db.now
		db.rows[row.ID] = row
		return credentialAcqRow{session: row}
	case strings.Contains(sql, "SET status = 'finalized'"):
		row, ok := db.rows[argString(args[0])]
		if !ok {
			return credentialAcqRow{err: pgx.ErrNoRows}
		}
		row.Status = credentialacq.StatusFinalized
		row.ResultAccountCredentialID = argInt64(args[1])
		if row.ConsumedAt.IsZero() {
			row.ConsumedAt = db.now
		}
		row.UpdatedAt = db.now
		db.rows[row.ID] = row
		return credentialAcqRow{session: row}
	default:
		return credentialAcqRow{err: errors.New("credential acquisition test db: unhandled query")}
	}
}

type credentialAcqRow struct {
	session credentialacq.Session
	err     error
}

func (r credentialAcqRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanCredentialAcqSession(dest, r.session)
}

func scanCredentialAcqSession(dest []any, row credentialacq.Session) error {
	scopes, _ := json.Marshal(row.RequestedScopes)
	redacted, _ := json.Marshal(row.RedactedContext)
	devicePayload, _ := json.Marshal(row.DeviceCodePayload)
	if string(devicePayload) == "null" {
		devicePayload = nil
	}
	values := []any{
		row.ID, row.TenantID, row.ProviderAccountID, row.Vendor, row.AuthMode, row.Kind, row.Status,
		row.ActorID, row.ActorRole, row.StateHash, row.NonceHash, row.EncryptedPKCEVerifier,
		row.ClientIdentitySource, pgText(string(row.AuthType)), devicePayload, pgText(row.RedirectURI), scopes, redacted,
		row.LongLivedRequested, row.IdempotencyKeyHash, pgInt8(row.ResultAccountCredentialID),
		pgText(row.ErrorClass), pgText(row.ErrorMessageRedacted), row.ExpiresAt, pgTime(row.ConsumedAt), pgTime(row.CancelledAt),
		row.CreatedAt, row.UpdatedAt,
	}
	for i := range dest {
		assignCredentialAcqScan(dest[i], values[i])
	}
	return nil
}

func assignCredentialAcqScan(dest any, value any) {
	switch d := dest.(type) {
	case *string:
		*d = value.(string)
	case *int64:
		*d = value.(int64)
	case *bool:
		*d = value.(bool)
	case *credentialacq.FlowKind:
		*d = value.(credentialacq.FlowKind)
	case *credentialacq.FlowStatus:
		*d = value.(credentialacq.FlowStatus)
	case *[]byte:
		*d = append([]byte(nil), value.([]byte)...)
	case *time.Time:
		*d = value.(time.Time)
	case *pgtype.Text:
		*d = value.(pgtype.Text)
	case *pgtype.Int8:
		*d = value.(pgtype.Int8)
	case *pgtype.Timestamptz:
		*d = value.(pgtype.Timestamptz)
	}
}

func pgText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: strings.TrimSpace(value) != ""}
}

func pgInt8(value int64) pgtype.Int8 {
	return pgtype.Int8{Int64: value, Valid: value != 0}
}

func pgTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: !value.IsZero()}
}

func argString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case credentialacq.FlowKind:
		return string(v)
	case credentialacq.FlowStatus:
		return string(v)
	default:
		return ""
	}
}

func argInt64(value any) int64 {
	if v, ok := value.(int64); ok {
		return v
	}
	return 0
}

func argBool(value any) bool {
	if v, ok := value.(bool); ok {
		return v
	}
	return false
}

func argBytes(value any) []byte {
	if v, ok := value.([]byte); ok {
		return append([]byte(nil), v...)
	}
	return nil
}

func argTime(value any) time.Time {
	if v, ok := value.(time.Time); ok {
		return v
	}
	return time.Time{}
}
