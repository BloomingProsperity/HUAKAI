package dify

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func apikeyInput(extra map[string]string) provider.BuildInput {
	return provider.BuildInput{
		InboundBody: []byte(`{"query":"hi","user":"u"}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "app-token-1",
			Extra: extra,
		},
	}
}

// TestBuildRequestDefaultEndpointAndBearer 抓的回归:默认 base/路径漂移或
// apikey 凭据丢 Bearer 前缀(Dify 鉴权即失败 401)。
func TestBuildRequestDefaultEndpointAndBearer(t *testing.T) {
	a := &Adapter{}
	req, err := a.BuildRequest(context.Background(), apikeyInput(nil))
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.URL.String(); got != "https://api.dify.ai/v1/chat-messages" {
		t.Fatalf("endpoint=%q want https://api.dify.ai/v1/chat-messages", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer app-token-1" {
		t.Fatalf("Authorization=%q want Bearer 前缀", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("非流式 Accept=%q want application/json", got)
	}
	if req.Method != "POST" {
		t.Fatalf("method=%q want POST", req.Method)
	}
}

// TestBuildRequestBotTypeRouting 抓的回归:chat 形态 bot_type 接错端点、
// 非法值静默回落默认端点,以及 workflow/completion 在形态投影落地前被误放行
// (放行=用户内容到不了 app、响应恒空,见 endpointPathForBotType 注释)。
func TestBuildRequestBotTypeRouting(t *testing.T) {
	a := &Adapter{}
	for _, tc := range []struct {
		botType string
		want    string
	}{
		{"", "https://api.dify.ai/v1/chat-messages"},
		{"chatflow", "https://api.dify.ai/v1/chat-messages"},
		{"agent", "https://api.dify.ai/v1/chat-messages"},
	} {
		req, err := a.BuildRequest(context.Background(), apikeyInput(map[string]string{"bot_type": tc.botType}))
		if err != nil {
			t.Fatalf("bot_type=%q BuildRequest: %v", tc.botType, err)
		}
		if got := req.URL.String(); got != tc.want {
			t.Errorf("bot_type=%q endpoint=%q want %q", tc.botType, got, tc.want)
		}
	}

	// workflow/completion:形态投影未落地,必须 fail-closed(mutation:
	// 把 endpointPathForBotType 改回放行 → 此处红)。
	for _, botType := range []string{"workflow", "completion"} {
		if _, err := a.BuildRequest(context.Background(), apikeyInput(map[string]string{"bot_type": botType})); err == nil {
			t.Errorf("bot_type=%q 应 fail-closed(投影未落地),却放行了", botType)
		}
	}

	if _, err := a.BuildRequest(context.Background(), apikeyInput(map[string]string{"bot_type": "chatbotx"})); err == nil {
		t.Fatal("非法 bot_type 必须报错,不得静默回落默认端点")
	}
}

// TestBuildRequestStreamAcceptHeader 抓的回归:流式请求 Accept 不是
// text/event-stream(部分网关/反代按 Accept 决定是否缓冲)。
func TestBuildRequestStreamAcceptHeader(t *testing.T) {
	a := &Adapter{}
	req, err := a.BuildRequest(context.Background(), apikeyInput(map[string]string{"stream": "true"}))
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("流式 Accept=%q want text/event-stream", got)
	}
}

// TestBuildRequestEmptyCredentialValue 抓的回归:空凭据 Value 放行出站
// (上游 401 还耗一次配额/重试预算)。
func TestBuildRequestEmptyCredentialValue(t *testing.T) {
	a := &Adapter{}
	in := apikeyInput(nil)
	in.Credential.Value = ""
	if _, err := a.BuildRequest(context.Background(), in); err == nil {
		t.Fatal("空凭据 Value 必须报错")
	}
}

// TestBuildRequestRejectsUnsupportedCredentialType 抓的回归:不支持的凭据
// 形态(oauth/session)被静默当 apikey 注入。
func TestBuildRequestRejectsUnsupportedCredentialType(t *testing.T) {
	a := &Adapter{}
	for _, ct := range []provider.CredentialType{
		provider.CredentialTypeOAuthAccessToken,
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeAWSSigV4,
	} {
		in := apikeyInput(nil)
		in.Credential.Type = ct
		if _, err := a.BuildRequest(context.Background(), in); err == nil {
			t.Errorf("凭据形态 %q 应被拒绝", ct)
		}
	}
}

// TestBuildRequestUpstreamPassthroughBaseURL 抓的回归:自托管 base_url 不
// 生效(请求仍发云端,把第三方 token 泄给官方端点),以及透传凭据被错误加
// Bearer 前缀。
func TestBuildRequestUpstreamPassthroughBaseURL(t *testing.T) {
	a := &Adapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Bearer self-hosted-token",
			Extra: map[string]string{"base_url": "https://dify.example.com"},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.URL.String(); got != "https://dify.example.com/v1/chat-messages" {
		t.Fatalf("endpoint=%q want https://dify.example.com/v1/chat-messages", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer self-hosted-token" {
		t.Fatalf("透传凭据应原样注入: %q", got)
	}
}

// TestBuildRequestUpstreamPassthroughCustomAuthHeader 抓的回归:
// Extra["auth_header"] 覆盖失效(自定义网关头被忽略)。
func TestBuildRequestUpstreamPassthroughCustomAuthHeader(t *testing.T) {
	a := &Adapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "raw-key",
			Extra: map[string]string{
				"base_url":    "https://dify.example.com",
				"auth_header": "X-App-Key",
			},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.Header.Get("X-App-Key"); got != "raw-key" {
		t.Fatalf("X-App-Key=%q want raw-key", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("指定 auth_header 后不应再写 Authorization: %q", got)
	}
}

// TestBuildRequestPrivateBaseURLRejectedBySSRFGuard 抓的回归:私网/环回
// base_url 绕过统一 SSRF 守卫(内网探测面)。
func TestBuildRequestPrivateBaseURLRejectedBySSRFGuard(t *testing.T) {
	a := &Adapter{}
	for _, base := range []string{"https://127.0.0.1", "https://10.0.0.8", "http://dify.example.com"} {
		in := provider.BuildInput{
			InboundBody: []byte(`{}`),
			Credential: provider.Credential{
				Type:  provider.CredentialTypeUpstreamPassthrough,
				Value: "k",
				Extra: map[string]string{"base_url": base},
			},
		}
		_, err := a.BuildRequest(context.Background(), in)
		if err == nil {
			t.Errorf("base_url=%q 应被 SSRF 守卫拒绝", base)
			continue
		}
		if !strings.Contains(err.Error(), "endpoint rejected") {
			t.Errorf("base_url=%q 错误形态应为 endpoint rejected: %v", base, err)
		}
	}
}

// TestBuildRequestEndpointPathOverride 抓的回归:in.EndpointPath 覆盖逻辑
// 失效(调用方指定的端点 path 被默认 path 吞掉)。
func TestBuildRequestEndpointPathOverride(t *testing.T) {
	a := &Adapter{}
	in := apikeyInput(nil)
	in.EndpointPath = "/v1/completion-messages"
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.URL.String(); got != "https://api.dify.ai/v1/completion-messages" {
		t.Fatalf("EndpointPath 覆盖失效: %q", got)
	}
}

// TestBuildRequestEndpointFieldOverridesBase 抓的回归:Endpoint 字段(httptest
// 注入口)失效,测试流量打到真实云端。
func TestBuildRequestEndpointFieldOverridesBase(t *testing.T) {
	a := &Adapter{Endpoint: "http://127.0.0.1:18080"}
	req, err := a.BuildRequest(context.Background(), apikeyInput(nil))
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.URL.String(); got != "http://127.0.0.1:18080/v1/chat-messages" {
		t.Fatalf("Endpoint 覆盖失效: %q", got)
	}
}
