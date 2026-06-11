package gemini

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func codeAssistInput(t *testing.T, cred provider.Credential, model string, body []byte) provider.BuildInput {
	t.Helper()
	return provider.BuildInput{
		UpstreamModelID: model,
		InboundBody:     body,
		Credential:      cred,
	}
}

func sessionCred(extra map[string]string) provider.Credential {
	return provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "tok-123", Extra: extra}
}

func readBody(t *testing.T, in provider.BuildInput) []byte {
	t.Helper()
	a := &CodeAssistAdapter{}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest err=%v", err)
	}
	b, _ := io.ReadAll(req.Body)
	return b
}

// TestCodeAssistNonStreamURLAndAuth 守卫:非流式 URL == v1internal:generateContent,
// Authorization=="Bearer tok-123",且无 x-goog-api-key(纯 OAuth)。
// Mutation:把 action 改成 streamGenerateContent / 把鉴权改成 X-Goog-Api-Key → 红。
func TestCodeAssistNonStreamURLAndAuth(t *testing.T) {
	a := &CodeAssistAdapter{}
	req, err := a.BuildRequest(context.Background(), codeAssistInput(t,
		sessionCred(map[string]string{"project_id": "proj-x"}), "gemini-2.5-pro", []byte(`{"contents":[]}`)))
	if err != nil {
		t.Fatalf("BuildRequest err=%v", err)
	}
	if got := req.URL.String(); got != "https://cloudcode-pa.googleapis.com/v1internal:generateContent" {
		t.Fatalf("URL=%q want .../v1internal:generateContent", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok-123" {
		t.Fatalf("Authorization=%q want Bearer tok-123", got)
	}
	if got := req.Header.Get("X-Goog-Api-Key"); got != "" {
		t.Fatalf("X-Goog-Api-Key 应为空(纯 OAuth),got %q", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept=%q want application/json", got)
	}
}

// TestCodeAssistStreamURLAndAccept 守卫:Extra[stream]==true → URL 末尾
// :streamGenerateContent?alt=sse 且 Accept==text/event-stream。
// Mutation:漏 ?alt=sse / 漏切 streamGenerateContent / Accept 不切 → 红。
func TestCodeAssistStreamURLAndAccept(t *testing.T) {
	a := &CodeAssistAdapter{}
	req, err := a.BuildRequest(context.Background(), codeAssistInput(t,
		sessionCred(map[string]string{"project_id": "proj-x", "stream": "true"}), "gemini-2.5-pro", []byte(`{"contents":[]}`)))
	if err != nil {
		t.Fatalf("BuildRequest err=%v", err)
	}
	if got := req.URL.String(); got != "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse" {
		t.Fatalf("URL=%q want .../v1internal:streamGenerateContent?alt=sse", got)
	}
	if got := req.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept=%q want text/event-stream", got)
	}
}

// TestCodeAssistBodyEnvelope 守卫承重的 {model,project,request} envelope:
// 顶层 model==UpstreamModelID、project==Extra[project_id]、request 字段 ==
// InboundBody 原始字节(byte-for-byte,不重 marshal 丢字段)。
// Mutation:不嵌 request / 重 marshal 内层丢未知字段 / 漏 project → 各自子断言红。
func TestCodeAssistBodyEnvelope(t *testing.T) {
	inner := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"unknownVendorField":42}`)
	body := readBody(t, codeAssistInput(t,
		sessionCred(map[string]string{"project_id": "proj-x"}), "gemini-2.5-pro", inner))

	var env struct {
		Model   string          `json:"model"`
		Project string          `json:"project"`
		Request json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("out body not JSON envelope: %v\nbody=%s", err, body)
	}
	if env.Model != "gemini-2.5-pro" {
		t.Errorf("envelope.model=%q want gemini-2.5-pro", env.Model)
	}
	if env.Project != "proj-x" {
		t.Errorf("envelope.project=%q want proj-x", env.Project)
	}
	// request 必须是内层 gemini body 原文(含 unknownVendorField,证明未经重
	// marshal 丢字段)。比较 canonical JSON 等价。
	if !jsonEqual(t, env.Request, inner) {
		t.Fatalf("envelope.request != InboundBody\n got=%s\nwant=%s", env.Request, inner)
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var ma, mb any
	if err := json.Unmarshal(a, &ma); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &mb); err != nil {
		return false
	}
	na, _ := json.Marshal(ma)
	nb, _ := json.Marshal(mb)
	return string(na) == string(nb)
}

// TestCodeAssistMissingProjectFailsLoud 守卫:project_id 空 → 报错(cloudcode-pa
// 拒空 project,不静默送空)。Mutation:去掉空 project 守卫 → 红。
func TestCodeAssistMissingProjectFailsLoud(t *testing.T) {
	a := &CodeAssistAdapter{}
	for _, extra := range []map[string]string{nil, {"project_id": ""}, {"project_id": "   "}} {
		_, err := a.BuildRequest(context.Background(), codeAssistInput(t,
			sessionCred(extra), "gemini-2.5-pro", []byte(`{"contents":[]}`)))
		if err == nil {
			t.Fatalf("project_id=%v 应 fail loud,got nil err", extra)
		}
	}
}

// TestCodeAssistRejectsAPIKey 守卫:apikey 凭据被显式拒(cloudcode-pa 不收 api-key)。
// Mutation:把 apikey 加进 AcceptableCredentialTypes → 红。
func TestCodeAssistRejectsAPIKey(t *testing.T) {
	a := &CodeAssistAdapter{}
	_, err := a.BuildRequest(context.Background(), codeAssistInput(t,
		provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x", Extra: map[string]string{"project_id": "p"}},
		"gemini-2.5-pro", []byte(`{"contents":[]}`)))
	if err == nil {
		t.Fatalf("apikey 凭据应被拒,got nil err")
	}
}

// TestCodeAssistGuards 守卫各空值守卫:Value 空 / model 空 / InboundBody 空 → 报错。
func TestCodeAssistGuards(t *testing.T) {
	a := &CodeAssistAdapter{}
	cases := []struct {
		name  string
		cred  provider.Credential
		model string
		body  []byte
	}{
		{"empty value", provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "", Extra: map[string]string{"project_id": "p"}}, "m", []byte(`{}`)},
		{"empty model", sessionCred(map[string]string{"project_id": "p"}), "", []byte(`{}`)},
		{"empty body", sessionCred(map[string]string{"project_id": "p"}), "m", []byte(`  `)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.BuildRequest(context.Background(), codeAssistInput(t, tc.cred, tc.model, tc.body)); err == nil {
				t.Fatalf("%s 应报错,got nil", tc.name)
			}
		})
	}
}

// TestCodeAssistMimicryHeadersPresent 守卫最小必需 header(UA + X-Goog-Api-Client)
// 存在且非空——让调用能在 Code Assist 后端工作的最小集。
// Mutation:删 UA 或 X-Goog-Api-Client set → 红。
func TestCodeAssistMimicryHeadersPresent(t *testing.T) {
	a := &CodeAssistAdapter{}
	req, err := a.BuildRequest(context.Background(), codeAssistInput(t,
		sessionCred(map[string]string{"project_id": "p"}), "gemini-2.5-pro", []byte(`{}`)))
	if err != nil {
		t.Fatalf("BuildRequest err=%v", err)
	}
	if req.Header.Get("User-Agent") == "" {
		t.Errorf("User-Agent 缺失")
	}
	if req.Header.Get("X-Goog-Api-Client") == "" {
		t.Errorf("X-Goog-Api-Client 缺失")
	}
}

// TestCodeAssistUpstreamPassthroughAuthVerbatim 守卫:upstream_passthrough 的
// Value(已含完整 "Bearer ...")原样注入,不再加前缀(避免双 Bearer)。
// Mutation:对 upstream_passthrough 仍加 "Bearer " 前缀 → 红(双 Bearer)。
func TestCodeAssistUpstreamPassthroughAuthVerbatim(t *testing.T) {
	a := &CodeAssistAdapter{}
	req, err := a.BuildRequest(context.Background(), codeAssistInput(t,
		provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Bearer already-prefixed",
			Extra: map[string]string{"project_id": "p", "auth_header": "Authorization"},
		}, "gemini-2.5-pro", []byte(`{}`)))
	if err != nil {
		t.Fatalf("BuildRequest err=%v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer already-prefixed" {
		t.Fatalf("Authorization=%q want verbatim Bearer already-prefixed (no double Bearer)", got)
	}
}

// TestCodeAssistGeminiIngressStreamPath 守卫 brief 分析的 gemini ingress 流式
// 路径:gemini ingress 经 credentialWithNativeStreamMode 注入 Extra[stream]=true,
// adapter 据此选 streamGenerateContent。内层 gemini body 无顶层 stream 字段
// (流式由 URL 表达),证明主信号是 Extra[stream] 而非 body 探测。
// Mutation:把流式判定改成只读 body 探测 → 本测试(body 无 stream 字段)会落
// 非流式分支 → URL 不含 streamGenerateContent → 红。
func TestCodeAssistGeminiIngressStreamPath(t *testing.T) {
	a := &CodeAssistAdapter{}
	// 内层裸 gemini body,无顶层 "stream"。
	innerNoStream := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	req, err := a.BuildRequest(context.Background(), codeAssistInput(t,
		sessionCred(map[string]string{"project_id": "p", "stream": "true"}), "gemini-2.5-pro", innerNoStream))
	if err != nil {
		t.Fatalf("BuildRequest err=%v", err)
	}
	if got := req.URL.String(); got != "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse" {
		t.Fatalf("gemini ingress 流式 URL=%q want streamGenerateContent?alt=sse", got)
	}
}

// TestCodeAssistEndpointOverrideForHTTPTest 守卫 Endpoint 字段覆盖 base(供 httptest)。
func TestCodeAssistEndpointOverrideForHTTPTest(t *testing.T) {
	a := &CodeAssistAdapter{Endpoint: "https://example.test"}
	req, err := a.BuildRequest(context.Background(), codeAssistInput(t,
		sessionCred(map[string]string{"project_id": "p"}), "gemini-2.5-pro", []byte(`{}`)))
	if err != nil {
		t.Fatalf("BuildRequest err=%v", err)
	}
	if got := req.URL.String(); got != "https://example.test/v1internal:generateContent" {
		t.Fatalf("override URL=%q want https://example.test/v1internal:generateContent", got)
	}
}

// TestCodeAssistAcceptableCredentialTypes 守卫凭据形态白名单(含 session/oauth/
// upstream_passthrough,不含 apikey)。
func TestCodeAssistAcceptableCredentialTypes(t *testing.T) {
	a := &CodeAssistAdapter{}
	got := a.AcceptableCredentialTypes()
	want := map[provider.CredentialType]bool{
		provider.CredentialTypeSessionToken:        true,
		provider.CredentialTypeOAuthAccessToken:    true,
		provider.CredentialTypeUpstreamPassthrough: true,
	}
	if len(got) != len(want) {
		t.Fatalf("AcceptableCredentialTypes len=%d want %d (%v)", len(got), len(want), got)
	}
	for _, ct := range got {
		if !want[ct] {
			t.Errorf("unexpected acceptable credential type %q", ct)
		}
		if ct == provider.CredentialTypeAPIKey {
			t.Errorf("apikey 不应在白名单内")
		}
	}
}

// TestCodeAssistCrossProtocolStreamIntent 守卫跨协议流式兜底:openai/anthropic
// ingress 翻译进来无 Extra["stream"]、内层 gemini body 无顶层 stream 字段
// (body 探测恒 false),端点选择只能靠 BuildInput.ClientStreamIntent。
// MUTATION: 删 stream 判定的 ClientStreamIntent 分支 → 错选非流 v1internal:
// generateContent → 本测试红。
func TestCodeAssistCrossProtocolStreamIntent(t *testing.T) {
	a := &CodeAssistAdapter{}
	in := codeAssistInput(t, sessionCred(map[string]string{"project_id": "proj-x"}), "gemini-2.5-pro", []byte(`{"contents":[]}`))
	in.ClientStreamIntent = true
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest err=%v", err)
	}
	if got := req.URL.String(); got != "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse" {
		t.Fatalf("URL=%q want .../v1internal:streamGenerateContent?alt=sse(跨协议流式意图被丢)", got)
	}
	if got := req.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept=%q want text/event-stream", got)
	}
}
