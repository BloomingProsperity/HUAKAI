// 包 gemini — Google Gemini API 的出站请求适配器。
//
// 边界声明（Owner 2026-05-06 directive）：
//
//	本文件实现 Gemini API key 直通（用 operator 持有的 generativelanguage
//	API key），不是 Gemini Advanced 个人订阅反转。Gemini Advanced 反转
//	形态（OAuth session 重包装）走单独 OAuthSessionAdapter，待做。
//
// Gemini API key 模式有两种鉴权位置：
//   - query: ?key=API_KEY  （新 generative API 默认）
//   - header: x-goog-api-key: API_KEY  （Cloud / Vertex 兼容）
//
// 本 adapter 默认走 header；Credential.Extra["auth_in_query"]="true" 时
// 改走 query。
package gemini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// 默认 generativelanguage v1beta endpoint 模板。{model} 由 caller 传入。
const defaultGenerateEndpointTemplate = "https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent"

// streamGenerateEndpointTemplate 是流式 SSE endpoint 模板。
const streamGenerateEndpointTemplate = "https://generativelanguage.googleapis.com/v1beta/models/{model}:streamGenerateContent"

// PassthroughAdapter 把客户原始 Gemini 形态请求直通到 generativelanguage
// 官方 endpoint。
type PassthroughAdapter struct {
	// EndpointTemplate 覆盖默认 endpoint 模板（含 {model} 占位符）。
	EndpointTemplate string
	// StreamEndpointTemplate 流式 SSE 路径模板（同样含 {model}）。
	StreamEndpointTemplate string
}

// Platform 返回平台标识。
func (a *PassthroughAdapter) Platform() string {
	return "gemini"
}

// AcceptableCredentialTypes 仅 apikey 与 upstream_passthrough。
func (a *PassthroughAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeAPIKey,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 构造出站请求。
//
// UpstreamModelID 必须非空，会被插入到 endpoint 模板的 {model} 占位符。
// Credential.Extra["stream"]="true" 时走流式 endpoint，否则非流式。
func (a *PassthroughAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("gemini passthrough: 不支持的凭据形态 %q", in.Credential.Type)
	}
	if in.Credential.Value == "" {
		return nil, errors.New("gemini passthrough: 凭据 Value 为空")
	}
	if in.UpstreamModelID == "" {
		return nil, errors.New("gemini passthrough: UpstreamModelID 不能为空（endpoint 需要 model 名）")
	}

	// 流式信号优先级:Extra["stream"] 显式值(gemini ingress 注入)>
	// ClientStreamIntent(跨协议 ingress 的 resolved 意图)。marshal 出的
	// gemini body 无顶层 stream 字段,无此兜底 openai/anthropic 客户端的
	// 流式请求会错选非流 :generateContent。
	streamSignal := in.Credential.Extra["stream"] == "true" ||
		(in.Credential.Extra["stream"] == "" && in.ClientStreamIntent)
	defaultEndpoint := a.endpointFor(streamSignal)
	if endpointPath := strings.TrimSpace(in.EndpointPath); endpointPath != "" {
		if strings.HasPrefix(endpointPath, "http://") || strings.HasPrefix(endpointPath, "https://") {
			defaultEndpoint = endpointPath
		} else {
			if !strings.HasPrefix(endpointPath, "/") {
				endpointPath = "/" + endpointPath
			}
			defaultEndpoint = "https://generativelanguage.googleapis.com" + endpointPath
		}
	}
	// 先替换 {model} 占位再走 EndpointForCredential。 否则
	// EndpointForCredential 内的 url.Parse 把 "{" "}" 转 "%7B" "%7D",
	// 后续 strings.ReplaceAll 找不到 "{model}" 子串, model 占位永远换不掉。
	// model ID 用 path escape 防 URL 保留字符断 routing。
	substituted := strings.ReplaceAll(defaultEndpoint, "{model}", url.PathEscape(in.UpstreamModelID))
	// API key 或 upstream_passthrough 凭据自带 base_url 时优先使用。
	endpoint, err := provider.EndpointForCredential(substituted, in.Credential)
	if err != nil {
		return nil, fmt.Errorf("gemini passthrough: endpoint rejected: %w", err)
	}

	// API key 在 query 还是 header
	if in.Credential.Extra["auth_in_query"] == "true" {
		sep := "?"
		if strings.Contains(endpoint, "?") {
			sep = "&"
		}
		// API key 用 query escape 防 reserved 字符破坏 query
		endpoint = endpoint + sep + "key=" + url.QueryEscape(in.Credential.Value)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("gemini passthrough: 构造请求失败: %w", err)
	}

	if in.Credential.Extra["auth_in_query"] != "true" {
		switch in.Credential.Type {
		case provider.CredentialTypeAPIKey:
			req.Header.Set("X-Goog-Api-Key", in.Credential.Value)
		case provider.CredentialTypeUpstreamPassthrough:
			if authHeader := in.Credential.Extra["auth_header"]; authHeader != "" {
				req.Header.Set(authHeader, in.Credential.Value)
			} else {
				req.Header.Set("X-Goog-Api-Key", in.Credential.Value)
			}
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// 可选：X-Goog-User-Project（用于 Cloud quota / billing 归属）
	if proj := in.Credential.Extra["goog_user_project"]; proj != "" {
		req.Header.Set("X-Goog-User-Project", proj)
	}

	return req, nil
}

func (a *PassthroughAdapter) endpointFor(stream bool) string {
	if stream {
		if a.StreamEndpointTemplate != "" {
			return a.StreamEndpointTemplate
		}
		return streamGenerateEndpointTemplate
	}
	if a.EndpointTemplate != "" {
		return a.EndpointTemplate
	}
	return defaultGenerateEndpointTemplate
}

func (a *PassthroughAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, ok := range a.AcceptableCredentialTypes() {
		if ok == t {
			return true
		}
	}
	return false
}
