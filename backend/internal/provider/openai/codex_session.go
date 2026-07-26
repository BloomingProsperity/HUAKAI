// 包 openai — OpenAI Codex CLI / ChatGPT Plus 网页 session 反转适配器。
//
// CodexSessionAdapter 区别于 PassthroughAdapter：
//   - 目标 endpoint 是 chatgpt.com 自有 backend（非官方 api.openai.com）
//   - 凭据形态是 session token（sb-xxxxx cookie / Bearer）或 upstream_passthrough
//   - 不支持普通开发者 API key（apikey 走 PassthroughAdapter）
//   - Body 由 caller 负责组装成 chatgpt.com 形态；adapter 仅注入 Auth + 必要 header
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// defaultCodexEndpoint 默认目标 endpoint：Codex Responses 接口。
// chatgpt.com 自有 backend，非 api.openai.com。
const defaultCodexEndpoint = "https://chatgpt.com/backend-api/codex/responses"

// defaultCodexUserAgent Codex CLI 风格默认 User-Agent。
// caller 可通过 Credential.Extra["user_agent"] 覆盖。
const defaultCodexUserAgent = "codex/1.0.0 (linux; go)"

// Codex Responses live 已坐实会拒绝这些常见采样/输出字段；顶层 stop 也会被拒。
var codexResponsesLiveUnsupportedFields = [...]string{
	"temperature",
	"top_p",
	"max_output_tokens",
	"stop",
}

// Codex Responses 上游不支持的字段,仅在 openai_codex session adapter 出站前剥离。
var codexResponsesAlignedUnsupportedFields = [...]string{
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

// 编译期接口合规性断言：CodexSessionAdapter 必须满足 provider.Adapter。
var _ provider.Adapter = (*CodexSessionAdapter)(nil)

// CodexSessionAdapter 实现 provider.Adapter，把 ChatGPT Plus 网页 session
// 或 Codex CLI session 反转成 API 形态。
//
// 与 PassthroughAdapter 的核心区别：
//   - 仅接受 session_token / upstream_passthrough 凭据
//   - 目标是 chatgpt.com backend，不是 api.openai.com
//   - 注入 Codex CLI / ChatGPT 必要 header（UA / OAI-Device-Id / OAI-Language）
//   - Body shape 由 caller 负责；adapter 透传不重塑
type CodexSessionAdapter struct {
	// Endpoint 覆盖默认 chatgpt.com endpoint。空串走
	// "https://chatgpt.com/backend-api/codex/responses"。
	Endpoint string
}

// Platform 返回平台标识，用于 admin trace + audit 渲染。
func (a *CodexSessionAdapter) Platform() string {
	return "openai_codex"
}

// AcceptableCredentialTypes 返回本 adapter 支持的凭据形态。
// 仅接受 session_token（ChatGPT/Codex CLI 登录后的 token）与
// upstream_passthrough（caller 已完整格式化 Authorization header）。
// 普通开发者 apikey 明确拒绝，应走 PassthroughAdapter。
func (a *CodexSessionAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 构造出站请求。返回的 *http.Request 可直接被 http.Client.Do
// 或 transport.RoundTripper 消费。
//
// 必填校验：
//   - in.Credential.Value 非空（session token 不能为空）
//   - in.UpstreamModelID 非空（对应 chatgpt.com 的 default_model_slug）
//
// Header 注入规则：
//   - Authorization: Bearer <session_token>（upstream_passthrough 模式下完整透传）
//   - User-Agent: Extra["user_agent"] 优先；空时用默认 Codex CLI 风格 UA
//   - OAI-Device-Id: Extra["oai_device_id"]（非空时注入）
//   - OAI-Language: 固定 "en-US"
//   - 其它 Extra key 通过下方扩展字段透传（cookie / arkose_token / chat_session_id 等）
func (a *CodexSessionAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	// 凭据形态校验：apikey 明确拒绝，其它不支持形态统一报错
	if in.Credential.Type == provider.CredentialTypeAPIKey {
		return nil, errors.New("openai codex session: apikey 凭据应走 PassthroughAdapter，本 adapter 仅接受 session_token / upstream_passthrough")
	}
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("openai codex session: 不支持的凭据形态 %q", in.Credential.Type)
	}

	// session token 不能为空（含纯空白）
	if strings.TrimSpace(in.Credential.Value) == "" {
		return nil, errors.New("openai codex session: 凭据 Value 为空或仅含空白（session token 必填）")
	}

	method := strings.ToUpper(strings.TrimSpace(in.HTTPMethod))
	if method == "" {
		method = http.MethodPost
	}
	if method != http.MethodPost && method != http.MethodGet {
		return nil, fmt.Errorf("openai codex session: 不支持的请求方法 %q", method)
	}
	discoveryRequest := method == http.MethodGet && strings.TrimSpace(in.EndpointPath) != ""

	// 生成请求必须有真实模型；账号目录请求没有模型字段。
	if !discoveryRequest && strings.TrimSpace(in.UpstreamModelID) == "" {
		return nil, errors.New("openai codex session: UpstreamModelID 为空（对应 chatgpt.com default_model_slug，必填）")
	}

	endpoint := strings.TrimSpace(a.Endpoint)
	if endpoint == "" {
		endpoint = defaultCodexEndpoint
	}
	if baseURL := strings.TrimSpace(in.Credential.Extra["base_url"]); baseURL != "" {
		validatedEndpoint, err := provider.ValidateCustomEndpointURL(baseURL)
		if err != nil {
			return nil, fmt.Errorf("openai codex session: base_url 非法: %w", err)
		}
		endpoint = validatedEndpoint
	}
	var err error
	// EndpointPath 只允许账号目录 GET 覆盖。图片公开入口虽然也携带
	// /v1/images/* 路径，但必须转换后发往 Codex Responses，不能把公开路径
	// 误当成真实上游地址。
	if discoveryRequest {
		endpoint, err = provider.EndpointForBuildInput(endpoint, in)
		if err != nil {
			return nil, fmt.Errorf("openai codex session: endpoint 非法: %w", err)
		}
	}

	var body []byte
	if discoveryRequest {
		body = nil
	} else if isCodexImagesEndpoint(in.EndpointPath) {
		body, err = buildCodexImagesResponsesBody(in)
	} else {
		body, err = normalizeCodexResponsesBody(in.InboundBody)
	}
	if err != nil {
		return nil, fmt.Errorf("openai codex session: 请求体规整失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai codex session: 构造请求失败: %w", err)
	}

	// Authorization header 注入
	switch in.Credential.Type {
	case provider.CredentialTypeSessionToken:
		// session token 以 Bearer 形态注入
		req.Header.Set("Authorization", "Bearer "+in.Credential.Value)
	case provider.CredentialTypeUpstreamPassthrough:
		// upstream 模式下 Value 即 caller 已格式化的完整 Authorization header 值
		req.Header.Set("Authorization", in.Credential.Value)
	}

	// Content-Type / Accept 标准头。Codex Responses 只接受流式响应。
	if discoveryRequest {
		req.Header.Set("Accept", "application/json")
	} else {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
	}

	// User-Agent：优先使用导入时保存的完整值；只有真实版本时按同一份版本
	// 构造 CLI UA，避免 version 头已更新但 UA 仍停在旧兜底。
	ua := strings.TrimSpace(in.Credential.Extra["user_agent"])
	version := strings.TrimSpace(in.Credential.Extra["codex_version"])
	if ua == "" && version != "" {
		ua = "codex_cli_rs/" + version
	}
	if ua == "" {
		ua = defaultCodexUserAgent
	}
	// 浏览器型 UA(Mozilla/...)不能泄给上游，检出即改写回客户端风格 UA。
	// 未来此 fallback 可换成 admin 可调的 platformsettings 值。
	if isBrowserUserAgent(ua) {
		ua = defaultCodexUserAgent
	}
	req.Header.Set("User-Agent", ua)

	// OAI-Device-Id：设备唯一 ID，Codex CLI / ChatGPT 风控必要字段
	if deviceID := in.Credential.Extra["oai_device_id"]; deviceID != "" {
		req.Header.Set("OAI-Device-Id", deviceID)
	}

	// OAI-Language：固定 en-US，与 Codex CLI 默认行为一致
	req.Header.Set("OAI-Language", "en-US")

	originator := strings.TrimSpace(in.Credential.Extra["originator"])
	if originator == "" {
		originator = "codex_cli_rs"
	}
	req.Header.Set("originator", originator)

	if accountID := firstNonEmptyCodexExtra(in.Credential.Extra, "chatgpt_account_id", "account_id"); accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
	if version != "" {
		req.Header.Set("version", version)
	}

	// 扩展透传 header：caller 通过 Extra 传入的其它 chatgpt.com 必要字段
	// 支持 key：cookie / arkose_token / chat_session_id / oai_country
	if cookie := in.Credential.Extra["cookie"]; cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if arkose := in.Credential.Extra["arkose_token"]; arkose != "" {
		req.Header.Set("OpenAI-Sentinel-Arkose-Token", arkose)
	}
	if chatSessionID := in.Credential.Extra["chat_session_id"]; chatSessionID != "" {
		req.Header.Set("X-Chat-Session-Id", chatSessionID)
	}
	if country := in.Credential.Extra["oai_country"]; country != "" {
		req.Header.Set("OAI-Country", country)
	}

	return req, nil
}

func normalizeCodexResponsesBody(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw, nil
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, errors.New("请求体必须是 JSON object")
	}
	body["stream"] = json.RawMessage("true")
	body["store"] = json.RawMessage("false")
	if err := normalizeCodexResponsesInput(body); err != nil {
		return nil, err
	}
	for _, field := range codexResponsesLiveUnsupportedFields {
		delete(body, field)
	}
	for _, field := range codexResponsesAlignedUnsupportedFields {
		delete(body, field)
	}
	return json.Marshal(body)
}

func normalizeCodexResponsesInput(body map[string]json.RawMessage) error {
	raw, exists := body["input"]
	if !exists {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil
	}
	normalized, err := json.Marshal([]any{map[string]any{
		"type": "message",
		"role": "user",
		"content": []any{map[string]any{
			"type": "input_text",
			"text": text,
		}},
	}})
	if err != nil {
		return err
	}
	body["input"] = normalized
	return nil
}

func firstNonEmptyCodexExtra(extra map[string]string, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(extra[key]); v != "" {
			return v
		}
	}
	return ""
}

// acceptsCredential 检查凭据形态是否在 AcceptableCredentialTypes 列表中。
func (a *CodexSessionAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, ok := range a.AcceptableCredentialTypes() {
		if ok == t {
			return true
		}
	}
	return false
}
