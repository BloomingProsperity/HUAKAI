// 包 openai — OpenAI 平台的出站请求适配器。
//
// 当前 v1 实现 PassthroughAdapter：用合法持有的 OpenAI 开发者 API key
// （sk-...）直通到官方 endpoint，不做反转 / 不做应用层伪装 / 不做传输层
// 伪装。客户请求 body 已是 OpenAI Chat Completions 形态时直通；其它
// 形态由 protocol-translation 层在调用 BuildRequest 之前完成。
//
// 反转形态（ChatGPT Plus / Codex CLI session 反转）会在后续 SessionAdapter
// / CodexAdapter 文件实现，与 PassthroughAdapter 共享 BuildInput 入参，
// 但 endpoint / Auth header / body shape 都不同。
package openai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// 默认 OpenAI 官方 Chat Completions endpoint。可通过 PassthroughAdapter.Endpoint
// 字段覆盖（如自托管代理 / OpenAI-compatible 上游）。
const defaultChatCompletionsEndpoint = "https://api.openai.com/v1/chat/completions"

// PassthroughAdapter 实现 provider.Adapter，把客户原始 OpenAI 形态请求直通
// 转发到 OpenAI 官方 endpoint，仅注入 Authorization 与可选的 organization
// / project header。
type PassthroughAdapter struct {
	// Endpoint 覆盖默认 endpoint。空串走 OpenAI 官方
	// "https://api.openai.com/v1/chat/completions"。
	Endpoint string
}

// Platform 返回平台标识。
func (a *PassthroughAdapter) Platform() string {
	return "openai"
}

// AcceptableCredentialTypes 列出本 adapter 支持的凭据形态。仅 apikey
// 与 upstream_passthrough（自带 base URL 的开发者代理）。
func (a *PassthroughAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeAPIKey,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 构造出站请求。返回的 *http.Request 可直接被 http.Client.Do
// 或 transport.RoundTripper 消费。
func (a *PassthroughAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("openai passthrough: 不支持的凭据形态 %q", in.Credential.Type)
	}
	if in.Credential.Value == "" {
		return nil, errors.New("openai passthrough: 凭据 Value 为空")
	}

	defaultEndpoint := a.Endpoint
	if defaultEndpoint == "" {
		defaultEndpoint = defaultChatCompletionsEndpoint
	}
	// upstream_passthrough 凭据自带 base_url, 优先用之 (防第三方 token 发到
	// OpenAI 官方端点)。
	endpoint := provider.EndpointForCredential(defaultEndpoint, in.Credential)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("openai passthrough: 构造请求失败: %w", err)
	}

	// Authorization header
	switch in.Credential.Type {
	case provider.CredentialTypeAPIKey:
		req.Header.Set("Authorization", "Bearer "+in.Credential.Value)
	case provider.CredentialTypeUpstreamPassthrough:
		// upstream 模式下 Value 即是已格式化的 Authorization header 完整值
		req.Header.Set("Authorization", in.Credential.Value)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// 可选 vendor-specific header
	if org := in.Credential.Extra["org_id"]; org != "" {
		req.Header.Set("OpenAI-Organization", org)
	}
	if proj := in.Credential.Extra["project_id"]; proj != "" {
		req.Header.Set("OpenAI-Project", proj)
	}
	if betas := in.Credential.Extra["openai_beta"]; betas != "" {
		// 多个 beta 用逗号分隔；按 OpenAI 文档约定。
		req.Header.Set("OpenAI-Beta", betas)
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
