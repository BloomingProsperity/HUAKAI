package vertex

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func upstreamCred(extra map[string]string) provider.Credential {
	return provider.Credential{
		Type:  provider.CredentialTypeUpstreamPassthrough,
		Value: "Bearer tok123",
		Extra: extra,
	}
}

// TestGeminiNonStreamURL 钉死 Gemini 非流式完整 URL（逐字符相等）。
// 判别性:错 host/publisher/action 任一字段都会让全等断言红。
func TestGeminiNonStreamURL(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeGemini}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gemini-2.5-pro",
		InboundBody:     []byte(`{"contents":[]}`),
		Credential:      upstreamCred(map[string]string{"project_id": "p", "location": "us-central1", "auth_header": "Authorization"}),
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	want := "https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/publishers/google/models/gemini-2.5-pro:generateContent"
	if got := req.URL.String(); got != want {
		t.Fatalf("URL=%q\nwant %q", got, want)
	}
}

// TestGeminiBodyPassthroughByteForByte 抓的回归:Gemini 模式 body 必须原样直通,
// 不得 reshape。判别性:如果 Gemini 误走 reshapeAnthropicBody 会注入
// anthropic_version 并删字段,body 改变 → 断言红。
func TestGeminiBodyPassthroughByteForByte(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeGemini}
	in := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"model":"ignored"}`)
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gemini-2.5-pro",
		InboundBody:     in,
		Credential:      upstreamCred(map[string]string{"project_id": "p"}),
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	got := readBody(t, req)
	if string(got) != string(in) {
		t.Fatalf("Gemini body 被改写:\ngot:  %s\nwant: %s", got, in)
	}
}

// TestGeminiStreamURL 抓的回归:Gemini 流式 → streamGenerateContent + ?alt=sse。
// Mutation:把 stream action 改回 generateContent 或漏 alt=sse → 断言红。
func TestGeminiStreamURL(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeGemini}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gemini-2.5-pro",
		InboundBody:     []byte(`{}`),
		Credential:      upstreamCred(map[string]string{"project_id": "p", "location": "us-central1", "stream": "true"}),
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	got := req.URL.String()
	if !strings.Contains(got, ":streamGenerateContent") {
		t.Errorf("流式 action 不是 streamGenerateContent: %q", got)
	}
	if req.URL.Query().Get("alt") != "sse" {
		t.Errorf("流式缺 ?alt=sse: %q", got)
	}
}

// TestLocationGlobalNoRegionPrefix 抓的回归:location=global → host 无区域前缀。
// Mutation:翻转 global 分支 → host 变 "global-aiplatform..." 断言红。
func TestLocationGlobalNoRegionPrefix(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeGemini}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gemini-2.5-pro",
		InboundBody:     []byte(`{}`),
		Credential:      upstreamCred(map[string]string{"project_id": "p", "location": "global"}),
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.URL.Host; got != "aiplatform.googleapis.com" {
		t.Fatalf("global host=%q want aiplatform.googleapis.com (无区域前缀)", got)
	}
}

// TestLocationDefaultsToUSCentral1 抓的回归:location 空 → 默认 us-central1。
// Mutation:删默认值 → location 段为空、host 变 "-aiplatform..." 断言红。
func TestLocationDefaultsToUSCentral1(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeGemini}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gemini-2.5-pro",
		InboundBody:     []byte(`{}`),
		Credential:      upstreamCred(map[string]string{"project_id": "p"}),
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.URL.Host; got != "us-central1-aiplatform.googleapis.com" {
		t.Fatalf("default location host=%q want us-central1-aiplatform.googleapis.com", got)
	}
	if !strings.Contains(req.URL.Path, "/locations/us-central1/") {
		t.Fatalf("path 缺 default location 段: %q", req.URL.Path)
	}
}

// TestLocationInvalidRejected 抓的回归:非法 location 被 ^[a-z0-9-]+$ 拒。
// 这是 SSRF/path-injection 判别器。Mutation:删 regex 校验 → 恶意 location
// 不报错、断言红。
func TestLocationInvalidRejected(t *testing.T) {
	bad := []string{
		"us central1", // 空格
		"evil.com/x",  // 路径注入
		"US-CENTRAL1", // 大写（白名单仅小写）
		"us_central1", // 下划线
		"../../etc",   // 目录穿越
		"a.b",         // 点（子域注入）
		"-evil",       // 前导连字符 → 畸形 host
		"useast-",     // 尾随连字符 → 畸形 host
		"--",          // 纯连字符
		"-",           // 单连字符
	}
	for _, loc := range bad {
		a := &PassthroughAdapter{Mode: ModeGemini}
		_, err := a.BuildRequest(context.Background(), provider.BuildInput{
			UpstreamModelID: "gemini-2.5-pro",
			InboundBody:     []byte(`{}`),
			Credential:      upstreamCred(map[string]string{"project_id": "p", "location": loc}),
		})
		if err == nil {
			t.Errorf("非法 location %q 未被拒（SSRF/path-injection 风险）", loc)
		}
	}
}

// TestAnthropicNonStreamURL 抓的回归:Anthropic 非流式 → publisher=anthropic +
// action=rawPredict。
func TestAnthropicNonStreamURL(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeAnthropic}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "claude-opus-4-1",
		InboundBody:     []byte(`{"model":"claude-opus-4-1","messages":[]}`),
		Credential:      upstreamCred(map[string]string{"project_id": "proj", "location": "us-east5"}),
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	want := "https://us-east5-aiplatform.googleapis.com/v1/projects/proj/locations/us-east5/publishers/anthropic/models/claude-opus-4-1:rawPredict"
	if got := req.URL.String(); got != want {
		t.Fatalf("URL=%q\nwant %q", got, want)
	}
}

// TestAnthropicStreamURL 抓的回归:Anthropic 流式 → streamRawPredict + ?alt=sse。
func TestAnthropicStreamURL(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeAnthropic}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "claude-opus-4-1",
		InboundBody:     []byte(`{"messages":[]}`),
		Credential:      upstreamCred(map[string]string{"project_id": "proj", "stream": "true"}),
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	got := req.URL.String()
	if !strings.Contains(got, ":streamRawPredict") {
		t.Errorf("流式 action 不是 streamRawPredict: %q", got)
	}
	if req.URL.Query().Get("alt") != "sse" {
		t.Errorf("流式缺 ?alt=sse: %q", got)
	}
}

// TestStreamFromInboundBodyWhenExtraUnset 抓的回归(S1):Anthropic ingress
// (/v1/messages) 不写 Credential.Extra["stream"](那只在 Gemini ingress 注入),
// 客户端 raw body 直通到此仍带 "stream":true。adapter 必须据 body 选
// streamRawPredict——否则 Claude-on-Vertex 流式整条建非流 URL,forwarder 收到
// buffered JSON 无法解析(主路径不可用)。
// Mutation:删 BuildRequest 的 inboundRequestsStream 回退 → 建 rawPredict、
// 无 ?alt=sse,本测试红。
func TestStreamFromInboundBodyWhenExtraUnset(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeAnthropic}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "claude-opus-4-1",
		InboundBody:     []byte(`{"model":"claude-opus-4-1","stream":true,"messages":[]}`),
		Credential:      upstreamCred(map[string]string{"project_id": "proj"}), // 注意:无 stream Extra
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	got := req.URL.String()
	if !strings.Contains(got, ":streamRawPredict") {
		t.Errorf("body stream:true 未驱动流式 action: %q", got)
	}
	if req.URL.Query().Get("alt") != "sse" {
		t.Errorf("body stream:true 未追加 ?alt=sse: %q", got)
	}
	// 出站 body 仍须剥掉 stream（Vertex 用 URL action 表达流式，body 不带）。
	if strings.Contains(string(readBody(t, req)), `"stream"`) {
		t.Errorf("出站 anthropic body 不应保留 stream 字段")
	}
}

// TestNonStreamBodyKeepsNonStreamURL 抓的回归:body 无 stream / stream:false
// 时不得误判成流式(inboundRequestsStream 把缺失/false 当 true)。
func TestNonStreamBodyKeepsNonStreamURL(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeAnthropic}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "claude-opus-4-1",
		InboundBody:     []byte(`{"stream":false,"messages":[]}`),
		Credential:      upstreamCred(map[string]string{"project_id": "proj"}),
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if strings.Contains(req.URL.String(), "stream") {
		t.Errorf("stream:false 不应建流式 URL: %q", req.URL.String())
	}
}

// TestAnthropicDatedModelSurvives 抓的回归:dated model 形 name@YYYYMMDD 的 @
// 必须在 URL path 段里原样存活（白名单含 @，不转义）。
// Mutation:从 validModelID 白名单删 '@' → BuildRequest 拒该 model、或改回
// PathEscape 使 @ 变 %40，本测试红。
func TestAnthropicDatedModelSurvives(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeAnthropic}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "claude-opus-4-1@20250805",
		InboundBody:     []byte(`{"messages":[]}`),
		Credential:      upstreamCred(map[string]string{"project_id": "proj"}),
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if !strings.Contains(req.URL.String(), "/models/claude-opus-4-1@20250805:rawPredict") {
		t.Fatalf("dated model @ 未存活: %q", req.URL.String())
	}
	if strings.Contains(req.URL.String(), "%40") {
		t.Fatalf("dated model @ 仍是 %%40 未还原: %q", req.URL.String())
	}
}

// TestAnthropicReshapeInBuildRequest 抓的回归:Anthropic 模式 BuildRequest 出站
// body 必须剥 model+stream + 注 anthropic_version。
func TestAnthropicReshapeInBuildRequest(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeAnthropic}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "claude-opus-4-1",
		InboundBody:     []byte(`{"model":"claude-opus-4-1","stream":false,"messages":[],"max_tokens":10}`),
		Credential:      upstreamCred(map[string]string{"project_id": "proj"}),
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	body := string(readBody(t, req))
	if strings.Contains(body, `"model"`) {
		t.Errorf("出站 body 仍含 model: %s", body)
	}
	if strings.Contains(body, `"stream"`) {
		t.Errorf("出站 body 仍含 stream: %s", body)
	}
	if !strings.Contains(body, `"anthropic_version":"`+AnthropicVersionVertex+`"`) {
		t.Errorf("出站 body 缺 anthropic_version: %s", body)
	}
}

// TestAnthropicBadJSONFailsClosed 抓的回归:Anthropic 模式坏 body → BuildRequest
// 报错，不发坏 body。
func TestAnthropicBadJSONFailsClosed(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeAnthropic}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "claude-opus-4-1",
		InboundBody:     []byte(`{"model":"x"`), // 截断
		Credential:      upstreamCred(map[string]string{"project_id": "proj"}),
	})
	if err == nil {
		t.Fatal("Anthropic 坏 body 应 fail-closed 报错")
	}
}

// TestAuthHeaderNoDoubleBearer 抓的回归:Value 已是 "Bearer tok" 时注入
// Authorization 不能变成双 Bearer；X-Goog-User-Project 必须等于 project_id。
// Mutation:删 X-Goog-User-Project set → 断言红。
func TestAuthHeaderNoDoubleBearer(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeGemini}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gemini-2.5-pro",
		InboundBody:     []byte(`{}`),
		Credential:      upstreamCred(map[string]string{"project_id": "myproj", "auth_header": "Authorization"}),
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok123" {
		t.Errorf("Authorization=%q want %q (无双 Bearer)", got, "Bearer tok123")
	}
	if got := req.Header.Get("X-Goog-User-Project"); got != "myproj" {
		t.Errorf("X-Goog-User-Project=%q want myproj", got)
	}
}

// TestRejectNonPassthroughCredential 抓的回归:非 upstream_passthrough 凭据被拒。
func TestRejectNonPassthroughCredential(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeGemini}
	for _, ct := range []provider.CredentialType{
		provider.CredentialTypeAPIKey,
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeOAuthAccessToken,
		provider.CredentialTypeAWSSigV4,
	} {
		_, err := a.BuildRequest(context.Background(), provider.BuildInput{
			UpstreamModelID: "gemini-2.5-pro",
			InboundBody:     []byte(`{}`),
			Credential:      provider.Credential{Type: ct, Value: "x", Extra: map[string]string{"project_id": "p"}},
		})
		if err == nil {
			t.Errorf("凭据形态 %q 应被拒", ct)
		}
	}
}

// TestProjectIDMissingRejected 抓的回归:project_id 空 → 报错（URL 必需）。
func TestProjectIDMissingRejected(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeGemini}
	for _, extra := range []map[string]string{
		{},                    // 完全缺
		{"project_id": ""},    // 空串
		{"project_id": "   "}, // 全空格
	} {
		_, err := a.BuildRequest(context.Background(), provider.BuildInput{
			UpstreamModelID: "gemini-2.5-pro",
			InboundBody:     []byte(`{}`),
			Credential:      upstreamCred(extra),
		})
		if err == nil {
			t.Errorf("project_id=%v 应报错", extra)
		}
	}
}

// TestPlatformAndCredentialTypes 钉死平台标识与可接受凭据形态。
func TestPlatformAndCredentialTypes(t *testing.T) {
	for _, mode := range []vertexMode{ModeGemini, ModeAnthropic} {
		a := &PassthroughAdapter{Mode: mode}
		if a.Platform() != "vertex" {
			t.Errorf("Platform()=%q want vertex", a.Platform())
		}
		types := a.AcceptableCredentialTypes()
		if len(types) != 1 || types[0] != provider.CredentialTypeUpstreamPassthrough {
			t.Errorf("AcceptableCredentialTypes()=%v want [upstream_passthrough]", types)
		}
	}
}

func readBody(t *testing.T, req *http.Request) []byte {
	t.Helper()
	if req.Body == nil {
		return nil
	}
	b, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return b
}

// TestGeminiCrossProtocolStreamIntent 守卫跨协议流式兜底:openai/anthropic
// ingress→vertex_gemini 无 Extra["stream"],marshal 的 gemini body 无顶层
// stream 字段(body 探测恒 false),端点选择只能靠 BuildInput.ClientStreamIntent。
// MUTATION: 删 stream 判定的 ClientStreamIntent 分支 → 错选非流 :generateContent
// → 本测试红。
func TestGeminiCrossProtocolStreamIntent(t *testing.T) {
	a := &PassthroughAdapter{Mode: ModeGemini}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID:    "gemini-2.5-pro",
		InboundBody:        []byte(`{"contents":[]}`),
		ClientStreamIntent: true,
		Credential:         upstreamCred(map[string]string{"project_id": "p", "location": "us-central1"}),
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	got := req.URL.String()
	if !strings.Contains(got, ":streamGenerateContent") {
		t.Errorf("流式 action 不是 streamGenerateContent: %q", got)
	}
	if req.URL.Query().Get("alt") != "sse" {
		t.Errorf("流式缺 ?alt=sse: %q", got)
	}
}
