// 包 ollama — Ollama 原生 /api/chat 的出站请求适配器。
//
// 形态要点：
//   - 端点：POST {base}/api/chat。默认 base http://127.0.0.1:11434 仅作占位，
//     实际部署经 channel/account base_url 覆盖到真实主机；私网/localhost 上游
//     受统一出站 SSRF 策略约束（运营可配白名单），本 adapter 不自行绕过。
//   - 鉴权：Ollama 通常无鉴权——凭据 Value 为空是合法形态，此时不发任何
//     Authorization 头（发空 Bearer 会被部分反代当非法凭据拒）；Value 非空时
//     apikey 形态注 Bearer 前缀，upstream_passthrough 形态原样透传（自带前缀，
//     header 名可由 Extra["auth_header"] 覆盖）。
//   - 响应流式 wire 是 NDJSON（application/x-ndjson），Ollama 不按 Accept
//     协商，统一 Accept: application/json，无 stream 特判分支。
package ollama

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// defaultBaseURL 是默认 API base（本机占位；运营经 base_url 覆盖）。
const defaultBaseURL = "http://127.0.0.1:11434"

// endpointPath 是原生会话端点。
const endpointPath = "/api/chat"

// Adapter 把已 marshal 的 Ollama 请求 body 发往 /api/chat。
type Adapter struct {
	// Endpoint 覆盖默认 API base（scheme+host[:port]），供 httptest 注入。
	// 空值保持 http://127.0.0.1:11434。
	Endpoint string
}

// Platform 返回平台标识（与 OpenAI 兼容直通的 ollama_chat 共享平台）。
func (a *Adapter) Platform() string {
	return "ollama"
}

// AcceptableCredentialTypes 仅 apikey 与 upstream_passthrough。
func (a *Adapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeAPIKey,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 构造出站请求。in.InboundBody 此时已是 ollama_native marshal
// 产物（stream 开关、options 采样参数都在 body 内，本 adapter 不再 reshape）。
func (a *Adapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("ollama passthrough: 不支持的凭据形态 %q", in.Credential.Type)
	}

	base := strings.TrimRight(strings.TrimSpace(a.Endpoint), "/")
	if base == "" {
		base = defaultBaseURL
	}
	// EndpointForBuildInput 统一处理 in.EndpointPath 覆盖、两类凭据的 base_url
	// 选择与 SSRF 守卫；adapter 不得自行拼私有 endpoint 绕过守卫。
	endpoint, err := provider.EndpointForBuildInput(base+endpointPath, in)
	if err != nil {
		return nil, fmt.Errorf("ollama passthrough: endpoint rejected: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("ollama passthrough: 构造请求失败: %w", err)
	}

	// Value 为空合法（无鉴权实例），不发任何鉴权头；非空才注入。
	if in.Credential.Value != "" {
		switch in.Credential.Type {
		case provider.CredentialTypeAPIKey:
			req.Header.Set("Authorization", "Bearer "+in.Credential.Value)
		case provider.CredentialTypeUpstreamPassthrough:
			// 透传凭据自带前缀；header 名可由 Extra["auth_header"] 覆盖。
			header := strings.TrimSpace(in.Credential.Extra["auth_header"])
			if header == "" {
				header = "Authorization"
			}
			req.Header.Set(header, in.Credential.Value)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (a *Adapter) acceptsCredential(t provider.CredentialType) bool {
	for _, ok := range a.AcceptableCredentialTypes() {
		if ok == t {
			return true
		}
	}
	return false
}

var _ provider.Adapter = (*Adapter)(nil)
