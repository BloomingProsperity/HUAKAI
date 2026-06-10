package ollama

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func apikeyInput(value string) provider.BuildInput {
	return provider.BuildInput{
		InboundBody: []byte(`{"model":"llama3.2","messages":[],"stream":false}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: value,
		},
	}
}

// TestBuildRequestDefaultEndpointAndHeaders 抓的回归:默认 base/路径漂移
// (/api/chat 是原生端点,漂到 /v1/chat/completions 即打到兼容层)或
// Content-Type/Accept/method 形态错。
func TestBuildRequestDefaultEndpointAndHeaders(t *testing.T) {
	a := &Adapter{}
	req, err := a.BuildRequest(context.Background(), apikeyInput("k1"))
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.URL.String(); got != "http://127.0.0.1:11434/api/chat" {
		t.Fatalf("endpoint=%q want http://127.0.0.1:11434/api/chat", got)
	}
	if req.Method != "POST" {
		t.Fatalf("method=%q want POST", req.Method)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept=%q want application/json(NDJSON 不经 Accept 协商,无 stream 特判)", got)
	}
	body, _ := io.ReadAll(req.Body)
	if string(body) != `{"model":"llama3.2","messages":[],"stream":false}` {
		t.Fatalf("body 应为 InboundBody 直读: %s", body)
	}
}

// TestBuildRequestEmptyValueSendsNoAuthHeader 抓的回归:Ollama 通常无鉴权,
// 空 Value 是合法形态——必须完全不发 Authorization 头。
// Mutation:空 Value 也注 "Bearer "(空 Bearer)→ 本测试红。
func TestBuildRequestEmptyValueSendsNoAuthHeader(t *testing.T) {
	a := &Adapter{}
	req, err := a.BuildRequest(context.Background(), apikeyInput(""))
	if err != nil {
		t.Fatalf("空 Value 必须合法(无鉴权实例): %v", err)
	}
	if vals, present := req.Header["Authorization"]; present {
		t.Fatalf("空 Value 不得发任何 Authorization 头(含空 Bearer): %v", vals)
	}

	// upstream_passthrough 形态同理:空 Value 不发头。
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "",
		},
	}
	req2, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("upstream_passthrough 空 Value 必须合法: %v", err)
	}
	if vals, present := req2.Header["Authorization"]; present {
		t.Fatalf("透传空 Value 不得发 Authorization 头: %v", vals)
	}
}

// TestBuildRequestNonEmptyAPIKeyBearer 抓的回归:apikey 凭据丢 Bearer 前缀
// (带鉴权反代后的 Ollama 即 401)。
func TestBuildRequestNonEmptyAPIKeyBearer(t *testing.T) {
	a := &Adapter{}
	req, err := a.BuildRequest(context.Background(), apikeyInput("secret-key"))
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer secret-key" {
		t.Fatalf("Authorization=%q want Bearer secret-key", got)
	}
}

// TestBuildRequestRejectsUnsupportedCredentialType 抓的回归:不支持的凭据
// 形态(oauth/session/sigv4)被静默当 apikey 注入。
func TestBuildRequestRejectsUnsupportedCredentialType(t *testing.T) {
	a := &Adapter{}
	for _, ct := range []provider.CredentialType{
		provider.CredentialTypeOAuthAccessToken,
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeAWSSigV4,
	} {
		in := apikeyInput("k")
		in.Credential.Type = ct
		if _, err := a.BuildRequest(context.Background(), in); err == nil {
			t.Errorf("凭据形态 %q 应被拒绝", ct)
		}
	}
}

// TestBuildRequestUpstreamPassthroughBaseURL 抓的回归:自托管 base_url 不
// 生效(请求仍发默认占位主机),以及透传凭据被错误加 Bearer 前缀(透传
// Value 自带前缀)。
func TestBuildRequestUpstreamPassthroughBaseURL(t *testing.T) {
	a := &Adapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Bearer self-hosted-token",
			Extra: map[string]string{"base_url": "https://ollama.example.com"},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.URL.String(); got != "https://ollama.example.com/api/chat" {
		t.Fatalf("endpoint=%q want https://ollama.example.com/api/chat", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer self-hosted-token" {
		t.Fatalf("透传凭据应原样注入(不二次加前缀): %q", got)
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
				"base_url":    "https://ollama.example.com",
				"auth_header": "X-Ollama-Key",
			},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.Header.Get("X-Ollama-Key"); got != "raw-key" {
		t.Fatalf("X-Ollama-Key=%q want raw-key", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("指定 auth_header 后不应再写 Authorization: %q", got)
	}
}

// TestBuildRequestPrivateBaseURLRejectedBySSRFGuard 抓的回归:私网/环回/明文
// base_url 绕过统一 SSRF 守卫(内网探测面)——adapter 必须走
// EndpointForBuildInput,不得自拼 endpoint。
func TestBuildRequestPrivateBaseURLRejectedBySSRFGuard(t *testing.T) {
	a := &Adapter{}
	for _, base := range []string{"https://127.0.0.1", "https://10.0.0.8", "http://ollama.example.com"} {
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
	in := apikeyInput("k")
	in.EndpointPath = "/api/generate"
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.URL.String(); got != "http://127.0.0.1:11434/api/generate" {
		t.Fatalf("EndpointPath 覆盖失效: %q", got)
	}
}

// TestBuildRequestEndpointFieldOverridesBase 抓的回归:Endpoint 字段(httptest
// 注入口)失效,测试流量打到默认占位主机。
func TestBuildRequestEndpointFieldOverridesBase(t *testing.T) {
	a := &Adapter{Endpoint: "http://127.0.0.1:18080"}
	req, err := a.BuildRequest(context.Background(), apikeyInput("k"))
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.URL.String(); got != "http://127.0.0.1:18080/api/chat" {
		t.Fatalf("Endpoint 覆盖失效: %q", got)
	}
}

// TestPlatformAndCredentialTypes 抓的回归:平台标识漂移(transport 策略按
// "ollama" 放行)或凭据集合缩水。
func TestPlatformAndCredentialTypes(t *testing.T) {
	a := &Adapter{}
	if got := a.Platform(); got != "ollama" {
		t.Fatalf("Platform=%q want ollama", got)
	}
	types := a.AcceptableCredentialTypes()
	if len(types) != 2 || types[0] != provider.CredentialTypeAPIKey || types[1] != provider.CredentialTypeUpstreamPassthrough {
		t.Fatalf("AcceptableCredentialTypes=%v want [apikey upstream_passthrough]", types)
	}
}
