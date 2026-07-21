package credentialacq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

type authorizationCodeOAuthExchanger struct {
	vendor   string
	authMode string
	shape    TokenShape
	config   OAuthClientConfig
	client   *http.Client
	now      func() time.Time
}

type storedPKCEPayload struct {
	CodeVerifier string   `json:"code_verifier"`
	Nonce        string   `json:"nonce,omitempty"`
	TokenURL     string   `json:"token_url"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty"`
	RedirectURI  string   `json:"redirect_uri"`
	Scopes       []string `json:"scopes,omitempty"`
}

type oauthTokenResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	IDToken      string          `json:"id_token"`
	TokenType    string          `json:"token_type"`
	Scope        string          `json:"scope"`
	ExpiresIn    json.RawMessage `json:"expires_in"`
	// Anthropic 的 claude_ai_oauth 在 token 交换响应体内联携带上游账号身份。
	// 其他 provider 会留空这些字段,改为从 id_token claim 读取身份,因此这些字段纯属增量补充。
	Account struct {
		UUID         string `json:"uuid"`
		EmailAddress string `json:"email_address"`
	} `json:"account"`
	Email string `json:"email"`
	// OpenAI 的 OAuth 响应可能直接携带套餐与账号元数据。其他厂商返回时这些字段为空。
	ChatGPTUserID    string `json:"chatgpt_user_id"`
	ChatGPTPlanType  string `json:"chatgpt_plan_type"`
	ChatGPTAccountID string `json:"chatgpt_account_id"`
}

func newAuthorizationCodeOAuthExchanger(vendor, authMode string, shape TokenShape, defaults ...OAuthClientConfig) authorizationCodeOAuthExchanger {
	if shape == "" {
		shape = TokenShapeAnySessionOrAccess
	}
	cfg := OAuthClientConfig{}
	if len(defaults) > 0 {
		cfg = cloneOAuthClientConfig(defaults[0])
	}
	return authorizationCodeOAuthExchanger{vendor: vendor, authMode: authMode, shape: shape, config: cfg}
}

func (e authorizationCodeOAuthExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	if isAntigravityOAuthMode(e.vendor, e.authMode) {
		// 公开客户端身份必须在授权和后续刷新期间保持一致。请求体不能改写端点、
		// client、回调或 scope；测试可通过 exchanger.client 注入传输层。
		cfg = cloneOAuthClientConfig(e.config)
	} else {
		cfg = mergeOAuthClientConfig(e.config, cfg)
	}
	if err := validateOperatorPKCEConfig(e.vendor, e.authMode, cfg); err != nil {
		return OAuthStartResult{}, err
	}
	in.Vendor = e.vendor
	in.AuthMode = e.authMode
	return startStoredPKCEOAuthFlow(ctx, store, in, cfg)
}

func cloneOAuthClientConfig(cfg OAuthClientConfig) OAuthClientConfig {
	cfg.Scopes = append([]string(nil), cfg.Scopes...)
	return cfg
}

func mergeOAuthClientConfig(base, override OAuthClientConfig) OAuthClientConfig {
	cfg := cloneOAuthClientConfig(base)
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

func (e authorizationCodeOAuthExchanger) ExchangeOAuthCode(_ context.Context, session Session, code string) (CredentialCandidate, error) {
	return CredentialCandidate{}, fmt.Errorf("%w: %s/%s 需要持久化 PKCE 会话", ErrOAuthRequiresCallback, session.Vendor, session.AuthMode)
}

func (e authorizationCodeOAuthExchanger) ExchangeOAuthCodeWithStore(ctx context.Context, store *PostgresSessionStore, session Session, _ string, code string) (CredentialCandidate, error) {
	if store == nil {
		return CredentialCandidate{}, errors.New("credentialacq: session store not configured")
	}
	payload, err := decryptStoredPKCEPayload(ctx, store, session)
	if err != nil {
		return CredentialCandidate{}, err
	}
	token, err := e.exchangeAuthorizationCode(ctx, payload, code)
	if err != nil {
		return CredentialCandidate{}, err
	}
	raw, err := tokenCandidatePayload(token, payload, e.nowTime())
	if err != nil {
		return CredentialCandidate{}, err
	}
	fields, _, err := parseFakeTokenPayload(string(raw))
	if err != nil {
		return CredentialCandidate{}, err
	}
	if err := validateTokenShape(fields, e.shape); err != nil {
		return CredentialCandidate{}, err
	}
	candidate := CredentialCandidate{
		TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
		Vendor: session.Vendor, AuthMode: session.AuthMode, Payload: raw, ActorID: session.ActorID,
	}
	if isXAIOAuthMode(e.vendor, e.authMode) {
		client := e.client
		if client == nil {
			client = auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
		}
		identity, verifyErr := accountident.VerifyOIDCES256Identity(ctx, accountident.OIDCVerificationInput{
			RawIDToken: token.IDToken, Issuer: xaiOIDCIssuer, Audience: payload.ClientID,
			Nonce: payload.Nonce, JWKSURL: xaiOIDCJWKSURL,
			Source: accountident.SourceXAIOIDCSubject, RequireAccountScope: true,
			HTTPClient: client, Now: e.nowTime(),
		})
		if verifyErr != nil {
			return CredentialCandidate{}, fmt.Errorf("%w: xAI OIDC 身份校验失败: %v", ErrInvalidTokenShape, verifyErr)
		}
		AttachIdentity(&candidate, identity)
	}
	attachClientIdentitySource(&candidate, fields, session.ClientIdentitySource)
	attachOAuthResponseSubscription(&candidate, token.ChatGPTPlanType)
	return candidate, nil
}

func (e authorizationCodeOAuthExchanger) exchangeAuthorizationCode(ctx context.Context, payload storedPKCEPayload, code string) (oauthTokenResponse, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: authorization code is empty", ErrInvalidTokenShape)
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", strings.TrimSpace(payload.ClientID))
	form.Set("redirect_uri", strings.TrimSpace(payload.RedirectURI))
	form.Set("code_verifier", strings.TrimSpace(payload.CodeVerifier))
	if secret := strings.TrimSpace(payload.ClientSecret); secret != "" {
		form.Set("client_secret", secret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, payload.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := e.client
	if client == nil {
		// 深层 SSRF / DNS-rebind 防御
		// DEFERRED 尾巴): 默认 (生产 wiring 未注入 custom client) 走
		// SSRF-protected stdlib client — transport.Proxy=nil + DialContext
		// 拨号校验目标 IP 非 loopback/private/link-local/metadata + CheckRedirect
		// 禁 3xx, 关 DNS-rebind 攻击面 (静态层 commit 39c66a3 已落地)。
		// caller 注入 custom client（测试 mock RoundTripper）时走 caller
		// 自带的 client；生产 wiring 必须注入具备同等级 SSRF 防护的 client。
		client = auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
	}
	resp, err := client.Do(req)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return oauthTokenResponse{}, fmt.Errorf("credentialacq: oauth token endpoint returned status %d", resp.StatusCode)
	}
	var token oauthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return oauthTokenResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: oauth token response missing access token", ErrInvalidTokenShape)
	}
	return token, nil
}

func startStoredPKCEOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	if store == nil {
		return OAuthStartResult{}, errors.New("credentialacq: session store not configured")
	}
	state, err := randomURLToken(32)
	if err != nil {
		return OAuthStartResult{}, err
	}
	verifier, err := randomURLToken(64)
	if err != nil {
		return OAuthStartResult{}, err
	}
	challenge := pkceChallenge(verifier)
	nonce := ""
	if isXAIOAuthMode(in.Vendor, in.AuthMode) {
		nonce, err = randomURLToken(32)
		if err != nil {
			return OAuthStartResult{}, err
		}
	}
	in.Kind = FlowKindOAuth
	in.StateHash = HashOAuthState(state)
	if cfg.Source != "" {
		in.ClientIdentitySource = cfg.Source
	}
	if in.ClientIdentitySource == "" {
		in.ClientIdentitySource = ClientSourceOperatorConfig
	}
	if in.RedirectURI == "" {
		in.RedirectURI = cfg.RedirectURI
	}
	if len(in.RequestedScopes) == 0 {
		in.RequestedScopes = cfg.Scopes
	}
	stored := storedPKCEPayload{
		CodeVerifier: verifier,
		Nonce:        nonce,
		TokenURL:     strings.TrimSpace(cfg.TokenURL),
		ClientID:     strings.TrimSpace(cfg.ClientID),
		ClientSecret: strings.TrimSpace(cfg.ClientSecret),
		RedirectURI:  strings.TrimSpace(firstNonEmpty(in.RedirectURI, cfg.RedirectURI)),
		Scopes:       append([]string(nil), cfg.Scopes...),
	}
	rawStored, err := json.Marshal(stored)
	if err != nil {
		return OAuthStartResult{}, err
	}
	ciphertext, metadata, _, err := store.EncryptTransientPayload(ctx, rawStored, pkceAADFromStart(in))
	if err != nil {
		return OAuthStartResult{}, err
	}
	in.EncryptedPKCEVerifier = ciphertext
	in.NonceHash = metadata
	session, err := store.CreateFromStart(ctx, in)
	if err != nil {
		return OAuthStartResult{}, err
	}
	session.AuthType = AuthTypePKCE
	authorizeURL := BuildAuthorizeURL(cfg, state, challenge)
	if nonce != "" {
		authorizeURL = addAuthorizeParameter(authorizeURL, "nonce", nonce)
	}
	return OAuthStartResult{
		Session: session, AuthType: AuthTypePKCE, State: state, CodeVerifier: verifier, CodeChallenge: challenge,
		AuthorizeURL: authorizeURL,
	}, nil
}

func addAuthorizeParameter(rawURL, name, value string) string {
	u, err := url.Parse(rawURL)
	if err != nil || strings.TrimSpace(value) == "" {
		return rawURL
	}
	query := u.Query()
	query.Set(name, value)
	u.RawQuery = query.Encode()
	return u.String()
}

func decryptStoredPKCEPayload(ctx context.Context, store *PostgresSessionStore, session Session) (storedPKCEPayload, error) {
	plain, err := store.DecryptTransientPayload(ctx, session.EncryptedPKCEVerifier, session.NonceHash, pkceAADFromSession(session))
	if err != nil {
		return storedPKCEPayload{}, err
	}
	var payload storedPKCEPayload
	if err := json.NewDecoder(bytes.NewReader(plain)).Decode(&payload); err != nil {
		return storedPKCEPayload{}, fmt.Errorf("%w: oauth callback payload is not a stored PKCE exchange config", ErrInvalidTokenShape)
	}
	if strings.TrimSpace(payload.CodeVerifier) == "" || strings.TrimSpace(payload.TokenURL) == "" ||
		strings.TrimSpace(payload.ClientID) == "" || strings.TrimSpace(payload.RedirectURI) == "" {
		return storedPKCEPayload{}, fmt.Errorf("%w: stored OAuth exchange config missing required fields", ErrInvalidTokenShape)
	}
	return payload, nil
}

func validateOperatorPKCEConfig(vendor, authMode string, cfg OAuthClientConfig) error {
	if isAntigravityOAuthMode(vendor, authMode) {
		return validateAntigravityPublicCLIConfig(cfg)
	}
	var missing []string
	if strings.TrimSpace(cfg.Source) != ClientSourceOperatorConfig {
		missing = append(missing, "source=operator_config")
	}
	if strings.TrimSpace(cfg.AuthURL) == "" {
		missing = append(missing, "auth_url")
	}
	if strings.TrimSpace(cfg.TokenURL) == "" {
		missing = append(missing, "token_url")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		missing = append(missing, "client_id")
	}
	if strings.TrimSpace(cfg.RedirectURI) == "" {
		missing = append(missing, "redirect_uri")
	}
	if !hasOAuthScope(cfg.Scopes) {
		missing = append(missing, "scope")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s/%s operator OAuth config missing %s", ErrFeatureDisabled, vendor, authMode, strings.Join(missing, ","))
	}
	// SSRF / auth-leak 静态闸门: 拒绝任何 scheme
	// 非 https 或 host 命中私网 / loopback / link-local / metadata 的 OAuth
	// endpoint。深层 DialContext-level 防御参考 internal/auth.newSSRFProtectedOAuthClient,
	// 留下一切片接入;此处先封静态层。
	for _, item := range []struct {
		name, raw string
	}{{"auth_url", cfg.AuthURL}, {"token_url", cfg.TokenURL}} {
		if err := validateOAuthEndpointURL(item.raw); err != nil {
			return fmt.Errorf("%w: %s/%s %s 拒绝 (%v)", ErrFeatureDisabled, vendor, authMode, item.name, err)
		}
	}
	if isXAIOAuthMode(vendor, authMode) {
		if err := validateXAIOAuthConfig(cfg); err != nil {
			return fmt.Errorf("%w: %s/%s xAI OAuth config 拒绝 (%v)", ErrFeatureDisabled, vendor, authMode, err)
		}
	}
	return nil
}

func isAntigravityOAuthMode(vendor, authMode string) bool {
	key := credentialstore.ModeKey(vendor, authMode)
	return key == credentialstore.ModeKey(credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth) ||
		key == credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeAntigravity)
}

func validateAntigravityPublicCLIConfig(cfg OAuthClientConfig) error {
	want := AntigravityPublicCLIConfig()
	var mismatches []string
	if strings.TrimSpace(cfg.Source) != ClientSourcePublicCLI {
		mismatches = append(mismatches, "source")
	}
	if strings.TrimSpace(cfg.AuthURL) != want.AuthURL {
		mismatches = append(mismatches, "auth_url")
	}
	if strings.TrimSpace(cfg.TokenURL) != want.TokenURL {
		mismatches = append(mismatches, "token_url")
	}
	if strings.TrimSpace(cfg.ClientID) != want.ClientID {
		mismatches = append(mismatches, "client_id")
	}
	if strings.TrimSpace(cfg.ClientSecret) != want.ClientSecret {
		mismatches = append(mismatches, "client_secret")
	}
	if strings.TrimSpace(cfg.RedirectURI) != want.RedirectURI {
		mismatches = append(mismatches, "redirect_uri")
	}
	if normalizedOAuthScope(cfg.Scopes) != AntigravityPublicCLIScope {
		mismatches = append(mismatches, "scope")
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("%w: antigravity/oauth 公开客户端配置被改写: %s", ErrFeatureDisabled, strings.Join(mismatches, ","))
	}
	for _, raw := range []string{cfg.AuthURL, cfg.TokenURL} {
		if err := validateOAuthEndpointURL(raw); err != nil {
			return fmt.Errorf("%w: antigravity/oauth 端点无效: %v", ErrFeatureDisabled, err)
		}
	}
	return nil
}

func tokenCandidatePayload(token oauthTokenResponse, stored storedPKCEPayload, now time.Time) ([]byte, error) {
	access := strings.TrimSpace(token.AccessToken)
	refresh := strings.TrimSpace(token.RefreshToken)
	out := map[string]any{
		"client_id":            strings.TrimSpace(stored.ClientID),
		"oauth_token_endpoint": strings.TrimSpace(stored.TokenURL),
	}
	if access != "" {
		out["access_token"] = access
		out["session_token"] = access
	}
	if refresh != "" {
		out["refresh_token"] = refresh
	}
	if tokenType := strings.TrimSpace(token.TokenType); tokenType != "" {
		out["token_type"] = tokenType
	}
	if scope := strings.TrimSpace(token.Scope); scope != "" {
		out["scope"] = scope
	}
	if idToken := strings.TrimSpace(token.IDToken); idToken != "" {
		out["id_token"] = idToken
	}
	if userID := strings.TrimSpace(token.ChatGPTUserID); userID != "" {
		out["chatgpt_user_id"] = userID
	}
	if plan := strings.TrimSpace(token.ChatGPTPlanType); plan != "" {
		out["chatgpt_plan_type"] = plan
	}
	if accountID := strings.TrimSpace(token.ChatGPTAccountID); accountID != "" {
		out["chatgpt_account_id"] = accountID
	}
	if seconds := rawExpiresInSeconds(token.ExpiresIn); seconds > 0 {
		out["expires_in"] = seconds
		out["expires_at"] = now.Add(time.Duration(seconds) * time.Second).UTC().Format(time.RFC3339)
	}
	return json.Marshal(out)
}

func rawExpiresInSeconds(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var asInt int
	if err := json.Unmarshal(raw, &asInt); err == nil {
		return firstPositive(asInt)
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return intFromPayload(map[string]any{"expires_in": asString}, "expires_in")
	}
	return 0
}

// validateOAuthEndpointURL 静态拒绝明显的 SSRF / auth-leak 目标:
//   - scheme 必须 https (operator 配 OAuth endpoint 走明文 http 没有合法理由,
//     攻击者可借此把 client_secret / code / verifier 明文渗出)
//   - host 不能空 / 不能是 loopback / 不能是 private-net IP / link-local /
//     metadata IP / 不可路由地址。深层 DialContext 防 DNS-rebind 留 follow-up;
//     此处先封住"caller 直接写 attacker URL"这一层。
func validateOAuthEndpointURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid url: %v", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("scheme=%q must be https", parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("empty host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("host=%s is non-routable / private", host)
		}
		if ip.String() == "169.254.169.254" { // GCP/AWS/Azure 元数据服务
			return fmt.Errorf("host=%s is metadata IP", host)
		}
	} else {
		lower := strings.ToLower(host)
		if lower == "localhost" || strings.HasSuffix(lower, ".localhost") || lower == "metadata.google.internal" || lower == "instance-data" {
			return fmt.Errorf("host=%s is metadata / localhost name", host)
		}
	}
	return nil
}

func validateXAIOAuthConfig(cfg OAuthClientConfig) error {
	if strings.TrimSpace(cfg.ClientID) != xaiOAuthClientID {
		return fmt.Errorf("client_id mismatch")
	}
	if normalizedOAuthScope(cfg.Scopes) != xaiOAuthScope {
		return fmt.Errorf("scope mismatch")
	}
	for _, item := range []struct {
		name, raw string
	}{{"auth_url", cfg.AuthURL}, {"token_url", cfg.TokenURL}} {
		parsed, err := url.Parse(strings.TrimSpace(item.raw))
		if err != nil {
			return fmt.Errorf("%s invalid url: %v", item.name, err)
		}
		if !isXAIOAuthHost(parsed.Hostname()) {
			return fmt.Errorf("%s host=%s is outside x.ai", item.name, parsed.Hostname())
		}
	}
	return nil
}

func isXAIOAuthHost(host string) bool {
	lower := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	return lower == "x.ai" || strings.HasSuffix(lower, ".x.ai")
}

func normalizedOAuthScope(scopes []string) string {
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if trimmed := strings.TrimSpace(scope); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, " ")
}

func (e authorizationCodeOAuthExchanger) nowTime() time.Time {
	if e.now != nil {
		return e.now().UTC()
	}
	return time.Now().UTC()
}
