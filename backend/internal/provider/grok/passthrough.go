// 包 grok — xAI Grok 平台的出站请求适配器。
//
// Grok 走 OpenAI Chat Completions 兼容协议；与 OpenAI / OpenRouter 同款
// shape，仅 endpoint + Authorization Bearer 形态。鉴权用 xai-... 开发者
// API key（Bearer header）。
package grok

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// 默认 Grok 官方 Chat Completions endpoint。
const defaultChatCompletionsEndpoint = "https://api.x.ai/v1/chat/completions"

// PassthroughAdapter 把客户原始 OpenAI 形态请求直通转发到 Grok。
type PassthroughAdapter struct {
	// Endpoint 覆盖默认 endpoint（如自托管 OpenAI-compatible 代理前置）。
	Endpoint string
}

func (a *PassthroughAdapter) Platform() string { return "grok" }

func (a *PassthroughAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeAPIKey,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 构造出站请求。
func (a *PassthroughAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("grok passthrough: 不支持的凭据形态 %q", in.Credential.Type)
	}
	if in.Credential.Value == "" {
		return nil, errors.New("grok passthrough: 凭据 Value 为空")
	}

	endpoint := a.Endpoint
	if endpoint == "" {
		endpoint = defaultChatCompletionsEndpoint
	}

	// upstream_passthrough 凭据自带 base_url 优先用之。
	endpoint, err := provider.EndpointForCredential(endpoint, in.Credential)
	if err != nil {
		return nil, fmt.Errorf("grok passthrough: endpoint rejected: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("grok passthrough: 构造请求失败: %w", err)
	}

	switch in.Credential.Type {
	case provider.CredentialTypeAPIKey:
		req.Header.Set("Authorization", "Bearer "+in.Credential.Value)
	case provider.CredentialTypeUpstreamPassthrough:
		req.Header.Set("Authorization", in.Credential.Value)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

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
