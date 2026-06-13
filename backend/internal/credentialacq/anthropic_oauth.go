package credentialacq

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

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const (
	claudeAIOAuthAuthURL          = "https://claude.ai/oauth/authorize"
	claudeAIOAuthTokenURL         = "https://api.anthropic.com/v1/oauth/token"
	claudeAIOAuthPublicClientID   = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeAIOAuthScope            = "org:create_api_key user:profile user:inference"
	claudeAIOAuthLoopbackRedirect = "http://localhost:54545/callback"
	claudeAIOAuthLoopbackPath     = "/callback"

	claudeAIOAuthApprovedProfileSource = "approved_builtin_profile"
)

type claudeAIOAuthExchanger struct {
	now        func() time.Time
	httpClient *http.Client
}

func newClaudeAIOAuthExchanger() claudeAIOAuthExchanger {
	return claudeAIOAuthExchanger{}
}

// NewClaudeAIOAuthExchangerWithClient 返回带显式 HTTP client 的 exchanger,
// 用于 wiring 时注入 anthropicoauth mimicry uTLS transport (HUAKAI 反封禁
// 核心差异化 [[project_core_trust_chain_differentiator]] +
// [[project_huakai_codex_mimicry_verified]])。client=nil 时退到
// http.DefaultClient — 仅用于测试 / 静态分析路径, 不应进生产 wiring。
func NewClaudeAIOAuthExchangerWithClient(client *http.Client) Exchanger {
	return claudeAIOAuthExchanger{httpClient: client}
}

// IsClaudeAIOAuthExchangerWithExplicitClient 返 true 当且仅当传入 exchanger
// 是 claudeAIOAuthExchanger 且已注入非 nil HTTP client。供 wiring fail-loud
// 自检使用 — 生产启动时调用, 防止 install 调用被删 / helper 退化导致
// mimicry transport 失效却 silent 通过。
func IsClaudeAIOAuthExchangerWithExplicitClient(exc Exchanger) bool {
	e, ok := exc.(claudeAIOAuthExchanger)
	if !ok {
		return false
	}
	return e.httpClient != nil
}

func (e claudeAIOAuthExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	cfg = builtinProfileConfig(cfg)
	if err := validateBuiltinProfile(cfg); err != nil {
		return OAuthStartResult{}, err
	}
	in.Vendor = credentialstore.VendorAnthropic
	in.AuthMode = credentialstore.AuthModeClaudeAIOAuth
	return startStoredPKCEOAuthFlow(ctx, store, in, cfg)
}

func (e claudeAIOAuthExchanger) ExchangeOAuthCode(context.Context, Session, string) (CredentialCandidate, error) {
	return CredentialCandidate{}, fmt.Errorf("%w: anthropic claude_ai_oauth requires stored PKCE verifier", ErrOAuthExchangerMissing)
}

func (e claudeAIOAuthExchanger) ExchangeOAuthCodeWithStore(ctx context.Context, store *PostgresSessionStore, session Session, _ string, code string) (CredentialCandidate, error) {
	if store == nil {
		return CredentialCandidate{}, errors.New("credentialacq: session store not configured")
	}
	payload, err := decryptStoredPKCEPayload(ctx, store, session)
	if err != nil {
		return CredentialCandidate{}, err
	}
	cfg := builtinProfileConfig(OAuthClientConfig{
		ClientID:    payload.ClientID,
		TokenURL:    payload.TokenURL,
		RedirectURI: payload.RedirectURI,
		Scopes:      append([]string(nil), payload.Scopes...),
		Source:      ClientSourcePublicCLI,
	})
	if err := validateBuiltinProfile(cfg); err != nil {
		return CredentialCandidate{}, err
	}
	payload.ClientID = cfg.ClientID
	payload.TokenURL = cfg.TokenURL
	payload.RedirectURI = cfg.RedirectURI
	payload.Scopes = append([]string(nil), cfg.Scopes...)

	token, err := e.exchangeAuthorizationCodeJSON(ctx, payload, code)
	if err != nil {
		return CredentialCandidate{}, err
	}
	raw, err := e.claudeAIOAuthTokenPayload(token, payload)
	if err != nil {
		return CredentialCandidate{}, err
	}
	fields, _, err := parseFakeTokenPayload(string(raw))
	if err != nil {
		return CredentialCandidate{}, err
	}
	if err := validateTokenShape(fields, TokenShapeAccessRefresh); err != nil {
		return CredentialCandidate{}, err
	}
	candidate := CredentialCandidate{
		TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
		Vendor: session.Vendor, AuthMode: session.AuthMode, Payload: raw, ActorID: session.ActorID,
		RedactedContext: map[string]any{"client_identity_source": claudeAIOAuthApprovedProfileSource},
	}
	// Auto-capture the upstream Anthropic account identity carried inline in the
	// token-exchange response body (account.uuid / account.email_address); an empty
	// uuid falls back to manual binding and never blocks acquisition. This is the
	// live-path twin of the chatgpt/codex/gemini id_token seams so all four vendors
	// capture upstream account metadata at acquisition.
	AttachIdentity(&candidate, accountident.ExtractAnthropic(token.Account.UUID, token.Account.EmailAddress, token.Email))
	return candidate, nil
}

func builtinProfileConfig(override OAuthClientConfig) OAuthClientConfig {
	cfg := OAuthClientConfig{
		ClientID:    claudeAIOAuthPublicClientID,
		AuthURL:     claudeAIOAuthAuthURL,
		TokenURL:    claudeAIOAuthTokenURL,
		RedirectURI: claudeAIOAuthLoopbackRedirect,
		Scopes:      strings.Fields(claudeAIOAuthScope),
		Source:      ClientSourcePublicCLI,
	}
	if strings.TrimSpace(override.ClientID) != "" {
		cfg.ClientID = strings.TrimSpace(override.ClientID)
	}
	if strings.TrimSpace(override.ClientSecret) != "" {
		cfg.ClientSecret = strings.TrimSpace(override.ClientSecret)
	}
	if strings.TrimSpace(override.AuthURL) != "" {
		cfg.AuthURL = strings.TrimSpace(override.AuthURL)
	}
	if strings.TrimSpace(override.TokenURL) != "" {
		cfg.TokenURL = strings.TrimSpace(override.TokenURL)
	}
	if strings.TrimSpace(override.RedirectURI) != "" {
		cfg.RedirectURI = strings.TrimSpace(override.RedirectURI)
	}
	if len(override.Scopes) > 0 {
		cfg.Scopes = append([]string(nil), override.Scopes...)
	}
	if strings.TrimSpace(override.Source) != "" {
		cfg.Source = strings.TrimSpace(override.Source)
	}
	if override.HTTPClient != nil {
		cfg.HTTPClient = override.HTTPClient
	}
	return cfg
}

func validateBuiltinProfile(cfg OAuthClientConfig) error {
	var mismatches []string
	if strings.TrimSpace(cfg.ClientID) != claudeAIOAuthPublicClientID {
		mismatches = append(mismatches, "client_id")
	}
	if strings.TrimSpace(cfg.ClientSecret) != "" {
		mismatches = append(mismatches, "client_secret")
	}
	if strings.TrimSpace(cfg.AuthURL) != claudeAIOAuthAuthURL {
		mismatches = append(mismatches, "auth_url")
	}
	if strings.TrimSpace(cfg.TokenURL) != claudeAIOAuthTokenURL {
		mismatches = append(mismatches, "token_url")
	}
	// redirect_uri 此前只查非空,致使管理员 override 的任意 redirect(含攻击者 https host)能进
	// authorize URL 接走授权码。改为严格 loopback 校验,与 gemini/chatgpt 的 loopback 分支等强(不达标即
	// 作 profile mismatch 拒绝)。claude_ai_oauth 是 claude.ai 固定 public client、只注册 loopback redirect,
	// claude.ai 不会接受非 loopback;且 HTTPS admin server callback 还需把 flow_id 注入 redirect(本模式走
	// 通用 startStoredPKCEOAuthFlow,未注入),否则回调缺 flow_id 必被 admin handler 拒(400)。故此处一律拒
	// 非 loopback —— 不放出一条无法完成的 admin 回调路径。HTTPS admin allowlist 对齐留 roadmap。
	if err := validateClaudeAIRedirectURI(cfg.RedirectURI); err != nil {
		mismatches = append(mismatches, "redirect_uri")
	}
	if strings.Join(trimmedFields(cfg.Scopes), " ") != claudeAIOAuthScope {
		mismatches = append(mismatches, "scope")
	}
	if source := strings.TrimSpace(cfg.Source); source != "" && source != ClientSourcePublicCLI {
		mismatches = append(mismatches, "source")
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("%w: anthropic claude_ai_oauth built-in profile mismatch: %s", ErrFeatureDisabled, strings.Join(mismatches, ","))
	}
	return nil
}

// validateClaudeAIRedirectURI 严格校验 claude_ai_oauth 的 redirect_uri:仅接受 localhost loopback
// (无 userinfo、显式端口 [1024,65535]、path 恰为 /callback)。claude.ai public client 只注册 loopback,
// 非 loopback(含任意 https host)一律拒绝 —— 闭合 "redirect 只查非空,override 任意目标可接走授权码"。
func validateClaudeAIRedirectURI(raw string) error {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("%w: invalid redirect_uri: %v", ErrFeatureDisabled, err)
	}
	if parsed.Scheme != "http" {
		return fmt.Errorf("%w: redirect must be http loopback (claude.ai public client registers loopback only)", ErrFeatureDisabled)
	}
	if parsed.User != nil {
		return fmt.Errorf("%w: loopback redirect must not include userinfo", ErrFeatureDisabled)
	}
	if parsed.Hostname() != "localhost" {
		return fmt.Errorf("%w: loopback redirect host must be localhost", ErrFeatureDisabled)
	}
	portRaw := parsed.Port()
	if portRaw == "" {
		return fmt.Errorf("%w: loopback redirect requires explicit port", ErrFeatureDisabled)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil || port < 1024 || port > 65535 {
		return fmt.Errorf("%w: loopback redirect port out of range", ErrFeatureDisabled)
	}
	if parsed.EscapedPath() != claudeAIOAuthLoopbackPath {
		return fmt.Errorf("%w: loopback redirect path must be %s", ErrFeatureDisabled, claudeAIOAuthLoopbackPath)
	}
	return nil
}

func (e claudeAIOAuthExchanger) exchangeAuthorizationCodeJSON(ctx context.Context, payload storedPKCEPayload, code string) (oauthTokenResponse, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: authorization code is empty", ErrInvalidTokenShape)
	}
	body, err := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  strings.TrimSpace(payload.RedirectURI),
		"client_id":     claudeAIOAuthPublicClientID,
		"code_verifier": strings.TrimSpace(payload.CodeVerifier),
	})
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeAIOAuthTokenURL, bytes.NewReader(body))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client().Do(req)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return oauthTokenResponse{}, fmt.Errorf("credentialacq: anthropic oauth token endpoint returned status %d: %s", resp.StatusCode, oauthErrorSummary(respBody))
	}
	var token oauthTokenResponse
	if err := json.Unmarshal(respBody, &token); err != nil {
		return oauthTokenResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: anthropic oauth token response missing access token", ErrInvalidTokenShape)
	}
	if strings.TrimSpace(token.RefreshToken) == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: anthropic oauth token response missing refresh token", ErrInvalidTokenShape)
	}
	return token, nil
}

func (e claudeAIOAuthExchanger) claudeAIOAuthTokenPayload(token oauthTokenResponse, stored storedPKCEPayload) ([]byte, error) {
	raw, err := tokenCandidatePayload(token, stored, e.nowTime())
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	out["client_identity_source"] = claudeAIOAuthApprovedProfileSource
	out["client_id_source"] = claudeAIOAuthApprovedProfileSource
	return json.Marshal(out)
}

func (e claudeAIOAuthExchanger) client() *http.Client {
	if e.httpClient != nil {
		return e.httpClient
	}
	return http.DefaultClient
}

func (e claudeAIOAuthExchanger) nowTime() time.Time {
	if e.now != nil {
		return e.now().UTC()
	}
	return time.Now().UTC()
}

func trimmedFields(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func oauthErrorSummary(raw []byte) string {
	var decoded struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &decoded); err == nil {
		switch {
		case decoded.Error != "" && decoded.ErrorDescription != "":
			return decoded.Error + ": " + decoded.ErrorDescription
		case decoded.Error != "":
			return decoded.Error
		case decoded.ErrorDescription != "":
			return decoded.ErrorDescription
		}
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "empty response body"
	}
	return text
}
