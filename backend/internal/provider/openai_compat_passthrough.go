package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
)

// OpenAICompatPassthroughAdapter 把 OpenAI Chat Completions 形态的请求转发到
// 各平台特定的兼容 endpoint。
type OpenAICompatPassthroughAdapter struct {
	PlatformName string
	Endpoint     string
}

func (a *OpenAICompatPassthroughAdapter) Platform() string { return a.PlatformName }

func (a *OpenAICompatPassthroughAdapter) AcceptableCredentialTypes() []CredentialType {
	return []CredentialType{
		CredentialTypeAPIKey,
		CredentialTypeUpstreamPassthrough,
	}
}

func (a *OpenAICompatPassthroughAdapter) BuildRequest(ctx context.Context, in BuildInput) (*http.Request, error) {
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("%s passthrough: 不支持的凭据形态 %q", a.PlatformName, in.Credential.Type)
	}
	if in.Credential.Value == "" {
		return nil, errors.New(a.PlatformName + " passthrough: 凭据 Value 为空")
	}

	endpoint := a.Endpoint
	endpoint, err := EndpointForBuildInput(endpoint, in)
	if err != nil {
		return nil, fmt.Errorf("%s passthrough: endpoint rejected: %w", a.PlatformName, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("%s passthrough: 构造请求失败: %w", a.PlatformName, err)
	}

	switch in.Credential.Type {
	case CredentialTypeAPIKey:
		req.Header.Set("Authorization", "Bearer "+in.Credential.Value)
	case CredentialTypeUpstreamPassthrough:
		req.Header.Set("Authorization", in.Credential.Value)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	return req, nil
}

func (a *OpenAICompatPassthroughAdapter) acceptsCredential(t CredentialType) bool {
	for _, ok := range a.AcceptableCredentialTypes() {
		if ok == t {
			return true
		}
	}
	return false
}
