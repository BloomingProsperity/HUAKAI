// 包 bedrock — PassthroughAdapter 单元测试。
//
// 测试覆盖点：
//  1. 缺少 aws_region 时拒绝请求。
//  2. aws_sigv4 模式下缺少 aws_access_key_id 时拒绝。
//  3. 不支持的凭据形态（apikey）被拒绝。
//  4. stream=true 时切换到 :invoke-with-response-stream endpoint。
//  5. 有 session token 时 x-amz-security-token header 存在。
//  6. 含 "." 和 ":" 的 model id 被正确 URL 编码。
//  7. body 字节在签名路径中原样保留。
//  8. upstream_passthrough 模式直接透传 Authorization header value。
package bedrock

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// fixedTime 用于在测试中注入确定性时间戳，避免签名结果随机变化。
var fixedTime = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

// newTestAdapter 返回注入了固定时间的 PassthroughAdapter。
func newTestAdapter() *PassthroughAdapter {
	return &PassthroughAdapter{
		nowFunc: func() time.Time { return fixedTime },
	}
}

// validSigV4Input 返回一个合法的 aws_sigv4 BuildInput，供各测试用例复用。
func validSigV4Input() provider.BuildInput {
	return provider.BuildInput{
		UpstreamModelID: "anthropic.claude-3-5-sonnet-20241022-v2:0",
		InboundBody:     []byte(`{"prompt":"hello"}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAWSSigV4,
			Value: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
			Extra: map[string]string{
				"aws_region":        "us-east-1",
				"aws_access_key_id": "AKIDEXAMPLE",
			},
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 拒绝分支测试
// ─────────────────────────────────────────────────────────────────────────────

// TestRejectMissingRegion 验证缺少 aws_region 时 BuildRequest 返回 error。
func TestRejectMissingRegion(t *testing.T) {
	a := newTestAdapter()
	in := validSigV4Input()
	delete(in.Credential.Extra, "aws_region")

	_, err := a.BuildRequest(context.Background(), in)
	if err == nil {
		t.Fatal("期望返回 error，但得到 nil")
	}
	if !strings.Contains(err.Error(), "aws_region") {
		t.Errorf("error 应提及 aws_region，got: %v", err)
	}
}

// TestRejectInvalidRegion 抓的回归:aws_region 来自运营侧账号配置,虽非客户可控,
// 仍必须 fail-closed,避免污染值进入 Bedrock host。
// 变异:删 BuildRequest 的 validBedrockRegion 校验 → 这些非法值会继续构造请求,
// 本测试红。
func TestRejectInvalidRegion(t *testing.T) {
	badRegions := []string{
		"us.east-1",
		"us-east-1@internal",
		"us-east-1:443",
		"169.254.169.254",
		"localhost",
		"http://us-east-1",
		"US-EAST-1",
		"-us-east-1",
		"us-east-1-",
		"global",
	}
	for _, region := range badRegions {
		t.Run(region, func(t *testing.T) {
			a := newTestAdapter()
			in := validSigV4Input()
			in.Credential.Extra["aws_region"] = region

			_, err := a.BuildRequest(context.Background(), in)
			if err == nil {
				t.Fatalf("非法 aws_region %q 未被拒", region)
			}
			if !strings.Contains(err.Error(), "aws_region") {
				t.Fatalf("error=%v, want 提及 aws_region", err)
			}
		})
	}
}

// TestRegionValidationMutationWouldPoisonHost 证明 fixture 判别性:若上层校验被删,
// 含点号的 region 会被原样拼进 host,而不是被 URL 编码或清洗。
func TestRegionValidationMutationWouldPoisonHost(t *testing.T) {
	const poisonedRegion = "us-east-1.evil.internal"
	if validBedrockRegion(poisonedRegion) {
		t.Fatalf("测试前提失效: %q 不应在白名单内", poisonedRegion)
	}
	endpoint, err := (&PassthroughAdapter{}).buildEndpoint(poisonedRegion, "anthropic.claude-3-5-sonnet-20241022-v2:0", false)
	if err != nil {
		t.Fatalf("buildEndpoint: %v", err)
	}
	if !strings.Contains(endpoint, "bedrock-runtime."+poisonedRegion+".amazonaws.com") {
		t.Fatalf("fixture 不判别: endpoint=%q 未体现污染 region", endpoint)
	}
}

// TestRejectMissingAccessKeyID 验证 aws_sigv4 模式下缺少 aws_access_key_id 时返回 error。
func TestRejectMissingAccessKeyID(t *testing.T) {
	a := newTestAdapter()
	in := validSigV4Input()
	delete(in.Credential.Extra, "aws_access_key_id")

	_, err := a.BuildRequest(context.Background(), in)
	if err == nil {
		t.Fatal("期望返回 error，但得到 nil")
	}
	if !strings.Contains(err.Error(), "aws_access_key_id") {
		t.Errorf("error 应提及 aws_access_key_id，got: %v", err)
	}
}

// TestRejectUnsupportedCredentialType 验证 apikey 凭据形态被拒绝。
func TestRejectUnsupportedCredentialType(t *testing.T) {
	a := newTestAdapter()
	in := validSigV4Input()
	in.Credential.Type = provider.CredentialTypeAPIKey

	_, err := a.BuildRequest(context.Background(), in)
	if err == nil {
		t.Fatal("期望返回 error，但得到 nil")
	}
	if !strings.Contains(err.Error(), "不支持的凭据形态") {
		t.Errorf("error 应提及不支持的凭据形态，got: %v", err)
	}
}

// TestRejectEmptyModelID 验证 UpstreamModelID 为空时返回 error。
func TestRejectEmptyModelID(t *testing.T) {
	a := newTestAdapter()
	in := validSigV4Input()
	in.UpstreamModelID = ""

	_, err := a.BuildRequest(context.Background(), in)
	if err == nil {
		t.Fatal("期望返回 error，但得到 nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 正常路径测试
// ─────────────────────────────────────────────────────────────────────────────

// TestStreamEndpoint 验证 stream=true 时 URL 切换到 :invoke-with-response-stream。
func TestStreamEndpoint(t *testing.T) {
	a := newTestAdapter()
	in := validSigV4Input()
	in.Credential.Extra["stream"] = "true"

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest 失败: %v", err)
	}

	if !strings.Contains(req.URL.Path, "invoke-with-response-stream") {
		t.Errorf("流式 endpoint 应包含 invoke-with-response-stream，got: %s", req.URL.String())
	}
}

// TestNonStreamEndpoint 验证非流式时 URL 使用 /invoke。
func TestNonStreamEndpoint(t *testing.T) {
	a := newTestAdapter()
	in := validSigV4Input()

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest 失败: %v", err)
	}

	if !strings.HasSuffix(req.URL.Path, "/invoke") {
		t.Errorf("非流式 endpoint 应以 /invoke 结尾，got: %s", req.URL.Path)
	}
	if strings.Contains(req.URL.Path, "stream") {
		t.Errorf("非流式 endpoint 不应含 stream，got: %s", req.URL.Path)
	}
}

func TestValidRegionAccepted(t *testing.T) {
	for _, region := range []string{"us-east-1", "ap-northeast-1"} {
		t.Run(region, func(t *testing.T) {
			a := newTestAdapter()
			in := validSigV4Input()
			in.Credential.Extra["aws_region"] = region

			req, err := a.BuildRequest(context.Background(), in)
			if err != nil {
				t.Fatalf("合法 aws_region %q 被拒: %v", region, err)
			}
			if !strings.Contains(req.URL.Host, region) {
				t.Fatalf("host=%q 未包含 region %q", req.URL.Host, region)
			}
		})
	}
}

// TestSessionTokenHeader 验证存在 aws_session_token 时请求头含 x-amz-security-token。
func TestSessionTokenHeader(t *testing.T) {
	a := newTestAdapter()
	in := validSigV4Input()
	in.Credential.Extra["aws_session_token"] = "FakeSessionTokenXYZ"

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest 失败: %v", err)
	}

	got := req.Header.Get("X-Amz-Security-Token")
	if got != "FakeSessionTokenXYZ" {
		t.Errorf("x-amz-security-token = %q, want %q", got, "FakeSessionTokenXYZ")
	}
}

// TestModelIDURLEscaping 验证含 "." 和 ":" 的 model id 被正确 URL 编码写入 path。
//
// 示例 model id: "anthropic.claude-3-5-sonnet-20241022-v2:0"
// url.PathEscape 对 ":" 编码为 "%3A"，"." 保留（unreserved）。
func TestModelIDURLEscaping(t *testing.T) {
	a := newTestAdapter()
	in := validSigV4Input()
	// 使用含 "." 和 ":" 的 Bedrock 官方 model id
	in.UpstreamModelID = "anthropic.claude-3-5-sonnet-20241022-v2:0"

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest 失败: %v", err)
	}

	rawPath := req.URL.RawPath
	if rawPath == "" {
		rawPath = req.URL.Path
	}

	// ":" 必须被编码为 %3A（大写，url.PathEscape 行为）
	if !strings.Contains(rawPath, "%3A") {
		t.Errorf("model id 中的 ':' 应被编码为 %%3A，path: %s", rawPath)
	}
	// "." 保留（url.PathEscape 不编码点）
	if !strings.Contains(rawPath, "anthropic.claude") {
		t.Errorf("model id 中的 '.' 应保留，path: %s", rawPath)
	}
}

// TestBodyPreservedThroughSigning 验证 body 字节在签名路径中原样保留。
func TestBodyPreservedThroughSigning(t *testing.T) {
	a := newTestAdapter()
	in := validSigV4Input()
	wantBody := []byte(`{"messages":[{"role":"user","content":"test body preservation"}]}`)
	in.InboundBody = wantBody

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest 失败: %v", err)
	}

	gotBody, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("读取请求 body 失败: %v", err)
	}

	if string(gotBody) != string(wantBody) {
		t.Errorf("body 不匹配\n  got:  %s\n  want: %s", gotBody, wantBody)
	}
}

// TestUpstreamPassthroughMode 验证 upstream_passthrough 模式直接透传 Authorization header。
func TestUpstreamPassthroughMode(t *testing.T) {
	a := newTestAdapter()
	wantAuth := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260506/us-east-1/bedrock/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc123precomputed"

	in := provider.BuildInput{
		UpstreamModelID: "anthropic.claude-3-opus-20240229",
		InboundBody:     []byte(`{"prompt":"hi"}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: wantAuth,
			Extra: map[string]string{
				"aws_region": "us-east-1",
			},
		},
	}

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest 失败: %v", err)
	}

	got := req.Header.Get("Authorization")
	if got != wantAuth {
		t.Errorf("Authorization header 不匹配\n  got:  %s\n  want: %s", got, wantAuth)
	}
}

// TestSigV4HeadersPresent 验证 aws_sigv4 路径下 Authorization、x-amz-date、
// x-amz-content-sha256 header 均已设置。
func TestSigV4HeadersPresent(t *testing.T) {
	a := newTestAdapter()
	in := validSigV4Input()

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest 失败: %v", err)
	}

	for _, h := range []string{"Authorization", "X-Amz-Date", "X-Amz-Content-Sha256"} {
		if req.Header.Get(h) == "" {
			t.Errorf("header %q 不应为空", h)
		}
	}

	// Authorization 应以 AWS4-HMAC-SHA256 开头
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		t.Errorf("Authorization 应以 AWS4-HMAC-SHA256 开头，got: %s", auth)
	}
}

// TestPlatform 验证 Platform() 返回 "bedrock"。
func TestPlatform(t *testing.T) {
	a := &PassthroughAdapter{}
	if got := a.Platform(); got != "bedrock" {
		t.Errorf("Platform() = %q, want %q", got, "bedrock")
	}
}

// TestAcceptableCredentialTypes 验证可接受凭据类型集合。
func TestAcceptableCredentialTypes(t *testing.T) {
	a := &PassthroughAdapter{}
	types := a.AcceptableCredentialTypes()

	has := func(t provider.CredentialType) bool {
		for _, ct := range types {
			if ct == t {
				return true
			}
		}
		return false
	}

	if !has(provider.CredentialTypeAWSSigV4) {
		t.Error("应包含 CredentialTypeAWSSigV4")
	}
	if !has(provider.CredentialTypeUpstreamPassthrough) {
		t.Error("应包含 CredentialTypeUpstreamPassthrough")
	}
	if has(provider.CredentialTypeAPIKey) {
		t.Error("不应包含 CredentialTypeAPIKey")
	}
}
