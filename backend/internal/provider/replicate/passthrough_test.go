package replicate

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func apikeyBuildInput(model string) provider.BuildInput {
	return provider.BuildInput{
		UpstreamModelID: model,
		InboundBody:     []byte(`{"model":"alias","prompt":"a fox"}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "r8_test_token",
		},
	}
}

// TestBuildRequestDefaultEndpointModelInPath 抓的回归:endpoint 模板漂移、
// {model} 占位替换失效(%7Bmodel%7D 留在 URL 里),或 owner/name 的 "/" 被
// 整体 PathEscape 成 %2F(Replicate 端点是两个独立路径段)。
func TestBuildRequestDefaultEndpointModelInPath(t *testing.T) {
	a := &Adapter{}
	req, err := a.BuildRequest(context.Background(), apikeyBuildInput("black-forest-labs/flux-1.1-pro"))
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	want := "https://api.replicate.com/v1/models/black-forest-labs/flux-1.1-pro/predictions"
	if got := req.URL.String(); got != want {
		t.Fatalf("endpoint=%q want %q", got, want)
	}
	if req.Method != "POST" {
		t.Fatalf("method=%q want POST", req.Method)
	}
}

// TestBuildRequestModelSegmentsEscapedIndividually 抓的回归:段内保留字符
// 不转义(空格/问号断 URL),或逐段 escape 退化成整体 escape。
func TestBuildRequestModelSegmentsEscapedIndividually(t *testing.T) {
	a := &Adapter{}
	req, err := a.BuildRequest(context.Background(), apikeyBuildInput("own er/na?me"))
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.URL.String(); !strings.Contains(got, "/models/own%20er/na%3Fme/") {
		t.Fatalf("endpoint=%q want 段内字符转义且段间 / 保留", got)
	}
}

// TestBuildRequestWaitAndDeadlineHeaders 计费正确性守卫：同步等待结束时，上游
// 自动取消边界也必须同时生效。删任一头或让两个秒数漂移，本断言必红。
func TestBuildRequestWaitAndDeadlineHeaders(t *testing.T) {
	a := &Adapter{}
	req, err := a.BuildRequest(context.Background(), apikeyBuildInput("o/m"))
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.Header.Get("Prefer"); got != "wait=60" {
		t.Fatalf("Prefer=%q want wait=60(同步等待是计费正确性承重墙)", got)
	}
	if got := req.Header.Get("Cancel-After"); got != "60s" {
		t.Fatalf("Cancel-After=%q want 60s(退款后不得继续运行)", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept=%q", got)
	}
}

// TestBuildRequestBodyIsTranslatedNotPassthrough 抓的回归:OpenAI 形 body
// 原样透传(Replicate 422)而非 {"input":{...}} 翻译产物。
func TestBuildRequestBodyIsTranslatedNotPassthrough(t *testing.T) {
	a := &Adapter{}
	req, err := a.BuildRequest(context.Background(), apikeyBuildInput("o/m"))
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	body, _ := io.ReadAll(req.Body)
	if got := string(body); got != `{"input":{"prompt":"a fox"}}` {
		t.Fatalf("body=%s want 翻译产物 {\"input\":{\"prompt\":\"a fox\"}}", got)
	}
}

// TestBuildRequestAuthForms 抓的回归:apikey 丢 Bearer 前缀(401),
// upstream_passthrough 被强加前缀或 auth_header 覆盖失效。
func TestBuildRequestAuthForms(t *testing.T) {
	a := &Adapter{}
	req, err := a.BuildRequest(context.Background(), apikeyBuildInput("o/m"))
	if err != nil {
		t.Fatalf("BuildRequest(apikey): %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer r8_test_token" {
		t.Fatalf("apikey Authorization=%q want Bearer 前缀", got)
	}

	in := apikeyBuildInput("o/m")
	in.Credential = provider.Credential{
		Type:  provider.CredentialTypeUpstreamPassthrough,
		Value: "Token raw-value",
		Extra: map[string]string{"auth_header": "X-Replicate-Key"},
	}
	req, err = a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest(passthrough): %v", err)
	}
	if got := req.Header.Get("X-Replicate-Key"); got != "Token raw-value" {
		t.Fatalf("passthrough 自定义头=%q want 原样透传", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("指定 auth_header 后不应再写 Authorization: %q", got)
	}
}

// TestBuildRequestCredentialGuards 抓的回归:不支持的凭据形态 / 空 Value /
// 空 model 被静默放行(出站必 401 或 404,且已扣 reserve)。
func TestBuildRequestCredentialGuards(t *testing.T) {
	a := &Adapter{}
	in := apikeyBuildInput("o/m")
	in.Credential.Type = provider.CredentialTypeOAuthAccessToken
	if _, err := a.BuildRequest(context.Background(), in); err == nil {
		t.Fatal("oauth_access_token 应被拒")
	}
	in = apikeyBuildInput("o/m")
	in.Credential.Value = ""
	if _, err := a.BuildRequest(context.Background(), in); err == nil {
		t.Fatal("空凭据 Value 应被拒")
	}
	in = apikeyBuildInput("  ")
	if _, err := a.BuildRequest(context.Background(), in); err == nil {
		t.Fatal("空 UpstreamModelID 应被拒(model 进 URL path)")
	}
}

// TestBuildRequestB64JSONFailsClosed 第二道闸(第一道在 imageshttp 入口 400):
// b64_json body 流到 adapter 时必须 fail-loud,不得静默降级成 url 出站。
func TestBuildRequestB64JSONFailsClosed(t *testing.T) {
	a := &Adapter{}
	in := apikeyBuildInput("o/m")
	in.InboundBody = []byte(`{"prompt":"p","response_format":"b64_json"}`)
	if _, err := a.BuildRequest(context.Background(), in); err == nil {
		t.Fatal("b64_json 应在 BuildRequest fail-loud")
	}
}

// TestBuildRequestEndpointPathSemantics 抓的回归:图片 lane 统一下发的
// /v1/images/generations 被当真实 path 用(打到 api.replicate.com 不存在的
// 端点),edits/variations 被误放行(multipart 上传子请求 v1 范围外),或含
// {model} 的自定义模板覆盖失效。
func TestBuildRequestEndpointPathSemantics(t *testing.T) {
	a := &Adapter{}

	in := apikeyBuildInput("o/m")
	in.EndpointPath = "/v1/images/generations"
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("generations lane path 应映射到默认模板: %v", err)
	}
	if got := req.URL.String(); got != "https://api.replicate.com/v1/models/o/m/predictions" {
		t.Fatalf("endpoint=%q want 默认 predictions 模板", got)
	}

	for _, path := range []string{"/v1/images/edits", "/v1/images/variations", "/v1/other"} {
		in := apikeyBuildInput("o/m")
		in.EndpointPath = path
		if _, err := a.BuildRequest(context.Background(), in); err == nil {
			t.Fatalf("EndpointPath=%q 应 fail-loud", path)
		}
	}

	in = apikeyBuildInput("o/m")
	in.EndpointPath = "https://alt.example.com/v2/models/{model}/predictions"
	req, err = a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("自定义模板: %v", err)
	}
	if got := req.URL.String(); got != "https://alt.example.com/v2/models/o/m/predictions" {
		t.Fatalf("endpoint=%q want 自定义模板替换 {model}", got)
	}
}

// TestBuildRequestPrivateBaseURLRejectedBySSRFGuard 抓的回归:私网/环回
// base_url 绕过统一 SSRF 守卫(内网探测面)。
func TestBuildRequestPrivateBaseURLRejectedBySSRFGuard(t *testing.T) {
	a := &Adapter{}
	for _, base := range []string{"https://127.0.0.1", "https://10.0.0.8", "http://replicate.example.com"} {
		in := apikeyBuildInput("o/m")
		in.Credential = provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "k",
			Extra: map[string]string{"base_url": base},
		}
		if _, err := a.BuildRequest(context.Background(), in); err == nil {
			t.Errorf("base_url=%q 应被 SSRF 守卫拒绝", base)
		} else if !strings.Contains(err.Error(), "endpoint rejected") {
			t.Errorf("base_url=%q 错误形态应为 endpoint rejected: %v", base, err)
		}
	}
}

// TestAdapterIdentity 抓的回归:Platform 漂移(transport 策略按 "replicate"
// 取 RoundTripper,漂移=整族 ErrUnknownProvider)。
func TestAdapterIdentity(t *testing.T) {
	a := &Adapter{}
	if got := a.Platform(); got != "replicate" {
		t.Fatalf("Platform=%q want replicate", got)
	}
}
