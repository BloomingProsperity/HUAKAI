// 包 dify — Dify 应用 API 的出站请求适配器。
//
// Dify 鉴权是 per-app token（Authorization: Bearer <app-token>）；同一实例下
// 每个 app 的 bot 类型决定会话走哪个端点：
//   - ""(默认) / "chatflow" / "agent" → POST {base}/v1/chat-messages
//   - "workflow"                      → POST {base}/v1/workflows/run
//   - "completion"                    → POST {base}/v1/completion-messages
//
// bot 类型由凭据 Extra["bot_type"] 携带（账号侧配置）；base 默认
// https://api.dify.ai，自托管实例可由 API key 或 upstream_passthrough
// 凭据的 base_url 覆盖。
package dify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// defaultBaseURL 是 Dify 云端默认 API base。
const defaultBaseURL = "https://api.dify.ai"

// Adapter 把已 marshal 的 Dify 请求 body 发往对应端点。
type Adapter struct {
	// Endpoint 覆盖默认 API base（scheme+host[:port]），供 httptest 注入。
	// 空值保持 https://api.dify.ai。
	Endpoint string
}

// Platform 返回平台标识。
func (a *Adapter) Platform() string {
	return "dify"
}

// AcceptableCredentialTypes 仅 apikey 与 upstream_passthrough。
func (a *Adapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeAPIKey,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 构造出站请求。in.InboundBody 此时已是 dify_chat marshal 产物
// （response_mode 等流式语义在 body 内，本 adapter 不再 reshape）。
func (a *Adapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("dify passthrough: 不支持的凭据形态 %q", in.Credential.Type)
	}
	if in.Credential.Value == "" {
		return nil, errors.New("dify passthrough: 凭据 Value 为空")
	}

	path, err := endpointPathForBotType(in.Credential.Extra["bot_type"])
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(strings.TrimSpace(a.Endpoint), "/")
	if base == "" {
		base = defaultBaseURL
	}
	// EndpointForBuildInput 统一处理 in.EndpointPath 覆盖、两类凭据的 base_url
	// 选择与 SSRF 守卫；adapter 不得自行拼私有 endpoint 绕过守卫。
	endpoint, err := provider.EndpointForBuildInput(base+path, in)
	if err != nil {
		return nil, fmt.Errorf("dify passthrough: endpoint rejected: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("dify passthrough: 构造请求失败: %w", err)
	}

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
	req.Header.Set("Content-Type", "application/json")
	if in.Credential.Extra["stream"] == "true" {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	return req, nil
}

// endpointPathForBotType 把账号侧 bot_type 映射到端点 path；非法值 fail-loud，
// 不允许猜端点把请求发错 app 形态。
//
// v1 仅放行 chat 形态(""/chatflow/agent → /v1/chat-messages)。workflow 与
// completion 的端点虽已知(/v1/workflows/run、/v1/completion-messages),但
// 请求 body 形态不同(workflows/run 只认 inputs 变量表、无顶层 query;
// completion-messages 的提问在 inputs.query),且 workflow 流式输出载体是
// text_chunk/workflow_finished 而非 message/message_end——当前 marshal 与
// SSE 解析都只产/只认 chat 形,放行等于"声明支持但用户内容到不了 app、
// 响应永远为空"。在 inputs 投影与 workflow 事件解析落地前 fail-closed。
func endpointPathForBotType(botType string) (string, error) {
	switch strings.TrimSpace(botType) {
	case "", "chatflow", "agent":
		return "/v1/chat-messages", nil
	case "workflow", "completion":
		return "", fmt.Errorf("dify passthrough: bot_type %q 暂未支持(请求/响应形态投影未落地,fail-closed 防静默空响应)", botType)
	default:
		return "", fmt.Errorf("dify passthrough: 未知 bot_type %q", botType)
	}
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
