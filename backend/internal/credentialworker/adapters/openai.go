package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

const (
	defaultOpenAITokenEndpoint = "https://auth.openai.com/oauth/token"
	defaultRefreshTimeout      = 5 * time.Second

	ChatGPTOAuthTokenEndpoint = "https://auth.openai.com/oauth/token"
	ChatGPTOAuthClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
)

// OpenAIRefresh 用标准 refresh_token grant 刷新 OpenAI / ChatGPT 账号。
type OpenAIRefresh struct {
	Endpoint               string
	ClientID               string
	Scope                  string
	HTTPClient             *http.Client
	PrivacyMutationEnabled bool
}

func (r OpenAIRefresh) RefreshForProvider(ctx context.Context, accountID int64, providerName string, currentCredential []byte) ([]byte, time.Time, error) {
	cred, err := parseCredential(currentCredential)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("openai refresh account %d: %w", accountID, err)
	}
	refreshToken := credentialString(cred, "refresh_token")
	if refreshToken == "" {
		return nil, time.Time{}, fmt.Errorf("openai refresh account %d: refresh_token is empty", accountID)
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	if clientID := firstNonEmpty(r.ClientID, credentialString(cred, "client_id")); clientID != "" {
		form.Set("client_id", clientID)
	}
	if clientSecret := credentialString(cred, "client_secret"); clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	if scope := firstNonEmpty(r.Scope, credentialString(cred, "scope")); scope != "" {
		form.Set("scope", scope)
	}

	// 出站 token 端点钉死:只取 adapter 显式配置的 r.Endpoint(operator-config,如 codex)或内置默认,
	// **绝不**采信 credential payload 里的 oauth_token_endpoint——否则攻击者写入的内网/metadata 地址
	// 会被 refresh worker 当作 token 端点,把 refresh_token+client_secret 外带(SSRF)。
	resp, err := postTokenWithRetry(ctx, r.httpClient(), firstNonEmpty(r.Endpoint, defaultOpenAITokenEndpoint), "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("openai refresh account %d: %w", accountID, err)
	}
	newCredential, expiresAt, err := mergeTokenResponse(cred, resp)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("openai refresh account %d: %w", accountID, err)
	}
	newCredential, err = applyOpenAIRefreshMetadata(newCredential, r.PrivacyMutationEnabled)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("openai refresh account %d: %w", accountID, err)
	}
	return newCredential, expiresAt, nil
}

func (r OpenAIRefresh) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	// 纵深:未显式注入 client 时回退到 SSRF 保护客户端(拒 loopback/私网/link-local/metadata + 禁
	// 重定向),与 ChatGPTRefresh 一致;裸 http.DefaultClient 会让出站 token 请求对任意地址无防护。
	return auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
}

func applyOpenAIRefreshMetadata(raw []byte, privacyMutationEnabled bool) ([]byte, error) {
	cred, err := parseCredential(raw)
	if err != nil {
		return nil, err
	}
	cred["account_check_outcome"] = "not_configured"
	if privacyMutationEnabled {
		cred["privacy_action_outcome"] = "pending_operator_policy"
	} else {
		cred["privacy_action_outcome"] = "disabled"
	}
	return json.Marshal(cred)
}

// ChatGPTRefresh 用 OpenAI ChatGPT public CLI OAuth refresh_token grant 续期。
// Endpoint / ClientID 默认钉死为内置 public profile；credential JSON 中的
// oauth_token_endpoint / client_id / client_secret / scope 一律不参与出站请求。
type ChatGPTRefresh struct {
	Endpoint               string
	ClientID               string
	HTTPClient             *http.Client
	PrivacyMutationEnabled bool
}

func (r ChatGPTRefresh) RefreshForProvider(ctx context.Context, accountID int64, providerName string, currentCredential []byte) ([]byte, time.Time, error) {
	cred, err := parseCredential(currentCredential)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("chatgpt refresh account %d: %w", accountID, err)
	}
	refreshToken := credentialString(cred, "refresh_token")
	if refreshToken == "" {
		return nil, time.Time{}, fmt.Errorf("chatgpt refresh account %d: refresh_token is empty", accountID)
	}
	endpoint := firstNonEmpty(r.Endpoint, ChatGPTOAuthTokenEndpoint)
	clientID := firstNonEmpty(r.ClientID, ChatGPTOAuthClientID)
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	resp, err := postTokenWithRetry(ctx, r.httpClient(), endpoint, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("chatgpt refresh account %d: %w", accountID, err)
	}
	// ChatGPT acquisition 会把 access_token 双写到 session_token；refresh 也必须覆盖旧值。
	cred["session_token"] = resp.AccessToken
	newCredential, expiresAt, err := mergeTokenResponse(cred, resp)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("chatgpt refresh account %d: %w", accountID, err)
	}
	newCredential, err = applyOpenAIRefreshMetadata(newCredential, r.PrivacyMutationEnabled)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("chatgpt refresh account %d: %w", accountID, err)
	}
	return newCredential, expiresAt, nil
}

func (r ChatGPTRefresh) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int64  `json:"expires_in"`
	Scope            string `json:"scope"`
	ChatGPTPlanType  string `json:"chatgpt_plan_type"`
	ChatGPTUserID    string `json:"chatgpt_user_id"`
	ChatGPTAccountID string `json:"chatgpt_account_id"`
}

func parseCredential(raw []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("credential json is empty")
	}
	var cred map[string]any
	if err := json.Unmarshal(raw, &cred); err != nil {
		return nil, fmt.Errorf("credential json invalid: %w", err)
	}
	return cred, nil
}

func credentialString(cred map[string]any, key string) string {
	switch v := cred[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strings.TrimSpace(strconv.FormatFloat(v, 'f', -1, 64))
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func postTokenWithRetry(ctx context.Context, client *http.Client, endpoint, contentType string, body io.Reader) (tokenResponse, error) {
	var payload []byte
	if body != nil {
		data, err := io.ReadAll(body)
		if err != nil {
			return tokenResponse{}, err
		}
		payload = data
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		reqCtx, cancel := context.WithTimeout(ctx, defaultRefreshTimeout)
		resp, err := postTokenOnce(reqCtx, client, endpoint, contentType, payload)
		cancel()
		if err == nil {
			return resp, nil
		}
		last = err
		if !isRetryableTokenError(err) {
			break
		}
	}
	return tokenResponse{}, last
}

type tokenHTTPError struct {
	status     int
	body       string
	retryAfter time.Duration
}

func (e tokenHTTPError) RetryableRefresh() bool {
	return e.status >= http.StatusInternalServerError && e.status <= 599
}

func (e tokenHTTPError) NextRefreshAttempt(now time.Time) time.Time {
	if e.retryAfter <= 0 {
		return time.Time{}
	}
	return now.Add(e.retryAfter)
}

func (e tokenHTTPError) Error() string {
	// ANT-3: 把上游响应体一并暴露,让上层 credentialworker.ClassifyRefreshError
	// 通过子串匹配 (例如 invalid_grant) 正确分类为 auth_expired,而不是
	// 仅看到 "status 401" 落到 temporary。Body 截到 1KB 防日志膨胀。
	if e.body != "" {
		return fmt.Sprintf("oauth token endpoint returned status %d: %s", e.status, e.body)
	}
	return fmt.Sprintf("oauth token endpoint returned status %d", e.status)
}

func postTokenOnce(ctx context.Context, client *http.Client, endpoint, contentType string, payload []byte) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	resp, err := client.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return tokenResponse{}, tokenHTTPError{
			status:     resp.StatusCode,
			body:       strings.TrimSpace(string(body)),
			retryAfter: oauthRetryAfter(resp.StatusCode, resp.Header, time.Now().UTC()),
		}
	}
	var out tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return tokenResponse{}, fmt.Errorf("oauth token response invalid: %w", err)
	}
	return out, nil
}

func isRetryableTokenError(err error) bool {
	var httpErr tokenHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.status >= http.StatusInternalServerError && httpErr.status <= 599
	}
	return true
}

func oauthRetryAfter(status int, header http.Header, now time.Time) time.Duration {
	if status != http.StatusTooManyRequests {
		return 0
	}
	delay := time.Minute
	if raw := strings.TrimSpace(header.Get("Retry-After")); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
			delay = time.Duration(seconds) * time.Second
		} else if when, err := http.ParseTime(raw); err == nil {
			delay = when.Sub(now)
		}
	}
	if delay <= 0 {
		delay = time.Minute
	}
	if delay > time.Hour {
		delay = time.Hour
	}
	return delay
}

func mergeTokenResponse(cred map[string]any, resp tokenResponse) ([]byte, time.Time, error) {
	if strings.TrimSpace(resp.AccessToken) == "" {
		return nil, time.Time{}, errors.New("oauth token response missing access_token")
	}
	ttl := resp.ExpiresIn
	if ttl <= 0 {
		ttl = 3600
	}
	expiresAt := time.Now().UTC().Add(time.Duration(ttl) * time.Second)
	cred["access_token"] = resp.AccessToken
	cred["expires_at"] = expiresAt.Format(time.RFC3339)
	if strings.TrimSpace(resp.RefreshToken) != "" {
		cred["refresh_token"] = resp.RefreshToken
	}
	if strings.TrimSpace(resp.IDToken) != "" {
		cred["id_token"] = resp.IDToken
	}
	if strings.TrimSpace(resp.TokenType) != "" {
		cred["token_type"] = resp.TokenType
	}
	if strings.TrimSpace(resp.Scope) != "" {
		cred["scope"] = resp.Scope
	}
	if strings.TrimSpace(resp.ChatGPTPlanType) != "" {
		cred["chatgpt_plan_type"] = resp.ChatGPTPlanType
	}
	if strings.TrimSpace(resp.ChatGPTUserID) != "" {
		cred["chatgpt_user_id"] = resp.ChatGPTUserID
	}
	if strings.TrimSpace(resp.ChatGPTAccountID) != "" {
		cred["chatgpt_account_id"] = resp.ChatGPTAccountID
	}
	// R2 S2 defense-in-depth (Owner): 写回 store 前
	// 主动 scrub hostile credential 字段, 防止 store 残留攻击面被未来代码
	// 路径意外读取。本轮 refresh 出站 adapter (anthropic ANT-3 c201cb4 已修)
	// 不再读这些, 但 cred 不清会让下次 refresh / future ingest path 仍可能
	// 读到 plant 值。具体清单 = 信任链 invariant 涉及字段:
	//   - oauth_token_endpoint (SSRF endpoint)
	//   - client_secret (operator-config 才有, 不该入 cred)
	//   - fallback_client_id (gemini cross-client SSRF 攻击面)
	//   - setup_token / long_lived_setup_token (anthropic 长效升级面)
	// client_id 不强 scrub — 部分 vendor 用作 audit metadata, 由各 adapter
	// fail-closed 自己处理 (anthropic 已修, openai/gemini 是 S1-D 范围)。
	delete(cred, "oauth_token_endpoint")
	delete(cred, "client_secret")
	delete(cred, "fallback_client_id")
	delete(cred, "setup_token")
	delete(cred, "long_lived_setup_token")
	newCredential, err := json.Marshal(cred)
	return newCredential, expiresAt, err
}
