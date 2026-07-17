package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/anthropicoauth"
)

const defaultAnthropicTokenEndpoint = "https://platform.claude.com/v1/oauth/token"

// AnthropicRefresh 用 Anthropic OAuth refresh_token grant 刷新 Claude 账号。
//
// 信任链 (ANT-3): 出站 token endpoint 与 OAuth
// client_id 仅来自 operator-supplied 字段 (r.Endpoint, r.ClientID) 或 HUAKAI
// 硬编 approved built-in profile (defaultAnthropicTokenEndpoint /
// anthropicoauth.AnthropicPublicCLIClientID); 不接受任何来自 credential
// payload 的 endpoint / client_id 覆盖, 防止凭据被篡改后把 refresh 流量
// 导到攻击者 token endpoint (SSRF / auth token 泄露)。
type AnthropicRefresh struct {
	Endpoint   string
	ClientID   string
	HTTPClient *http.Client
}

func (r AnthropicRefresh) RefreshForProvider(ctx context.Context, accountID int64, providerName string, currentCredential []byte) ([]byte, time.Time, error) {
	cred, err := parseCredential(currentCredential)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("anthropic refresh account %d: %w", accountID, err)
	}
	refreshToken := credentialString(cred, "refresh_token")
	if refreshToken == "" {
		return nil, time.Time{}, fmt.Errorf("anthropic refresh account %d: refresh_token is empty", accountID)
	}

	body := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}
	// client_id 仅来自 operator 配置或 built-in approved CLI;不取 credential
	// payload 字段 (ANT-3 D-4=B SSRF / auth-leak 防线)。
	clientID := firstNonEmpty(r.ClientID, anthropicoauth.AnthropicPublicCLIClientID)
	if clientID != "" {
		body["client_id"] = clientID
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("anthropic refresh account %d: marshal request: %w", accountID, err)
	}

	// token endpoint 同上, 仅 operator config / builtin;credential payload
	// 中的 oauth_token_endpoint 一律忽略 (ANT-3 D-4=B)。
	endpoint := firstNonEmpty(r.Endpoint, defaultAnthropicTokenEndpoint)
	resp, err := postTokenWithRetry(ctx, r.httpClient(), endpoint, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("anthropic refresh account %d: %w", accountID, err)
	}
	newCredential, expiresAt, err := mergeTokenResponse(cred, resp)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("anthropic refresh account %d: %w", accountID, err)
	}
	return newCredential, expiresAt, nil
}

func (r AnthropicRefresh) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return anthropicoauth.DefaultHTTPClient()
}
