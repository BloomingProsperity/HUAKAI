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
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/google/uuid"
)

const (
	chatgptOAuthAuthURL          = "https://auth.openai.com/oauth/authorize"
	chatgptOAuthTokenURL         = "https://auth.openai.com/oauth/token"
	chatgptOAuthClientID         = "app_EMoamEEZ73f0CkXaXp7hrann"
	chatgptOAuthScope            = "openid email profile offline_access"
	chatgptOAuthLoopbackRedirect = "http://localhost:1455/auth/callback"
	chatgptAdminCallbackPath     = "/admin/v1/credentials/oauth-callback"

	chatgptApprovedProfileSource = "approved_builtin_profile_chatgpt_oauth"
)

type chatgptOAuthExchanger struct {
	now                         func() time.Time
	httpClient                  *http.Client
	httpsAdminCallbackAllowlist []string
}

type chatgptOAuthTokenResponse struct {
	oauthTokenResponse
	ChatGPTUserID    string `json:"chatgpt_user_id"`
	ChatGPTPlanType  string `json:"chatgpt_plan_type"`
	ChatGPTAccountID string `json:"chatgpt_account_id"`
}

func newChatGPTOAuthExchanger() chatgptOAuthExchanger {
	return chatgptOAuthExchanger{}
}

// NewChatGPTOAuthExchangerWithClient 返回带显式 HTTP client 的 ChatGPT exchanger。
// 生产 wiring 用它注入 OAuth-grade SSRF 防护 client，测试可注入 mock transport。
func NewChatGPTOAuthExchangerWithClient(client *http.Client) Exchanger {
	return chatgptOAuthExchanger{httpClient: client}
}

// IsChatGPTOAuthExchangerWithExplicitClient 只在 ChatGPT exchanger 已注入
// 非 nil HTTP client 时返回 true，供启动自检防止受控 transport 被绕过。
func IsChatGPTOAuthExchangerWithExplicitClient(exc Exchanger) bool {
	e, ok := exc.(chatgptOAuthExchanger)
	if !ok {
		return false
	}
	return e.httpClient != nil
}

// NewChatGPTOAuthExchangerWithClientAndAdminCallbackAllowlist 返回带静态
// admin callback allowlist 的 ChatGPT exchanger。allowlist 必须来自 operator
// 配置或测试注入，不能来自 OAuth 启动请求体。
func NewChatGPTOAuthExchangerWithClientAndAdminCallbackAllowlist(client *http.Client, allowlist []string) Exchanger {
	return chatgptOAuthExchanger{
		httpClient:                  client,
		httpsAdminCallbackAllowlist: cloneTrimmedStrings(allowlist),
	}
}

func (e chatgptOAuthExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
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
	in.AuthMode = credentialstore.AuthModeChatGPTOAuth
	start, err := startStoredPKCEOAuthFlow(ctx, store, in, cfg)
	if err != nil {
		return OAuthStartResult{}, err
	}
	start.AuthorizeURL = buildChatGPTAuthorizeURL(cfg, start.State, start.CodeChallenge, start.Session.ID)
	return start, nil
}

func (e chatgptOAuthExchanger) ExchangeOAuthCode(context.Context, Session, string) (CredentialCandidate, error) {
	return CredentialCandidate{}, fmt.Errorf("%w: openai chatgpt_oauth requires stored PKCE verifier", ErrOAuthExchangerMissing)
}

func (e chatgptOAuthExchanger) ExchangeOAuthCodeWithStore(ctx context.Context, store *PostgresSessionStore, session Session, _ string, code string) (CredentialCandidate, error) {
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
	raw, err := e.chatgptOAuthTokenPayload(token, payload)
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
	redacted := map[string]any{"client_identity_source": chatgptApprovedProfileSource}
	if plan := strings.TrimSpace(token.ChatGPTPlanType); plan != "" {
		redacted["chatgpt_plan_type_class"] = plan
	}
	return CredentialCandidate{
		TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
		Vendor: session.Vendor, AuthMode: session.AuthMode, Payload: raw, ActorID: session.ActorID,
		RedactedContext: redacted,
	}, nil
}

func chatgptBuiltinProfileConfig(override OAuthClientConfig) OAuthClientConfig {
	cfg := OAuthClientConfig{
		ClientID:    chatgptOAuthClientID,
		AuthURL:     chatgptOAuthAuthURL,
		TokenURL:    chatgptOAuthTokenURL,
		RedirectURI: chatgptOAuthLoopbackRedirect,
		Scopes:      strings.Fields(chatgptOAuthScope),
		Source:      ClientSourcePublicCLI,
	}
	if strings.TrimSpace(override.ClientSecret) != "" {
		cfg.ClientSecret = strings.TrimSpace(override.ClientSecret)
	}
	if strings.TrimSpace(override.RedirectURI) != "" {
		cfg.RedirectURI = strings.TrimSpace(override.RedirectURI)
	}
	if override.HTTPClient != nil {
		cfg.HTTPClient = override.HTTPClient
	}
	return cfg
}

func validateChatGPTBuiltinProfile(cfg OAuthClientConfig) error {
	return validateChatGPTBuiltinProfileWithHTTPSAdminAllowlist(cfg, nil)
}

func validateChatGPTBuiltinProfileWithHTTPSAdminAllowlist(cfg OAuthClientConfig, allowlist []string) error {
	var mismatches []string
	if strings.TrimSpace(cfg.ClientID) != chatgptOAuthClientID {
		mismatches = append(mismatches, "client_id")
	}
	if strings.TrimSpace(cfg.ClientSecret) != "" {
		mismatches = append(mismatches, "client_secret")
	}
	if strings.TrimSpace(cfg.AuthURL) != chatgptOAuthAuthURL {
		mismatches = append(mismatches, "auth_url")
	}
	if strings.TrimSpace(cfg.TokenURL) != chatgptOAuthTokenURL {
		mismatches = append(mismatches, "token_url")
	}
	if strings.Join(trimmedFields(cfg.Scopes), " ") != chatgptOAuthScope {
		mismatches = append(mismatches, "scope")
	}
	if err := validateChatGPTRedirectURIWithHTTPSAdminAllowlist(cfg.RedirectURI, allowlist); err != nil {
		mismatches = append(mismatches, fmt.Sprintf("redirect_uri (%v)", err))
	}
	if source := strings.TrimSpace(cfg.Source); source != "" && source != ClientSourcePublicCLI {
		mismatches = append(mismatches, "source")
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("%w: openai chatgpt_oauth built-in profile mismatch: %s", ErrFeatureDisabled, strings.Join(mismatches, ","))
	}
	return nil
}

func validateChatGPTRedirectURI(raw string) error {
	return validateChatGPTRedirectURIWithHTTPSAdminAllowlist(raw, nil)
}

func validateChatGPTRedirectURIWithHTTPSAdminAllowlist(raw string, allowlist []string) error {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("%w: invalid redirect_uri: %v", ErrFeatureDisabled, err)
	}
	switch parsed.Scheme {
	case "http":
		if trimmed != chatgptOAuthLoopbackRedirect {
			return fmt.Errorf("%w: loopback redirect must be %s", ErrFeatureDisabled, chatgptOAuthLoopbackRedirect)
		}
		return nil
	case "https":
		if err := validateOAuthEndpointURL(trimmed); err != nil {
			return fmt.Errorf("%w: admin redirect rejected (%v)", ErrFeatureDisabled, err)
		}
		if parsed.EscapedPath() != chatgptAdminCallbackPath {
			return fmt.Errorf("%w: admin redirect path must be %s", ErrFeatureDisabled, chatgptAdminCallbackPath)
		}
		if !chatgptHTTPSAdminCallbackAllowed(trimmed, allowlist) {
			return fmt.Errorf("%w: admin redirect must match static allowlist", ErrFeatureDisabled)
		}
		return nil
	default:
		return fmt.Errorf("%w: redirect scheme must be http loopback or https admin", ErrFeatureDisabled)
	}
}

func chatgptHTTPSAdminCallbackAllowed(raw string, allowlist []string) bool {
	want, ok := chatgptAdminCallbackAllowlistKey(raw)
	if !ok {
		return false
	}
	for _, allowed := range allowlist {
		got, ok := chatgptAdminCallbackAllowlistKey(allowed)
		if ok && got == want {
			return true
		}
	}
	return false
}

func chatgptAdminCallbackAllowlistKey(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	q := parsed.Query()
	for key, values := range q {
		if key != "flow_id" {
			return "", false
		}
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			return "", false
		}
	}
	q.Del("flow_id")
	parsed.RawQuery = q.Encode()
	parsed.Fragment = ""
	return parsed.String(), true
}

func chatgptRedirectURIWithFlowID(raw, flowID string) string {
	trimmed := strings.TrimSpace(raw)
	flowID = strings.TrimSpace(flowID)
	if trimmed == "" || flowID == "" {
		return trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	if parsed.Scheme != "https" || parsed.EscapedPath() != chatgptAdminCallbackPath {
		return trimmed
	}
	q := parsed.Query()
	q.Set("flow_id", flowID)
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func buildChatGPTAuthorizeURL(cfg OAuthClientConfig, state, codeChallenge, flowID string) string {
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

func (e chatgptOAuthExchanger) validateBuiltinProfile(cfg OAuthClientConfig) error {
	return validateChatGPTBuiltinProfileWithHTTPSAdminAllowlist(cfg, e.httpsAdminCallbackAllowlist)
}

func (e chatgptOAuthExchanger) exchangeAuthorizationCodeForm(ctx context.Context, payload storedPKCEPayload, code string) (chatgptOAuthTokenResponse, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return chatgptOAuthTokenResponse{}, fmt.Errorf("%w: authorization code is empty", ErrInvalidTokenShape)
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", strings.TrimSpace(payload.RedirectURI))
	form.Set("client_id", chatgptOAuthClientID)
	form.Set("code_verifier", strings.TrimSpace(payload.CodeVerifier))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatgptOAuthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return chatgptOAuthTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := e.client().Do(req)
	if err != nil {
		return chatgptOAuthTokenResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return chatgptOAuthTokenResponse{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return chatgptOAuthTokenResponse{}, fmt.Errorf("credentialacq: chatgpt oauth token endpoint returned status %d: %s", resp.StatusCode, oauthErrorSummary(body))
	}
	token, err := parseTokenResponseWithChatGPTMetadata(body)
	if err != nil {
		return chatgptOAuthTokenResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return chatgptOAuthTokenResponse{}, fmt.Errorf("%w: chatgpt oauth response missing access_token", ErrInvalidTokenShape)
	}
	if strings.TrimSpace(token.RefreshToken) == "" {
		return chatgptOAuthTokenResponse{}, fmt.Errorf("%w: chatgpt oauth response missing refresh_token (offline_access scope expected)", ErrInvalidTokenShape)
	}
	return token, nil
}

func parseTokenResponseWithChatGPTMetadata(raw []byte) (chatgptOAuthTokenResponse, error) {
	var token chatgptOAuthTokenResponse
	if err := json.Unmarshal(raw, &token); err != nil {
		return chatgptOAuthTokenResponse{}, err
	}
	token.ChatGPTUserID = strings.TrimSpace(token.ChatGPTUserID)
	token.ChatGPTPlanType = strings.TrimSpace(token.ChatGPTPlanType)
	token.ChatGPTAccountID = strings.TrimSpace(token.ChatGPTAccountID)
	return token, nil
}

func (e chatgptOAuthExchanger) chatgptOAuthTokenPayload(token chatgptOAuthTokenResponse, stored storedPKCEPayload) ([]byte, error) {
	raw, err := tokenCandidatePayload(token.oauthTokenResponse, stored, e.nowTime())
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	out["client_identity_source"] = chatgptApprovedProfileSource
	out["client_id_source"] = chatgptApprovedProfileSource
	if token.ChatGPTUserID != "" {
		out["chatgpt_user_id"] = token.ChatGPTUserID
	}
	if token.ChatGPTPlanType != "" {
		out["chatgpt_plan_type"] = token.ChatGPTPlanType
	}
	if token.ChatGPTAccountID != "" {
		out["chatgpt_account_id"] = token.ChatGPTAccountID
	}
	return json.Marshal(out)
}

func (e chatgptOAuthExchanger) client() *http.Client {
	if e.httpClient != nil {
		return e.httpClient
	}
	return auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
}

func (e chatgptOAuthExchanger) nowTime() time.Time {
	if e.now != nil {
		return e.now().UTC()
	}
	return time.Now().UTC()
}
