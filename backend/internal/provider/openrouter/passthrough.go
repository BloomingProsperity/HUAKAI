// 包 openrouter — OpenRouter 平台的出站请求适配器。
//
// OpenRouter 兼容 OpenAI Chat Completions 协议；HUAKAI 把它视作 "OpenAI 同
// shape，不同 endpoint + 自有 X-Title / HTTP-Referer header 约定" 的平台。
package openrouter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

const defaultChatCompletionsEndpoint = "https://openrouter.ai/api/v1/chat/completions"

// PassthroughAdapter 把客户原始 OpenAI 形态请求直通转发到 OpenRouter。
type PassthroughAdapter struct {
	// Endpoint 覆盖默认 endpoint。
	Endpoint string
}

func (a *PassthroughAdapter) Platform() string { return "openrouter" }

func (a *PassthroughAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeAPIKey,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 构造出站请求。
//
// OpenRouter 推荐 caller 注入 HTTP-Referer 和 X-Title 用于排行榜归属：
//   - Credential.Extra["http_referer"] → HTTP-Referer
//   - Credential.Extra["x_title"]      → X-Title
//
// 这两个 header 完全可选；不影响功能。
func (a *PassthroughAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("openrouter passthrough: 不支持的凭据形态 %q", in.Credential.Type)
	}
	if in.Credential.Value == "" {
		return nil, errors.New("openrouter passthrough: 凭据 Value 为空")
	}

	endpoint := a.Endpoint
	if endpoint == "" {
		endpoint = defaultChatCompletionsEndpoint
	}

	// upstream_passthrough 凭据自带 base_url 优先用之。
	endpoint = provider.EndpointForCredential(endpoint, in.Credential)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("openrouter passthrough: 构造请求失败: %w", err)
	}

	switch in.Credential.Type {
	case provider.CredentialTypeAPIKey:
		req.Header.Set("Authorization", "Bearer "+in.Credential.Value)
	case provider.CredentialTypeUpstreamPassthrough:
		req.Header.Set("Authorization", in.Credential.Value)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if ref := in.Credential.Extra["http_referer"]; ref != "" {
		req.Header.Set("HTTP-Referer", ref)
	}
	if title := in.Credential.Extra["x_title"]; title != "" {
		req.Header.Set("X-Title", title)
	}

	return req, nil
}

func (a *PassthroughAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, ok := range a.AcceptableCredentialTypes() {
		if ok == t {
			return true
		}
	}
	return false
}
