package credentialacq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/google/uuid"
)

// codex_web_oauth 是 Codex CLI 浏览器侧 authorization-code(PKCE)登录路径,与
// openai/codex_cli_oauth 的 device-code 获取路径并列。两者共享同一个 OpenAI 公开
// CLI client profile(client_id / authorize / token / scope / loopback redirect),
// 但绑定到各自独立的 auth_mode mode key,由单-exchanger-per-key 的 dispatch 干净解析:
// device-code 走 device_authorization 端点 + 轮询;web 路径构造浏览器 authorize URL
// 再用回调 code 换 token。本 exchanger 复用 chatgpt_oauth 已落地的 OpenAI built-in
// profile / redirect 校验与 PKCE / session-store 机制,差别仅在绑定的 auth_mode 与
// 凭据上打的 client_identity_source 标签。
const codexWebApprovedProfileSource = "approved_builtin_profile_codex_web_oauth"

type codexWebOAuthExchanger struct {
	now                         func() time.Time
	httpClient                  *http.Client
	httpsAdminCallbackAllowlist []string
}

func newCodexWebOAuthExchanger() codexWebOAuthExchanger {
	return codexWebOAuthExchanger{}
}

// NewCodexWebOAuthExchangerWithClientAndAdminCallbackAllowlist 返回带静态 admin
// callback allowlist 的 codex web exchanger。allowlist 必须来自 operator 配置或
// 测试注入,不能来自 OAuth 启动请求体。
func NewCodexWebOAuthExchangerWithClientAndAdminCallbackAllowlist(client *http.Client, allowlist []string) Exchanger {
	return codexWebOAuthExchanger{
		httpClient:                  client,
		httpsAdminCallbackAllowlist: cloneTrimmedStrings(allowlist),
	}
}

// IsCodexWebOAuthExchangerWithExplicitClient 只在 codex web exchanger 已注入非 nil
// HTTP client 时返回 true,供启动自检防止受控 transport 被绕过。
func IsCodexWebOAuthExchangerWithExplicitClient(exc Exchanger) bool {
	e, ok := exc.(codexWebOAuthExchanger)
	if !ok {
		return false
	}
	return e.httpClient != nil
}

func (e codexWebOAuthExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	cfg = chatgptBuiltinProfileConfig(cfg)
	if err := e.validateBuiltinProfile(cfg); err != nil {
		return OAuthStartResult{}, err
	}
	flowID := strings.TrimSpace(in.ID)
	if flowID == "" {
		flowID = uuid.NewString()
	}
	cfg.RedirectURI = chatgptRedirectURIWithFlowID(cfg.RedirectURI, flowID)
	in.ID = flowID
	in.RedirectURI = cfg.RedirectURI
	in.Vendor = credentialstore.VendorOpenAI
	in.AuthMode = credentialstore.AuthModeCodexWebOAuth
	start, err := startStoredPKCEOAuthFlow(ctx, store, in, cfg)
	if err != nil {
		return OAuthStartResult{}, err
	}
	start.AuthorizeURL = buildCodexAuthorizeURL(cfg, start.State, start.CodeChallenge, start.Session.ID)
	return start, nil
}

func (e codexWebOAuthExchanger) ExchangeOAuthCode(context.Context, Session, string) (CredentialCandidate, error) {
	return CredentialCandidate{}, fmt.Errorf("%w: openai codex_web_oauth requires stored PKCE verifier", ErrOAuthExchangerMissing)
}

func (e codexWebOAuthExchanger) ExchangeOAuthCodeWithStore(ctx context.Context, store *PostgresSessionStore, session Session, _ string, code string) (CredentialCandidate, error) {
	if store == nil {
		return CredentialCandidate{}, errors.New("credentialacq: session store not configured")
	}
	payload, err := decryptStoredPKCEPayload(ctx, store, session)
	if err != nil {
		return CredentialCandidate{}, err
	}
	cfg := OAuthClientConfig{
		ClientID:     payload.ClientID,
		ClientSecret: payload.ClientSecret,
		AuthURL:      chatgptOAuthAuthURL,
		TokenURL:     payload.TokenURL,
		RedirectURI:  payload.RedirectURI,
		Scopes:       append([]string(nil), payload.Scopes...),
		Source:       ClientSourcePublicCLI,
	}
	if err := e.validateBuiltinProfile(cfg); err != nil {
		return CredentialCandidate{}, err
	}
	payload.ClientID = chatgptOAuthClientID
	payload.TokenURL = chatgptOAuthTokenURL
	payload.RedirectURI = strings.TrimSpace(cfg.RedirectURI)
	payload.Scopes = strings.Fields(chatgptOAuthScope)

	token, err := e.exchangeAuthorizationCodeForm(ctx, payload, code)
	if err != nil {
		return CredentialCandidate{}, err
	}
	raw, err := e.codexWebOAuthTokenPayload(token, payload)
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
		RedactedContext: map[string]any{"client_identity_source": codexWebApprovedProfileSource},
	}
	// Auto-extract the upstream account identity from the id_token JWT (sub claim),
	// mirroring chatgpt_oauth; codex web tokens share the ChatGPT JWT shape. Empty
	// id_token -> empty Identity -> AttachIdentity no-ops (defensive, never blocks).
	AttachIdentity(&candidate, accountident.ExtractChatGPT(token.IDToken, ""))
	return candidate, nil
}

// buildCodexAuthorizeURL 构造 Codex CLI 浏览器登录的 authorize URL。除标准 PKCE
// 参数外,带 Codex 特定参数 prompt=login / id_token_add_organizations=true /
// codex_cli_simplified_flow=true —— 这些是 codex login 浏览器路径必须的,缺一项
// 真实 OpenAI 端不会返回预期的 organization-aware code / 简化流。
func buildCodexAuthorizeURL(cfg OAuthClientConfig, state, codeChallenge, flowID string) string {
	u, err := url.Parse(chatgptOAuthAuthURL)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", chatgptOAuthClientID)
	q.Set("redirect_uri", chatgptRedirectURIWithFlowID(cfg.RedirectURI, flowID))
	q.Set("scope", chatgptOAuthScope)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	q.Set("prompt", "login")
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	u.RawQuery = q.Encode()
	return u.String()
}

func (e codexWebOAuthExchanger) validateBuiltinProfile(cfg OAuthClientConfig) error {
	return validateChatGPTBuiltinProfileWithHTTPSAdminAllowlist(cfg, e.httpsAdminCallbackAllowlist)
}

func (e codexWebOAuthExchanger) exchangeAuthorizationCodeForm(ctx context.Context, payload storedPKCEPayload, code string) (oauthTokenResponse, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: authorization code is empty", ErrInvalidTokenShape)
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", strings.TrimSpace(payload.RedirectURI))
	form.Set("client_id", chatgptOAuthClientID)
	form.Set("code_verifier", strings.TrimSpace(payload.CodeVerifier))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatgptOAuthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := e.client().Do(req)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return oauthTokenResponse{}, fmt.Errorf("credentialacq: codex web oauth token endpoint returned status %d: %s", resp.StatusCode, oauthErrorSummary(body))
	}
	var token oauthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return oauthTokenResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: codex web oauth response missing access_token", ErrInvalidTokenShape)
	}
	if strings.TrimSpace(token.RefreshToken) == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: codex web oauth response missing refresh_token (offline_access scope expected)", ErrInvalidTokenShape)
	}
	return token, nil
}

func (e codexWebOAuthExchanger) codexWebOAuthTokenPayload(token oauthTokenResponse, stored storedPKCEPayload) ([]byte, error) {
	raw, err := tokenCandidatePayload(token, stored, e.nowTime())
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	out["client_identity_source"] = codexWebApprovedProfileSource
	out["client_id_source"] = codexWebApprovedProfileSource
	out["auth_mode"] = credentialstore.AuthModeCodexWebOAuth
	return json.Marshal(out)
}

func (e codexWebOAuthExchanger) client() *http.Client {
	if e.httpClient != nil {
		return e.httpClient
	}
	return auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
}

func (e codexWebOAuthExchanger) nowTime() time.Time {
	if e.now != nil {
		return e.now().UTC()
	}
	return time.Now().UTC()
}
