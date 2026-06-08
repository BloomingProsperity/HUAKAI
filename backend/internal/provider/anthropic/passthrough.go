// 包 anthropic — Anthropic 平台的出站请求适配器。
//
// 边界声明（Owner 2026-05-06 directive）：
//
//	本文件仅实现 Anthropic 官方 **API key 直通**（用 operator 合法持有
//	的 sk-ant-api03-... 开发者 key 转发到 api.anthropic.com）。这是公开
//	API 路径，不是 sub2api 那种"Pro/Max OAuth 反转"形态。
//	Pro/Max 反转 (R3 transport mimicry / R7 应用层伪装 / claude_token_provider
//	等价物) 已 paused — 见 docs/process/plans/2026-05-06-r3-transport-mimicry-claude.md。
package anthropic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// 默认 Anthropic 官方 Messages endpoint。
const defaultMessagesEndpoint = "https://api.anthropic.com/v1/messages"

// 默认 anthropic-version 头值。Owner 可通过 Credential.Extra
// ["anthropic_version"] 覆盖；空值时用此默认。
const defaultAnthropicVersion = "2023-06-01"

// PassthroughAdapter 实现 provider.Adapter，把客户原始 Anthropic Messages
// 形态请求直通转发到 Anthropic 官方 endpoint，注入 x-api-key 与 anthropic-
// version header。
type PassthroughAdapter struct {
	// Endpoint 覆盖默认 endpoint。空串走官方 v1/messages。
	Endpoint string
}

// Platform 返回平台标识。
func (a *PassthroughAdapter) Platform() string {
	return "anthropic"
}

// AcceptableCredentialTypes 仅 apikey 与 upstream_passthrough。OAuth 路径
// 由独立的反转 adapter 实现（已暂停）。
func (a *PassthroughAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeAPIKey,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 构造出站请求。
func (a *PassthroughAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("anthropic passthrough: 不支持的凭据形态 %q", in.Credential.Type)
	}
	if in.Credential.Value == "" {
		return nil, errors.New("anthropic passthrough: 凭据 Value 为空")
	}

	endpoint := a.Endpoint
	if endpoint == "" {
		endpoint = defaultMessagesEndpoint
	}

	// upstream_passthrough 凭据自带 base_url 优先用之。
	endpoint, err := provider.EndpointForCredential(endpoint, in.Credential)
	if err != nil {
		return nil, fmt.Errorf("anthropic passthrough: endpoint rejected: %w", err)
	}
	if strings.TrimSpace(in.Credential.Extra["claude_beta_query"]) == "true" {
		endpoint, err = provider.EndpointWithQueryParamIfMissing(endpoint, "beta", "true")
		if err != nil {
			return nil, fmt.Errorf("anthropic passthrough: endpoint rejected: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("anthropic passthrough: 构造请求失败: %w", err)
	}

	// Anthropic 用 x-api-key（不是 Authorization Bearer）
	switch in.Credential.Type {
	case provider.CredentialTypeAPIKey:
		req.Header.Set("X-API-Key", in.Credential.Value)
	case provider.CredentialTypeUpstreamPassthrough:
		// upstream 模式下完整 header value 由 caller 控制
		// （可能是 X-API-Key 或 Authorization 形态）
		if authHeader := in.Credential.Extra["auth_header"]; authHeader != "" {
			req.Header.Set(authHeader, in.Credential.Value)
		} else {
			req.Header.Set("X-API-Key", in.Credential.Value)
		}
	}

	// anthropic-version 必填
	version := in.Credential.Extra["anthropic_version"]
	if version == "" {
		version = defaultAnthropicVersion
	}
	req.Header.Set("Anthropic-Version", version)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	applyClaudeDeviceProfile(req.Header)

	// 可选 anthropic-beta（如 prompt-caching / computer-use 等 beta 特性）
	if betas := in.Credential.Extra["anthropic_beta"]; betas != "" {
		req.Header.Set("Anthropic-Beta", betas)
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

// Claude Code (Anthropic CLI) device-profile defaults — make the egress carry the
// genuine Claude Code client signature so upstream sees the real client, not a bare
// relay. Parity with CLIProxyAPI internal/runtime/executor/helps/claude_device_profile.go.
// ON per Owner 2026-06-08「必须开着」(overrides CB-001 default-off).
const (
	claudeCodeUserAgent           = "claude-cli/2.1.63 (external, cli)"
	claudeStainlessPackageVersion = "0.74.0"
	claudeStainlessRuntimeVersion = "v24.3.0"
	claudeStainlessOS             = "MacOS"
	claudeStainlessArch           = "arm64"
)

func applyClaudeDeviceProfile(h http.Header) {
	h.Set("User-Agent", claudeCodeUserAgent)
	h.Set("X-Stainless-Package-Version", claudeStainlessPackageVersion)
	h.Set("X-Stainless-Runtime-Version", claudeStainlessRuntimeVersion)
	h.Set("X-Stainless-Os", claudeStainlessOS)
	h.Set("X-Stainless-Arch", claudeStainlessArch)
}
