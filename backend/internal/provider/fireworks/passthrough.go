// 包 fireworks — Fireworks AI 平台的出站请求适配器。
//
// Fireworks AI 走 OpenAI Chat Completions 兼容协议；endpoint 为
// api.fireworks.ai/inference/v1/chat/completions。鉴权用 Fireworks AI
// 官方 API key（Bearer header）。
package fireworks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// 默认 Fireworks AI 官方 Chat Completions endpoint。
const defaultChatCompletionsEndpoint = "https://api.fireworks.ai/inference/v1/chat/completions"

// PassthroughAdapter 把客户原始 OpenAI 形态请求直通转发到 Fireworks AI。
type PassthroughAdapter struct {
	// Endpoint 覆盖默认 endpoint（如自托管 OpenAI-compatible 代理前置）。
	Endpoint string
}

func (a *PassthroughAdapter) Platform() string { return "fireworks" }

func (a *PassthroughAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeAPIKey,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 构造出站请求。
func (a *PassthroughAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("fireworks passthrough: 不支持的凭据形态 %q", in.Credential.Type)
	}
	if in.Credential.Value == "" {
		return nil, errors.New("fireworks passthrough: 凭据 Value 为空")
	}

	endpoint := a.Endpoint
	if endpoint == "" {
		endpoint = defaultChatCompletionsEndpoint
	}

	// upstream_passthrough 凭据自带 base_url 优先用之。
	endpoint, err := provider.EndpointForCredential(endpoint, in.Credential)
	if err != nil {
		return nil, fmt.Errorf("fireworks passthrough: endpoint rejected: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("fireworks passthrough: 构造请求失败: %w", err)
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
