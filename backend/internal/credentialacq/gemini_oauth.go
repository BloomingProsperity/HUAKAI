package credentialacq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/google/uuid"
)

const (
	GeminiPublicCLIClientID     = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	GeminiPublicCLISecretEnv    = "HUAKAI_GEMINI_OAUTH_CLIENT_SECRET"
	DefaultGeminiTokenEndpoint  = "https://oauth2.googleapis.com/token"
	geminiPublicCLIClientID     = GeminiPublicCLIClientID
	geminiPublicCLIClientSecret = "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"
	geminiOAuthAuthURL          = "https://accounts.google.com/o/oauth2/v2/auth"
	geminiOAuthTokenURL         = DefaultGeminiTokenEndpoint
	geminiOAuthScope            = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"
	geminiOAuthLoopbackRedirect = "http://localhost:8085/oauth2callback"
	geminiApprovedProfileSource = "approved_builtin_profile_gemini_public_cli"

	geminiAdminCallbackPath = "/admin/v1/credentials/oauth-callback"
)

type geminiPublicCLIOAuthExchanger struct {
	now                         func() time.Time
	httpClient                  *http.Client
	authMode                    string
	clientSecret                string
	httpsAdminCallbackAllowlist []string
}

func newGeminiPublicCLIOAuthExchanger(authMode string) geminiPublicCLIOAuthExchanger {
	return geminiPublicCLIOAuthExchanger{authMode: strings.TrimSpace(authMode)}
}

// NewGeminiPublicCLIOAuthExchangerWithClient 返回已注入 HTTP client 的 Gemini exchanger。
// 兼容旧测试/调用点；未注入 env secret 时 StartOAuthFlow 会 fail-closed。
func NewGeminiPublicCLIOAuthExchangerWithClient(authMode string, client *http.Client) Exchanger {
	return geminiPublicCLIOAuthExchanger{authMode: strings.TrimSpace(authMode), httpClient: client}
}

// NewGeminiPublicCLIOAuthExchangerWithClientAndSecret 返回已注入 HTTP client
// 和 operator env secret 的 Gemini exchanger。生产 wiring 只允许走该入口。
func NewGeminiPublicCLIOAuthExchangerWithClientAndSecret(authMode string, client *http.Client, secret string) Exchanger {
	return geminiPublicCLIOAuthExchanger{
		authMode:     strings.TrimSpace(authMode),
		httpClient:   client,
		clientSecret: strings.TrimSpace(secret),
	}
}

// NewGeminiPublicCLIOAuthExchangerWithClientAndAdminCallbackAllowlist 返回带
// 静态 admin callback allowlist 的 Gemini exchanger。allowlist 必须来自
// operator 配置或测试注入，不能来自 OAuth 启动请求体。
func NewGeminiPublicCLIOAuthExchangerWithClientAndAdminCallbackAllowlist(authMode string, client *http.Client, allowlist []string) Exchanger {
	return geminiPublicCLIOAuthExchanger{
		authMode:                    strings.TrimSpace(authMode),
		httpClient:                  client,
		httpsAdminCallbackAllowlist: cloneTrimmedStrings(allowlist),
	}
}

// NewGeminiPublicCLIOAuthExchangerWithClientSecretAndAdminCallbackAllowlist
// 返回同时注入 env secret 与静态 admin callback allowlist 的 Gemini exchanger。
func NewGeminiPublicCLIOAuthExchangerWithClientSecretAndAdminCallbackAllowlist(authMode string, client *http.Client, secret string, allowlist []string) Exchanger {
	return geminiPublicCLIOAuthExchanger{
		authMode:                    strings.TrimSpace(authMode),
		httpClient:                  client,
		clientSecret:                strings.TrimSpace(secret),
		httpsAdminCallbackAllowlist: cloneTrimmedStrings(allowlist),
	}
}

// IsGeminiPublicCLIOAuthExchangerWithExplicitClient 只在 Gemini exchanger 已注入
// 非 nil HTTP client 时返回 true，供启动自检防止受控 transport 被绕过。
func IsGeminiPublicCLIOAuthExchangerWithExplicitClient(exc Exchanger) bool {
	e, ok := exc.(geminiPublicCLIOAuthExchanger)
	if !ok {
		return false
	}
	return e.httpClient != nil
}

func (e geminiPublicCLIOAuthExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	cfg = geminiBuiltinProfileConfig(cfg)
	// 请求体不能覆盖固定客户端身份；wiring 只能用部署者配置
	// 整体替换公开 profile 的 secret。
	if secret := strings.TrimSpace(e.clientSecret); secret != "" {
		cfg.ClientSecret = secret
	}
	if err := e.validateBuiltinProfile(cfg); err != nil {
		return OAuthStartResult{}, err
	}
	flowID := strings.TrimSpace(in.ID)
	if flowID == "" {
		flowID = uuid.NewString()
	}
	cfg.RedirectURI = geminiRedirectURIWithFlowID(cfg.RedirectURI, flowID)
	in.ID = flowID
	in.RedirectURI = cfg.RedirectURI
	in.Vendor = credentialstore.VendorGemini
	in.AuthMode = e.mode()
	start, err := startStoredPKCEOAuthFlow(ctx, store, in, cfg)
	if err != nil {
		return OAuthStartResult{}, err
	}
	start.AuthorizeURL = buildGeminiAuthorizeURL(cfg, start.State, start.CodeChallenge, start.Session.ID)
	return start, nil
}

func (e geminiPublicCLIOAuthExchanger) ExchangeOAuthCode(context.Context, Session, string) (CredentialCandidate, error) {
	return CredentialCandidate{}, fmt.Errorf("%w: gemini %s requires stored PKCE verifier", ErrOAuthExchangerMissing, e.mode())
}

func (e geminiPublicCLIOAuthExchanger) ExchangeOAuthCodeWithStore(ctx context.Context, store *PostgresSessionStore, session Session, _ string, code string) (CredentialCandidate, error) {
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
		AuthURL:      geminiOAuthAuthURL,
		TokenURL:     payload.TokenURL,
		RedirectURI:  payload.RedirectURI,
		Scopes:       append([]string(nil), payload.Scopes...),
		Source:       ClientSourcePublicCLI,
	}
	if err := e.validateBuiltinProfile(cfg); err != nil {
		return CredentialCandidate{}, err
	}
	payload.ClientID = geminiPublicCLIClientID
	payload.TokenURL = geminiOAuthTokenURL
	payload.RedirectURI = strings.TrimSpace(cfg.RedirectURI)
	payload.Scopes = strings.Fields(geminiOAuthScope)

	token, err := e.exchangeAuthorizationCodeForm(ctx, payload, code)
	if err != nil {
		return CredentialCandidate{}, err
	}
	raw, err := e.geminiOAuthTokenPayload(token, payload)
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
		RedactedContext: map[string]any{"client_identity_source": geminiApprovedProfileSource},
	}
	// 上游账户身份从 id_token 的 sub/email 声明提取(userinfo HTTP 拉取留 roadmap,
	// 避免在 SSRF 受控交换路径新增出站);仅作账户管理元数据,解析失败回退空/manual。
	AttachIdentity(&candidate, accountident.ExtractGemini(token.IDToken, ""))
	return candidate, nil
}

func geminiBuiltinProfileConfig(override OAuthClientConfig) OAuthClientConfig {
	cfg := OAuthClientConfig{
		ClientID:     geminiPublicCLIClientID,
		ClientSecret: geminiPublicCLIClientSecret,
		AuthURL:      geminiOAuthAuthURL,
		TokenURL:     geminiOAuthTokenURL,
		RedirectURI:  geminiOAuthLoopbackRedirect,
		Scopes:       strings.Fields(geminiOAuthScope),
		Source:       ClientSourcePublicCLI,
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

// GeminiPublicCLIConfig 返回授权、导入刷新和定时刷新共用的公开客户端身份。
// 部署者可通过环境变量应对上游 profile 轮换，单次导入不能提供替换值。
func GeminiPublicCLIConfig() OAuthClientConfig {
	cfg := geminiBuiltinProfileConfig(OAuthClientConfig{})
	if secret := strings.TrimSpace(os.Getenv(GeminiPublicCLISecretEnv)); secret != "" {
		cfg.ClientSecret = secret
	}
	return cfg
}

func validateGeminiBuiltinProfile(cfg OAuthClientConfig) error {
	return validateGeminiBuiltinProfileWithHTTPSAdminAllowlist(cfg, nil)
}

func validateGeminiBuiltinProfileWithHTTPSAdminAllowlist(cfg OAuthClientConfig, allowlist []string) error {
	var mismatches []string
	if strings.TrimSpace(cfg.ClientID) != geminiPublicCLIClientID {
		mismatches = append(mismatches, "client_id")
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		mismatches = append(mismatches, "client_secret")
	}
	if strings.TrimSpace(cfg.AuthURL) != geminiOAuthAuthURL {
		mismatches = append(mismatches, "auth_url")
	}
	if strings.TrimSpace(cfg.TokenURL) != geminiOAuthTokenURL {
		mismatches = append(mismatches, "token_url")
	}
	if strings.Join(trimmedFields(cfg.Scopes), " ") != geminiOAuthScope {
		mismatches = append(mismatches, "scope")
	}
	if err := validateGeminiRedirectURIWithHTTPSAdminAllowlist(cfg.RedirectURI, allowlist); err != nil {
		mismatches = append(mismatches, fmt.Sprintf("redirect_uri (%v)", err))
	}
	if source := strings.TrimSpace(cfg.Source); source != "" && source != ClientSourcePublicCLI {
		mismatches = append(mismatches, "source")
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("%w: gemini public CLI built-in profile mismatch: %s", ErrFeatureDisabled, strings.Join(mismatches, ","))
	}
	return nil
}

func validateGeminiRedirectURI(raw string) error {
	return validateGeminiRedirectURIWithHTTPSAdminAllowlist(raw, nil)
}

func validateGeminiRedirectURIWithHTTPSAdminAllowlist(raw string, allowlist []string) error {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("%w: invalid redirect_uri: %v", ErrFeatureDisabled, err)
	}
	switch parsed.Scheme {
	case "http":
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
		if parsed.EscapedPath() != "/oauth2callback" {
			return fmt.Errorf("%w: loopback redirect path must be /oauth2callback", ErrFeatureDisabled)
		}
		return nil
	case "https":
		if err := validateOAuthEndpointURL(trimmed); err != nil {
			return fmt.Errorf("%w: admin redirect rejected (%v)", ErrFeatureDisabled, err)
		}
		if parsed.EscapedPath() != geminiAdminCallbackPath {
			return fmt.Errorf("%w: admin redirect path must be %s", ErrFeatureDisabled, geminiAdminCallbackPath)
		}
		// D-3=C 的 admin server callback 需要 operator 静态 allowlist 注入。
		// 本切片尚未接 ProfileBindings wiring，默认空 allowlist 拒绝 HTTPS admin redirect，只保留 loopback。
		if !geminiHTTPSAdminCallbackAllowed(trimmed, allowlist) {
			return fmt.Errorf("%w: admin redirect must match static allowlist", ErrFeatureDisabled)
		}
		return nil
	default:
		return fmt.Errorf("%w: redirect scheme must be http loopback or https admin", ErrFeatureDisabled)
	}
}

func geminiHTTPSAdminCallbackAllowed(raw string, allowlist []string) bool {
	want, ok := geminiAdminCallbackAllowlistKey(raw)
	if !ok {
		return false
	}
	for _, allowed := range allowlist {
		got, ok := geminiAdminCallbackAllowlistKey(allowed)
		if ok && got == want {
			return true
		}
	}
	return false
}

func geminiAdminCallbackAllowlistKey(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	if parsed.Fragment != "" || parsed.RawFragment != "" {
		return "", false
	}
	if parsed.User != nil {
		return "", false
	}
	if parsed.Opaque != "" {
		return "", false
	}
	q, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", false
	}
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
	return parsed.String(), true
}

func cloneTrimmedStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, item := range in {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func geminiRedirectURIWithFlowID(raw, flowID string) string {
	trimmed := strings.TrimSpace(raw)
	flowID = strings.TrimSpace(flowID)
	if trimmed == "" || flowID == "" {
		return trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	if parsed.Scheme != "https" || parsed.EscapedPath() != geminiAdminCallbackPath {
		return trimmed
	}
	q := parsed.Query()
	q.Set("flow_id", flowID)
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func buildGeminiAuthorizeURL(cfg OAuthClientConfig, state, codeChallenge, flowID string) string {
	cfg.RedirectURI = geminiRedirectURIWithFlowID(cfg.RedirectURI, flowID)
	raw := BuildAuthorizeURL(cfg, state, codeChallenge)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	u.RawQuery = q.Encode()
	return u.String()
}

func (e geminiPublicCLIOAuthExchanger) validateBuiltinProfile(cfg OAuthClientConfig) error {
	return validateGeminiBuiltinProfileWithHTTPSAdminAllowlist(cfg, e.httpsAdminCallbackAllowlist)
}

func (e geminiPublicCLIOAuthExchanger) exchangeAuthorizationCodeForm(ctx context.Context, payload storedPKCEPayload, code string) (oauthTokenResponse, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: authorization code is empty", ErrInvalidTokenShape)
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", strings.TrimSpace(payload.RedirectURI))
	form.Set("client_id", geminiPublicCLIClientID)
	form.Set("client_secret", strings.TrimSpace(payload.ClientSecret))
	form.Set("code_verifier", strings.TrimSpace(payload.CodeVerifier))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, geminiOAuthTokenURL, strings.NewReader(form.Encode()))
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
		return oauthTokenResponse{}, fmt.Errorf("credentialacq: gemini oauth token endpoint returned status %d: %s", resp.StatusCode, oauthErrorSummary(body))
	}
	var token oauthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return oauthTokenResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: gemini oauth token response missing access token", ErrInvalidTokenShape)
	}
	if strings.TrimSpace(token.RefreshToken) == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: gemini oauth token response missing refresh token (access_type=offline + prompt=consent expected to issue refresh_token)", ErrInvalidTokenShape)
	}
	return token, nil
}

func (e geminiPublicCLIOAuthExchanger) geminiOAuthTokenPayload(token oauthTokenResponse, stored storedPKCEPayload) ([]byte, error) {
	raw, err := tokenCandidatePayload(token, stored, e.nowTime())
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	out["client_identity_source"] = geminiApprovedProfileSource
	out["client_id_source"] = geminiApprovedProfileSource
	return json.Marshal(out)
}

func (e geminiPublicCLIOAuthExchanger) client() *http.Client {
	if e.httpClient != nil {
		return e.httpClient
	}
	return auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
}

func (e geminiPublicCLIOAuthExchanger) nowTime() time.Time {
	if e.now != nil {
		return e.now().UTC()
	}
	return time.Now().UTC()
}

func (e geminiPublicCLIOAuthExchanger) mode() string {
	if strings.TrimSpace(e.authMode) != "" {
		return strings.TrimSpace(e.authMode)
	}
	return credentialstore.AuthModeCodeAssist
}
