package anthropic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

type OAuthSessionAdapter struct {
	Endpoint string
	Now      func() time.Time
}

func (a *OAuthSessionAdapter) Platform() string {
	return "anthropic"
}

func (a *OAuthSessionAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeOAuthAccessToken,
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

func (a *OAuthSessionAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if in.Credential.Type == provider.CredentialTypeAPIKey {
		return nil, errors.New("anthropic oauth session: api key credentials must use the messages passthrough adapter")
	}
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("anthropic oauth session: unsupported credential type %q", in.Credential.Type)
	}
	if strings.TrimSpace(in.Credential.Value) == "" {
		return nil, errors.New("anthropic oauth session: credential value is empty")
	}
	if err := a.rejectExpired(in.Credential); err != nil {
		return nil, err
	}
	customEndpoint := strings.TrimSpace(a.Endpoint) != "" || provider.UsesCustomPassthroughEndpoint(in.Credential)
	endpoint := a.Endpoint
	if endpoint == "" {
		endpoint = defaultMessagesEndpoint
	}
	endpoint, err := provider.EndpointForBuildInput(endpoint, in)
	if err != nil {
		return nil, fmt.Errorf("anthropic oauth session: endpoint rejected: %w", err)
	}
	method := strings.ToUpper(strings.TrimSpace(in.HTTPMethod))
	if method == "" {
		method = http.MethodPost
	}
	if method != http.MethodPost && method != http.MethodGet {
		return nil, fmt.Errorf("anthropic oauth session: unsupported method %q", method)
	}
	if method == http.MethodPost {
		endpoint, err = oauthMessagesEndpointWithBeta(endpoint, in.Credential.Extra["claude_beta_query"], customEndpoint)
		if err != nil {
			return nil, fmt.Errorf("anthropic oauth session: endpoint rejected: %w", err)
		}
	}
	var body *bytes.Reader
	if method == http.MethodGet {
		body = bytes.NewReader(nil)
	} else {
		body = bytes.NewReader(in.InboundBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("anthropic oauth session: build request: %w", err)
	}
	switch in.Credential.Type {
	case provider.CredentialTypeOAuthAccessToken, provider.CredentialTypeSessionToken:
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(in.Credential.Value))
	case provider.CredentialTypeUpstreamPassthrough:
		if header := strings.TrimSpace(in.Credential.Extra["auth_header"]); header != "" {
			req.Header.Set(header, in.Credential.Value)
		} else {
			req.Header.Set("Authorization", in.Credential.Value)
		}
	}
	version := in.Credential.Extra["anthropic_version"]
	if version == "" {
		version = defaultAnthropicVersion
	}
	req.Header.Set("Anthropic-Version", version)
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	// DM-03:客户端 beta token 只放行白名单——OAuth/session 池账号带
	// Claude Code 设备指纹,透传任意 token=指纹异常(反封禁);凭据配置
	// token 永远原样在前。
	if betas := outboundBetaHeader(in.Credential.Extra["anthropic_beta"], in.InboundBetaTokens, oauthBetaAllowed); betas != "" {
		req.Header.Set("Anthropic-Beta", betas)
	}
	// DEVPIN-02: OAuth/session 路(池账号主出口)同样要带 Claude Code 设备指纹,
	// 否则裸 relay 一眼被上游识别。复用 DEVPIN-01 的 per-account 钉定 helper。
	applyClaudeDeviceProfile(req.Header, resolveAccountDeviceProfile(in.Account.AccountID))
	stampClaudeCodeStaticHeaders(req.Header)
	applyClaudeSessionHeaders(req.Header, in.Account.AccountID)
	return req, nil
}

// oauthMessagesEndpointWithBeta 实现 session 出站的三态规则：官方默认地址缺省
// 加 beta=true，显式 false 关闭；自定义地址缺省不加，只有显式 true 才加。
// URL 合并交给公共 helper，保留已有 query 且不覆盖已有 beta 值。
func oauthMessagesEndpointWithBeta(endpoint, setting string, customEndpoint bool) (string, error) {
	switch strings.ToLower(strings.TrimSpace(setting)) {
	case "true":
		return provider.EndpointWithQueryParamIfMissing(endpoint, "beta", "true")
	case "false":
		return endpoint, nil
	case "":
		if customEndpoint {
			return endpoint, nil
		}
		return provider.EndpointWithQueryParamIfMissing(endpoint, "beta", "true")
	default:
		return "", errors.New("claude_beta_query must be true, false, or empty")
	}
}

func (a *OAuthSessionAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, ok := range a.AcceptableCredentialTypes() {
		if ok == t {
			return true
		}
	}
	return false
}

func (a *OAuthSessionAdapter) rejectExpired(cred provider.Credential) error {
	raw := strings.TrimSpace(cred.Extra["expires_at"])
	if raw == "" {
		return nil
	}
	expiresAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil
	}
	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}
	if now.After(expiresAt.UTC()) {
		return fmt.Errorf("%w: anthropic oauth access token", credentialstore.ErrCredentialExpired)
	}
	return nil
}

var _ provider.Adapter = (*OAuthSessionAdapter)(nil)
