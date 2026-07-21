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
	"net/netip"
	"net/url"
	"strconv"
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

	// UpstreamModelID 对应 chatgpt.com 的 default_model_slug，不能为空（含纯空白）
	if strings.TrimSpace(in.UpstreamModelID) == "" {
		return nil, errors.New("openai codex session: UpstreamModelID 为空（对应 chatgpt.com default_model_slug，必填）")
	}

	endpoint := strings.TrimSpace(a.Endpoint)
	if endpoint == "" {
		endpoint = defaultCodexEndpoint
	}
	if baseURL := strings.TrimSpace(in.Credential.Extra["base_url"]); baseURL != "" {
		validatedEndpoint, err := validateCodexSessionEndpoint(baseURL)
		if err != nil {
			return nil, fmt.Errorf("openai codex session: base_url 非法: %w", err)
		}
		endpoint = validatedEndpoint
	}

	body, err := normalizeCodexResponsesBody(in.InboundBody)
	if err != nil {
		return nil, fmt.Errorf("openai codex session: 请求体规整失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
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
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

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

func validateCodexSessionEndpoint(raw string) (string, error) {
	if hasCodexEndpointControlOrSpace(raw) {
		return "", errors.New("包含控制字符或空白字符")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("URL 无效或 host 为空")
	}
	if u.User != nil {
		return "", errors.New("不允许 userinfo")
	}
	if u.Fragment != "" {
		return "", errors.New("不允许 fragment")
	}
	if strings.Contains(u.Host, "%") {
		return "", errors.New("不允许编码 host")
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("端口无效")
	}
	if port := u.Port(); port != "" && !validCodexEndpointPort(port) {
		return "", errors.New("端口无效")
	}

	host, isLoopback, err := classifyCodexSessionEndpointHost(u.Hostname())
	if err != nil {
		return "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !isLoopback {
			return "", errors.New("http 仅允许本机测试 endpoint")
		}
	default:
		return "", errors.New("scheme 必须是 https")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = replaceCodexEndpointHostCanonical(u.Host, host)
	return u.String(), nil
}

func classifyCodexSessionEndpointHost(raw string) (string, bool, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	if host == "" {
		return "", false, errors.New("host 为空")
	}
	if strings.Contains(host, "%") {
		return "", false, errors.New("不允许编码 host")
	}
	if host[len(host)-1] == '.' {
		return "", false, errors.New("不允许 trailing-dot host")
	}
	if hasCodexEndpointNonASCII(host) {
		return "", false, errors.New("不允许非 ASCII host")
	}
	if host == "localhost" {
		return host, true, nil
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		if addr.IsLoopback() {
			return addr.String(), true, nil
		}
		if !publicCodexEndpointIP(addr) {
			return "", false, errors.New("拒绝内网、链路本地、metadata 或特殊用途 IP")
		}
		return addr.String(), false, nil
	}
	if blockedCodexEndpointHost(host) || numericObfuscatedCodexEndpointHost(host) {
		return "", false, errors.New("拒绝内网、metadata 或特殊用途 host")
	}
	return host, false, nil
}

func replaceCodexEndpointHostCanonical(hostport, host string) string {
	if port := endpointPortFromHostPort(hostport); port != "" {
		if strings.Contains(host, ":") {
			return "[" + host + "]:" + port
		}
		return host + ":" + port
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func endpointPortFromHostPort(hostport string) string {
	if i := strings.LastIndex(hostport, ":"); i >= 0 && i < len(hostport)-1 && !strings.Contains(hostport[i+1:], "]") {
		if _, err := strconv.Atoi(hostport[i+1:]); err == nil {
			return hostport[i+1:]
		}
	}
	return ""
}

func hasCodexEndpointControlOrSpace(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] == 0x7f {
			return true
		}
	}
	return false
}

func hasCodexEndpointNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

func validCodexEndpointPort(raw string) bool {
	port, err := strconv.Atoi(raw)
	return err == nil && port > 0 && port <= 65535
}

func publicCodexEndpointIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() ||
		!addr.IsGlobalUnicast() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified() {
		return false
	}
	for _, prefix := range codexEndpointSpecialUseDenyPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

var codexEndpointSpecialUseDenyPrefixes = []netip.Prefix{
	mustCodexEndpointPrefix("0.0.0.0/8"),
	mustCodexEndpointPrefix("100.64.0.0/10"),
	mustCodexEndpointPrefix("192.0.0.0/24"),
	mustCodexEndpointPrefix("192.0.2.0/24"),
	mustCodexEndpointPrefix("192.88.99.0/24"),
	mustCodexEndpointPrefix("198.18.0.0/15"),
	mustCodexEndpointPrefix("198.51.100.0/24"),
	mustCodexEndpointPrefix("203.0.113.0/24"),
	mustCodexEndpointPrefix("240.0.0.0/4"),
	mustCodexEndpointPrefix("255.255.255.255/32"),
	mustCodexEndpointPrefix("::/96"),
	mustCodexEndpointPrefix("64:ff9b::/96"),
	mustCodexEndpointPrefix("64:ff9b:1::/48"),
	mustCodexEndpointPrefix("100::/64"),
	mustCodexEndpointPrefix("2001::/23"),
	mustCodexEndpointPrefix("2001:db8::/32"),
	mustCodexEndpointPrefix("2002::/16"),
	mustCodexEndpointPrefix("3fff::/20"),
	mustCodexEndpointPrefix("5f00::/16"),
}

func mustCodexEndpointPrefix(raw string) netip.Prefix {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		panic(err)
	}
	return prefix
}

func blockedCodexEndpointHost(host string) bool {
	switch host {
	case "metadata",
		"metadata.google.internal",
		"metadata.goog",
		"instance-data",
		"instance-data.ec2.internal",
		"169.254.169.254",
		"localhost.localdomain":
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".home", ".corp", ".intranet"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func numericObfuscatedCodexEndpointHost(host string) bool {
	labels := strings.Split(host, ".")
	if len(labels) == 0 || len(labels) > 4 {
		return false
	}
	for _, label := range labels {
		if label == "" {
			return false
		}
		switch {
		case strings.HasPrefix(label, "0x") || strings.HasPrefix(label, "0X"):
			if _, err := strconv.ParseUint(label[2:], 16, 32); err != nil {
				return false
			}
		case len(label) > 1 && label[0] == '0':
			if _, err := strconv.ParseUint(label, 8, 32); err != nil {
				return false
			}
		default:
			if _, err := strconv.ParseUint(label, 10, 32); err != nil {
				return false
			}
		}
	}
	return true
}
