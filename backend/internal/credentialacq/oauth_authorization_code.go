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
)

type authorizationCodeOAuthExchanger struct {
	vendor   string
	authMode string
	shape    TokenShape
	client   *http.Client
	now      func() time.Time
}

type storedPKCEPayload struct {
	CodeVerifier string   `json:"code_verifier"`
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
}

func newAuthorizationCodeOAuthExchanger(vendor, authMode string, shape TokenShape) authorizationCodeOAuthExchanger {
	if shape == "" {
		shape = TokenShapeAnySessionOrAccess
	}
	return authorizationCodeOAuthExchanger{vendor: vendor, authMode: authMode, shape: shape}
}

func (e authorizationCodeOAuthExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	if err := validateOperatorPKCEConfig(e.vendor, e.authMode, cfg); err != nil {
		return OAuthStartResult{}, err
	}
	in.Vendor = e.vendor
	in.AuthMode = e.authMode
	return startStoredPKCEOAuthFlow(ctx, store, in, cfg)
}

func (e authorizationCodeOAuthExchanger) ExchangeOAuthCode(_ context.Context, session Session, code string) (CredentialCandidate, error) {
	// 单测和手工恢复仍允许 JSON token 直填；真实 callback 走带 store 的解密交换路径。
	return NewPKCEFakeExchanger(e.shape).ExchangeOAuthCode(context.Background(), session, code)
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
	return CredentialCandidate{
		TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
		Vendor: session.Vendor, AuthMode: session.AuthMode, Payload: raw, ActorID: session.ActorID,
	}, nil
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
		// caller 注入 custom client (test mock RoundTripper) 时, 走 caller
		// 自带的 client; production wiring 应注入 SSRF-protected client (例 anthropicoauth.DefaultHTTPClient
		// 的 mimicry uTLS 也实现了 OAuth-grade defense)。
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
	return OAuthStartResult{
		Session: session, AuthType: AuthTypePKCE, State: state, CodeVerifier: verifier, CodeChallenge: challenge,
		AuthorizeURL: BuildAuthorizeURL(cfg, state, challenge),
	}, nil
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
		if ip.String() == "169.254.169.254" { // GCP/AWS/Azure metadata
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

func (e authorizationCodeOAuthExchanger) nowTime() time.Time {
	if e.now != nil {
		return e.now().UTC()
	}
	return time.Now().UTC()
}
