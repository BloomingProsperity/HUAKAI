// OpenAI Codex session 反转适配器 — 表格驱动测试。
package openai

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// ── 编译期接口合规性（文件独立，不依赖其它测试） ──────────────────────────────
// var _ 已在 codex_session.go 中声明；测试文件此处仅做说明性注释，不重复声明。

func TestCodexSessionAdapter_Platform(t *testing.T) {
	a := &CodexSessionAdapter{}
	if got := a.Platform(); got != "openai_codex" {
		t.Errorf("Platform()=%q want openai_codex", got)
	}
}

func TestCodexSessionAdapter_AcceptableCredentialTypes(t *testing.T) {
	a := &CodexSessionAdapter{}
	got := a.AcceptableCredentialTypes()
	if len(got) != 2 {
		t.Fatalf("AcceptableCredentialTypes 长度=%d want 2: %v", len(got), got)
	}
	want := map[provider.CredentialType]bool{
		provider.CredentialTypeSessionToken:        true,
		provider.CredentialTypeUpstreamPassthrough: true,
	}
	for _, ct := range got {
		if !want[ct] {
			t.Errorf("意外的凭据类型 %q", ct)
		}
	}
	// apikey 不应在列表中
	for _, ct := range got {
		if ct == provider.CredentialTypeAPIKey {
			t.Errorf("apikey 不应出现在 AcceptableCredentialTypes 中")
		}
	}
}

// ── BuildRequest 正常路径：session token → Authorization Bearer + endpoint ─

func TestCodexSessionAdapter_BuildRequest_SessionToken(t *testing.T) {
	a := &CodexSessionAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{"model":"gpt-5.5","input":"hi","stream":false,"store":true,"max_output_tokens":16}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "sb-session-token-fake",
			Extra: map[string]string{
				"oai_device_id": "device-uuid-fake",
			},
		},
		Account: provider.AccountInfo{AccountID: 1, Platform: "openai_codex", AccountType: "session"},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	// 方法必须是 POST
	if req.Method != "POST" {
		t.Errorf("Method=%q want POST", req.Method)
	}

	// 默认 endpoint
	if got := req.URL.String(); got != "https://chatgpt.com/backend-api/codex/responses" {
		t.Errorf("URL=%q want 默认 codex endpoint", got)
	}

	// Authorization Bearer 注入
	if got := req.Header.Get("Authorization"); got != "Bearer sb-session-token-fake" {
		t.Errorf("Authorization=%q want Bearer sb-session-token-fake", got)
	}

	// OAI-Device-Id 注入
	if got := req.Header.Get("OAI-Device-Id"); got != "device-uuid-fake" {
		t.Errorf("OAI-Device-Id=%q want device-uuid-fake", got)
	}

	// OAI-Language 固定 en-US
	if got := req.Header.Get("OAI-Language"); got != "en-US" {
		t.Errorf("OAI-Language=%q want en-US", got)
	}

	// Content-Type
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type=%q want application/json", got)
	}
	if got := req.Header.Get("Accept"); got != "text/event-stream" {
		t.Errorf("Accept=%q want text/event-stream", got)
	}
	if got := req.Header.Get("originator"); got != "codex_cli_rs" {
		t.Errorf("originator=%q want codex_cli_rs", got)
	}

	// Body 只做 Codex Responses 必要规整,保留其它字段。
	body, _ := io.ReadAll(req.Body)
	gotBody := decodeJSONBody(t, body)
	if gotBody["stream"] != true {
		t.Fatalf("stream=%v want true; body=%s", gotBody["stream"], body)
	}
	if gotBody["store"] != false {
		t.Fatalf("store=%v want false; body=%s", gotBody["store"], body)
	}
	if _, ok := gotBody["max_output_tokens"]; ok {
		t.Fatalf("max_output_tokens 未剥离: body=%s", body)
	}
	if gotBody["input"] != "hi" {
		t.Fatalf("input=%v want hi; body=%s", gotBody["input"], body)
	}
}

func TestCodexSessionAdapter_BuildRequest_RemovesUnsupportedResponsesFields(t *testing.T) {
	a := &CodexSessionAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-5.5",
		InboundBody: []byte(`{
			"model":"gpt-5.5",
			"instructions":"answer briefly",
			"input":"hi",
			"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],
			"tool_choice":"auto",
			"parallel_tool_calls":true,
			"reasoning":{"effort":"low"},
			"text":{"format":{"type":"json_object"}},
			"include":["reasoning.encrypted_content"],
			"temperature":0.7,
			"top_p":0.9,
			"max_output_tokens":16,
			"stop":["\n\n"],
			"max_completion_tokens":32,
			"frequency_penalty":0.1,
			"presence_penalty":0.2,
			"logprobs":true,
			"top_logprobs":2,
			"n":2,
			"stream_options":{"include_usage":true},
			"user":"owner",
			"metadata":{"trace":"x"},
			"prompt_cache_retention":"24h",
			"safety_identifier":"sid"
		}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "sb-session-token-fake",
		},
	}

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(req.Body)
	gotBody := decodeJSONBody(t, raw)

	removedFields := []string{
		"temperature",
		"top_p",
		"max_output_tokens",
		"stop",
		"max_completion_tokens",
		"frequency_penalty",
		"presence_penalty",
		"logprobs",
		"top_logprobs",
		"n",
		"stream_options",
		"user",
		"metadata",
		"prompt_cache_retention",
		"safety_identifier",
	}
	for _, field := range removedFields {
		if _, ok := gotBody[field]; ok {
			t.Fatalf("%s 未剥离: body=%s", field, raw)
		}
	}
	preservedFields := []string{
		"tools",
		"tool_choice",
		"parallel_tool_calls",
		"input",
		"model",
		"instructions",
		"reasoning",
		"text",
		"include",
	}
	for _, field := range preservedFields {
		if _, ok := gotBody[field]; !ok {
			t.Fatalf("%s 被误删: body=%s", field, raw)
		}
	}
	if gotBody["stream"] != true {
		t.Fatalf("stream=%v want true; body=%s", gotBody["stream"], raw)
	}
	if gotBody["store"] != false {
		t.Fatalf("store=%v want false; body=%s", gotBody["store"], raw)
	}
}

// ── BuildRequest 自定义 endpoint ────────────────────────────────────────────

func TestCodexSessionAdapter_BuildRequest_CustomEndpoint(t *testing.T) {
	a := &CodexSessionAdapter{Endpoint: "https://chatgpt.com/backend-api/codex/chat"}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "sb-tok",
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.URL.String(); got != "https://chatgpt.com/backend-api/codex/chat" {
		t.Errorf("自定义 endpoint 未生效: URL=%q", got)
	}
}

func TestCodexSessionAdapter_BuildRequest_ExtraBaseURLOverridesEndpoint(t *testing.T) {
	a := &CodexSessionAdapter{Endpoint: "https://chatgpt.com/backend-api/codex/chat"}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "sb-tok",
			Extra: map[string]string{
				"base_url": " https://codex-proxy.example/backend-api/codex/completions?route=primary ",
			},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.URL.String(); got != "https://codex-proxy.example/backend-api/codex/completions?route=primary" {
		t.Fatalf("Extra base_url 未覆盖 endpoint: URL=%q", got)
	}
}

func TestCodexSessionAdapter_BuildRequest_LocalBaseURLAllowedForE2E(t *testing.T) {
	a := &CodexSessionAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "sb-tok",
			Extra: map[string]string{
				"base_url": "http://127.0.0.1:18080/backend-api/codex/completions",
			},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.URL.String(); got != "http://127.0.0.1:18080/backend-api/codex/completions" {
		t.Fatalf("本机 base_url 未保留: URL=%q", got)
	}
}

func TestCodexSessionAdapter_BuildRequest_RejectUnsafeBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
	}{
		{name: "http 非本机", baseURL: "http://codex-proxy.example/backend-api/codex/completions"},
		{name: "metadata IP", baseURL: "https://169.254.169.254/latest/meta-data"},
		{name: "metadata host", baseURL: "https://metadata.google.internal/backend-api/codex/completions"},
		{name: "私网 IP", baseURL: "https://10.0.0.1/backend-api/codex/completions"},
		{name: "特殊用途 IP", baseURL: "https://203.0.113.10/backend-api/codex/completions"},
		{name: "空 host", baseURL: "https:///backend-api/codex/completions"},
		{name: "编码 host", baseURL: "https://example%2ecom/backend-api/codex/completions"},
		{name: "混淆数字 host", baseURL: "https://0177.0.0.1/backend-api/codex/completions"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &CodexSessionAdapter{}
			in := provider.BuildInput{
				UpstreamModelID: "gpt-4o",
				InboundBody:     []byte(`{}`),
				Credential: provider.Credential{
					Type:  provider.CredentialTypeSessionToken,
					Value: "sb-tok",
					Extra: map[string]string{"base_url": tc.baseURL},
				},
			}
			_, err := a.BuildRequest(context.Background(), in)
			if err == nil {
				t.Fatal("不安全 base_url 应被拒绝")
			}
			if !strings.Contains(err.Error(), "base_url 非法") {
				t.Fatalf("error=%v want base_url 非法", err)
			}
		})
	}
}

// ── BuildRequest UpstreamPassthrough：完整 Authorization header 透传 ──────

func TestCodexSessionAdapter_BuildRequest_UpstreamPassthrough(t *testing.T) {
	a := &CodexSessionAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Bearer caller-pre-formatted-token",
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	// upstream 模式下完整 Authorization 值保留
	if got := req.Header.Get("Authorization"); got != "Bearer caller-pre-formatted-token" {
		t.Errorf("upstream passthrough 应保留完整 Authorization 值: got=%q", got)
	}
}

// ── BuildRequest UA / OAI-Device-Id / OAI-Language 注入 ─────────────────

func TestCodexSessionAdapter_BuildRequest_Headers(t *testing.T) {
	// caller 提供自定义 UA 与 Device-Id
	a := &CodexSessionAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "sb-tok",
			Extra: map[string]string{
				"user_agent":         "custom-codex-agent/2.0",
				"oai_device_id":      "my-device-id",
				"chatgpt_account_id": "acct-header",
				"account_id":         "acct-fallback",
				"codex_version":      "0.99.0",
				"originator":         "custom_originator",
			},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("User-Agent"); got != "custom-codex-agent/2.0" {
		t.Errorf("User-Agent=%q want custom-codex-agent/2.0", got)
	}
	if got := req.Header.Get("OAI-Device-Id"); got != "my-device-id" {
		t.Errorf("OAI-Device-Id=%q want my-device-id", got)
	}
	if got := req.Header.Get("OAI-Language"); got != "en-US" {
		t.Errorf("OAI-Language=%q want en-US", got)
	}
	if got := req.Header.Get("originator"); got != "custom_originator" {
		t.Errorf("originator=%q want custom_originator", got)
	}
	if got := req.Header.Get("chatgpt-account-id"); got != "acct-header" {
		t.Errorf("chatgpt-account-id=%q want acct-header", got)
	}
	if got := req.Header.Get("version"); got != "0.99.0" {
		t.Errorf("version=%q want 0.99.0", got)
	}
}

func TestCodexSessionAdapter_BuildRequest_AccountIDFallbackHeader(t *testing.T) {
	a := &CodexSessionAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "sb-tok",
			Extra: map[string]string{"account_id": "acct-fallback"},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("chatgpt-account-id"); got != "acct-fallback" {
		t.Fatalf("chatgpt-account-id=%q want acct-fallback", got)
	}
}

func TestCodexSessionAdapter_BuildRequest_DefaultUA(t *testing.T) {
	// caller 未提供 user_agent，应用默认 Codex CLI 风格 UA
	a := &CodexSessionAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "sb-tok",
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("User-Agent"); got == "" {
		t.Errorf("未设置默认 User-Agent")
	}
	// 默认 UA 应包含 codex 标识
	if !strings.Contains(req.Header.Get("User-Agent"), "codex") {
		t.Errorf("默认 User-Agent 应含 codex 标识: got=%q", req.Header.Get("User-Agent"))
	}
}

// ── BuildRequest 必填校验：空 session token ──────────────────────────────

func TestCodexSessionAdapter_BuildRequest_RejectEmptySessionToken(t *testing.T) {
	a := &CodexSessionAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "", // 空 token
		},
	}
	_, err := a.BuildRequest(context.Background(), in)
	if err == nil {
		t.Fatal("空 session token 应被 reject")
	}
	if !strings.Contains(err.Error(), "凭据 Value 为空") {
		t.Errorf("error 文案不对: %v", err)
	}
}

// ── BuildRequest 必填校验：空 UpstreamModelID ────────────────────────────

func TestCodexSessionAdapter_BuildRequest_RejectEmptyUpstreamModelID(t *testing.T) {
	a := &CodexSessionAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "", // 空 model slug
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "sb-tok",
		},
	}
	_, err := a.BuildRequest(context.Background(), in)
	if err == nil {
		t.Fatal("空 UpstreamModelID 应被 reject")
	}
	if !strings.Contains(err.Error(), "UpstreamModelID 为空") {
		t.Errorf("error 文案不对: %v", err)
	}
}

// ── BuildRequest 拒绝 apikey credential ──────────────────────────────────

func TestCodexSessionAdapter_BuildRequest_RejectAPIKey(t *testing.T) {
	a := &CodexSessionAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-fake-should-be-rejected",
		},
	}
	_, err := a.BuildRequest(context.Background(), in)
	if err == nil {
		t.Fatal("apikey 凭据应被 reject")
	}
	// 错误信息应明确指向 PassthroughAdapter
	if !strings.Contains(err.Error(), "PassthroughAdapter") {
		t.Errorf("reject apikey error 应提示走 PassthroughAdapter: %v", err)
	}
}

// ── BuildRequest 拒绝其它不支持凭据形态 ──────────────────────────────────

func TestCodexSessionAdapter_BuildRequest_RejectOAuthToken(t *testing.T) {
	a := &CodexSessionAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeOAuthAccessToken,
			Value: "oauth-tok",
		},
	}
	_, err := a.BuildRequest(context.Background(), in)
	if err == nil {
		t.Fatal("OAuth access token 应被 reject")
	}
	if !strings.Contains(err.Error(), "不支持的凭据形态") {
		t.Errorf("error 文案不对: %v", err)
	}
}

// ── Extra 扩展 header 透传（cookie / arkose_token 等） ───────────────────

func TestCodexSessionAdapter_BuildRequest_ExtraHeaders(t *testing.T) {
	a := &CodexSessionAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "sb-tok",
			Extra: map[string]string{
				"cookie":          "__Secure-next-auth.session-token=abc",
				"arkose_token":    "arkose-fake-token",
				"chat_session_id": "chat-sess-123",
				"oai_country":     "US",
			},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Cookie"); got != "__Secure-next-auth.session-token=abc" {
		t.Errorf("Cookie=%q", got)
	}
	if got := req.Header.Get("OpenAI-Sentinel-Arkose-Token"); got != "arkose-fake-token" {
		t.Errorf("OpenAI-Sentinel-Arkose-Token=%q", got)
	}
	if got := req.Header.Get("X-Chat-Session-Id"); got != "chat-sess-123" {
		t.Errorf("X-Chat-Session-Id=%q", got)
	}
	if got := req.Header.Get("OAI-Country"); got != "US" {
		t.Errorf("OAI-Country=%q", got)
	}
}

func decodeJSONBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("body JSON 解析失败: %v; raw=%s", err, raw)
	}
	return out
}
