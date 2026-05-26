package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/anthropicoauth"
)

// ANT-3 (Owner 2026-05-26 D-4=B): 即使 credential payload 写着
// oauth_token_endpoint=http://attacker.test, refresh 出站只能打 operator
// 配置 (r.Endpoint) 或 HUAKAI 硬编 defaultAnthropicTokenEndpoint。
// 自检 mutation: 把 endpoint 选取改回
// firstNonEmpty(r.Endpoint, credentialString(cred, "oauth_token_endpoint"),
// defaultAnthropicTokenEndpoint), capturedURL 会变成 attacker token endpoint,
// 该 test 立刻变红。
func TestAnthropicRefreshIgnoresCredentialOAuthTokenEndpoint(t *testing.T) {
	var capturedURL string
	client := &http.Client{Transport: anthropicAdapterRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedURL = r.URL.String()
		return anthropicAdapterJSONResponse(http.StatusOK, map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"expires_in":    3600,
		}), nil
	})}
	refresher := AnthropicRefresh{HTTPClient: client}
	// credential payload 故意夹带 attacker token endpoint。
	cred := []byte(`{
		"access_token":          "old",
		"refresh_token":         "rt-old",
		"oauth_token_endpoint":  "http://attacker.test/v1/oauth/token",
		"client_id":             "attacker-cid"
	}`)

	_, _, err := refresher.RefreshForProvider(context.Background(), 101, "anthropic", cred)
	if err != nil {
		t.Fatalf("Refresh err=%v", err)
	}
	if capturedURL == "http://attacker.test/v1/oauth/token" {
		t.Fatalf("token endpoint hit attacker URL %q — credential SSRF guard 失效", capturedURL)
	}
	if capturedURL != defaultAnthropicTokenEndpoint {
		t.Fatalf("token URL=%q want %q (built-in default)", capturedURL, defaultAnthropicTokenEndpoint)
	}
}

// ANT-3: credential payload 中的 client_id 不再被信任;只能使用 operator
// 注入的 r.ClientID 或 HUAKAI 硬编 anthropicoauth.AnthropicPublicCLIClientID。
func TestAnthropicRefreshIgnoresCredentialClientID(t *testing.T) {
	var capturedBody map[string]string
	client := &http.Client{Transport: anthropicAdapterRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &capturedBody)
		return anthropicAdapterJSONResponse(http.StatusOK, map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"expires_in":    3600,
		}), nil
	})}
	refresher := AnthropicRefresh{HTTPClient: client}
	cred := []byte(`{"access_token":"old","refresh_token":"rt-old","client_id":"attacker-cid"}`)

	if _, _, err := refresher.RefreshForProvider(context.Background(), 101, "anthropic", cred); err != nil {
		t.Fatalf("Refresh err=%v", err)
	}
	if capturedBody["client_id"] == "attacker-cid" {
		t.Fatalf("token endpoint saw attacker client_id %q", capturedBody["client_id"])
	}
	if capturedBody["client_id"] != anthropicoauth.AnthropicPublicCLIClientID {
		t.Fatalf("client_id=%q want built-in approved %q", capturedBody["client_id"], anthropicoauth.AnthropicPublicCLIClientID)
	}
}

// ANT-3 上游 401 invalid_grant: 适配器必须把响应 body 透到 err 文本,
// 让 credentialworker.ClassifyRefreshError 根据子串 "invalid_grant" 分类
// 为 OutcomeAuthExpired,而不是 "status 401" 落到 temporary。判别
// mutation: 删 tokenHTTPError.body 透传 (恢复纯 "status 401"),该 test
// 立刻变红;cursor C1 教训 — adapter 单元 test 必须用真上层 classifier
// 会抓的子串作断言,否则形同摆设。
func TestAnthropicRefreshSurfacesUpstream401InvalidGrant(t *testing.T) {
	var hits int
	client := &http.Client{Transport: anthropicAdapterRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits++
		return anthropicAdapterJSONResponse(http.StatusUnauthorized, map[string]any{
			"error":             "invalid_grant",
			"error_description": "refresh token expired",
		}), nil
	})}
	refresher := AnthropicRefresh{HTTPClient: client}
	cred := []byte(`{"access_token":"old","refresh_token":"rt-old"}`)

	_, _, err := refresher.RefreshForProvider(context.Background(), 101, "anthropic", cred)
	if err == nil {
		t.Fatal("expected refresh error on upstream 401")
	}
	if hits != 1 {
		t.Fatalf("token endpoint hits=%d want 1 (401 不应被 retry)", hits)
	}
	msg := err.Error()
	if !strings.Contains(msg, "401") {
		t.Fatalf("err=%v want HTTP 401 status surfaced", err)
	}
	if !strings.Contains(msg, "invalid_grant") {
		t.Fatalf("err=%v want upstream invalid_grant body surfaced for classifier", err)
	}
}

// ANT-3 happy path: operator 给 endpoint, refresh_token 从 credential payload 读,
// access_token / refresh_token 来自上游响应。验证 r.Endpoint override 仍生效
// (D-4=B 信任的是 operator,不是 credential payload)。
func TestAnthropicRefreshOperatorEndpointOverrideUsedForOutbound(t *testing.T) {
	var capturedURL string
	client := &http.Client{Transport: anthropicAdapterRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedURL = r.URL.String()
		return anthropicAdapterJSONResponse(http.StatusOK, map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"expires_in":    3600,
		}), nil
	})}
	refresher := AnthropicRefresh{
		Endpoint:   "https://operator.anthropic.test/v1/oauth/token",
		ClientID:   "operator-injected-cid",
		HTTPClient: client,
	}
	cred := []byte(`{"access_token":"old","refresh_token":"rt-old","oauth_token_endpoint":"http://attacker.test","client_id":"attacker-cid"}`)

	newCred, _, err := refresher.RefreshForProvider(context.Background(), 101, "anthropic", cred)
	if err != nil {
		t.Fatalf("Refresh err=%v", err)
	}
	if capturedURL != "https://operator.anthropic.test/v1/oauth/token" {
		t.Fatalf("operator endpoint override 未生效, captured=%q", capturedURL)
	}
	var merged map[string]any
	if err := json.Unmarshal(newCred, &merged); err != nil {
		t.Fatalf("decode merged credential: %v", err)
	}
	if merged["access_token"] != "new-access" {
		t.Fatalf("merged access_token=%v want new-access", merged["access_token"])
	}
}

// ANT-3 credential payload 缺 refresh_token + setup_token 也无 long-lived 模式 →
// 直接报错;mutation: 把 r.AllowLongLivedSetupToken 静默接受空 setup_token 这里仍变红。
func TestAnthropicRefreshFailsClosedWithoutRefreshToken(t *testing.T) {
	refresher := AnthropicRefresh{
		HTTPClient: &http.Client{Transport: anthropicAdapterRoundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("HTTP must not fire when refresh_token is missing")
			return nil, errors.New("unreachable")
		})},
	}
	cred := []byte(`{"access_token":"old"}`)
	_, _, err := refresher.RefreshForProvider(context.Background(), 101, "anthropic", cred)
	if err == nil {
		t.Fatal("expected error when refresh_token missing")
	}
	if !strings.Contains(err.Error(), "refresh_token") {
		t.Fatalf("err=%v want refresh_token is empty signal", err)
	}
}

type anthropicAdapterRoundTripFunc func(*http.Request) (*http.Response, error)

func (f anthropicAdapterRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func anthropicAdapterJSONResponse(status int, body map[string]any) *http.Response {
	raw, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(raw))),
	}
}
