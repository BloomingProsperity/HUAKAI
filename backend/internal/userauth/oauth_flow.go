package userauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/google/uuid"
)

func NewOAuthFlowChallenge(tenantID int64, provider, redirectURI string, ttl time.Duration, now time.Time) (OAuthFlowChallenge, error) {
	state, stateHash, err := GenerateToken()
	if err != nil {
		return OAuthFlowChallenge{}, err
	}
	nonce, nonceHash, err := GenerateToken()
	if err != nil {
		return OAuthFlowChallenge{}, err
	}
	verifier, _, err := GenerateToken()
	if err != nil {
		return OAuthFlowChallenge{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	return OAuthFlowChallenge{
		ID:            uuid.NewString(),
		TenantID:      tenantID,
		Provider:      normalizeSocialProvider(provider),
		State:         state,
		StateHash:     stateHash,
		Nonce:         nonce,
		NonceHash:     nonceHash,
		PKCEVerifier:  verifier,
		PKCEChallenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		RedirectURI:   strings.TrimSpace(redirectURI),
		ExpiresAt:     now.UTC().Add(ttl),
	}, nil
}

type OAuthHTTPProvider struct {
	cfg    OAuthConfig
	client *http.Client
	now    func() time.Time
}

func NewOAuthHTTPProvider(cfg OAuthConfig, client *http.Client) (*OAuthHTTPProvider, error) {
	cfg.Provider = normalizeSocialProvider(cfg.Provider)
	if cfg.Provider == "" || strings.TrimSpace(cfg.ClientID) == "" {
		return nil, ErrInvalidInput
	}
	if client == nil {
		client = http.DefaultClient
	}
	var err error
	cfg, err = applyOAuthProviderDefaults(cfg)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.TokenURL) == "" {
		return nil, ErrInvalidInput
	}
	// 静态出站 endpoint 门控。social OAuth 的 token 兑换/JWKS/GitHub user&emails 都向运维可
	// 配置的 endpoint 发出(携带 OAuth code、client_secret、Bearer token)。这里在构造期拒绝非 https 或
	// 字面私有/环回/链路本地(含 169.254 元数据)地址,堵住把凭据发往内网/metadata 的误配/投毒。域名解析
	// 到私有 IP 的 DNS-rebind 由拨号期 SSRF 客户端再拦一层(buildOAuthProvider 注入)。
	for _, ep := range []struct{ label, url string }{
		{"auth_url", cfg.AuthURL}, {"token_url", cfg.TokenURL}, {"jwks_url", cfg.JWKSURL},
		{"user_url", cfg.UserURL}, {"emails_url", cfg.EmailsURL}, {"openid_url", cfg.OpenIDURL},
	} {
		if err := ValidateOAuthEndpointURL(ep.label, ep.url); err != nil {
			return nil, err
		}
	}
	return &OAuthHTTPProvider{cfg: cfg, client: client, now: time.Now}, nil
}

// ValidateOAuthEndpointURL 校验一个出站 OAuth endpoint:空值跳过(表示不适用,如 GitHub 无 JWKS);
// 非空则必须是 https 且 host 非字面私有/环回/链路本地/元数据 IP。
func ValidateOAuthEndpointURL(label, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %s 非法 url", ErrInvalidInput, label)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("%w: %s 必须使用 https", ErrInvalidInput, label)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: %s 缺少 host", ErrInvalidInput, label)
	}
	// 复用拨号期 SSRF guard 的同一套严格 IP deny 策略(环回/私有/链路本地/CGNAT 100.64/10/special-use/
	// 组播/非全局单播),保证静态校验与拨号校验不漂移。
	if ip := net.ParseIP(host); ip != nil && !auth.IsPublicOAuthIP(ip) {
		return fmt.Errorf("%w: %s 不能指向私有/环回/CGNAT/special-use/元数据地址", ErrInvalidInput, label)
	}
	return nil
}

func (p *OAuthHTTPProvider) Provider() string {
	if p == nil {
		return ""
	}
	return p.cfg.Provider
}

func (p *OAuthHTTPProvider) AuthorizationURL(challenge OAuthFlowChallenge) (string, error) {
	if p == nil {
		return "", ErrOAuthProviderMissing
	}
	u, err := url.Parse(p.cfg.AuthURL)
	if err != nil {
		return "", err
	}
	redirectURI := firstNonEmpty(challenge.RedirectURI, p.cfg.RedirectURI)
	q := u.Query()
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("state", challenge.State)
	q.Set("code_challenge", challenge.PKCEChallenge)
	q.Set("code_challenge_method", "S256")
	if challenge.Nonce != "" {
		q.Set("nonce", challenge.Nonce)
	}
	if len(p.cfg.Scopes) > 0 {
		q.Set("scope", strings.Join(p.cfg.Scopes, " "))
	}
	if p.cfg.Provider == SocialProviderDingTalk {
		q.Set("prompt", "consent")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (p *OAuthHTTPProvider) ExchangeVerifiedIdentity(ctx context.Context, flow OAuthFlowSession, code string) (VerifiedIdentity, error) {
	if p == nil {
		return VerifiedIdentity{}, ErrOAuthProviderMissing
	}
	if p.cfg.Provider == SocialProviderDingTalk {
		return p.dingTalkIdentity(ctx, flow, code)
	}
	resp, err := p.exchangeCode(ctx, flow, code)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	switch p.cfg.Provider {
	case SocialProviderGoogle:
		return p.googleIdentity(ctx, flow, resp)
	case SocialProviderGitHub:
		return p.githubIdentity(ctx, resp.AccessToken)
	case SocialProviderQQ:
		return p.qqIdentity(ctx, resp.AccessToken)
	case SocialProviderNodeSeek:
		return p.genericUserInfoIdentity(ctx, resp.AccessToken)
	case SocialProviderLinuxDo:
		return p.genericUserInfoIdentity(ctx, resp.AccessToken)
	case SocialProviderDiscord:
		return p.genericUserInfoIdentity(ctx, resp.AccessToken)
	default:
		return VerifiedIdentity{}, ErrOAuthProviderMissing
	}
}

type oauthTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	AccessCamel string `json:"accessToken"`
	IDToken     string `json:"id_token"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

func (p *OAuthHTTPProvider) exchangeCode(ctx context.Context, flow OAuthFlowSession, code string) (oauthTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", strings.TrimSpace(code))
	form.Set("client_id", p.cfg.ClientID)
	if p.cfg.ClientSecret != "" {
		form.Set("client_secret", p.cfg.ClientSecret)
	}
	form.Set("redirect_uri", firstNonEmpty(flow.RedirectURI, p.cfg.RedirectURI))
	form.Set("code_verifier", flow.PKCEVerifier)
	if p.cfg.Provider == SocialProviderQQ {
		form.Set("fmt", "json")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	out, err := decodeOAuthTokenResponse(body)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	if out.AccessToken == "" {
		out.AccessToken = out.AccessCamel
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || out.Error != "" {
		logOAuthProviderUpstreamError(ctx, p.cfg.Provider, res.StatusCode, oauthProviderErrorDetails{
			Code: out.Error, Message: out.ErrorDesc,
		})
		return oauthTokenResponse{}, fmt.Errorf("%w: oauth exchange failed", ErrSocialLoginRejected)
	}
	if out.AccessToken == "" && out.IDToken == "" {
		return oauthTokenResponse{}, ErrSocialLoginRejected
	}
	return out, nil
}

func decodeOAuthTokenResponse(body []byte) (oauthTokenResponse, error) {
	var out oauthTokenResponse
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return out, ErrSocialLoginRejected
	}
	if bytes.HasPrefix(trimmed, []byte("{")) {
		if err := json.Unmarshal(trimmed, &out); err != nil {
			return out, err
		}
		return out, nil
	}
	values, err := url.ParseQuery(string(trimmed))
	if err != nil {
		return out, err
	}
	out.AccessToken = values.Get("access_token")
	out.TokenType = values.Get("token_type")
	out.IDToken = values.Get("id_token")
	out.Error = values.Get("error")
	out.ErrorDesc = values.Get("error_description")
	return out, nil
}

func (p *OAuthHTTPProvider) googleIdentity(ctx context.Context, flow OAuthFlowSession, token oauthTokenResponse) (VerifiedIdentity, error) {
	if strings.TrimSpace(token.IDToken) == "" {
		return VerifiedIdentity{}, ErrSocialLoginRejected
	}
	claims, err := p.verifyGoogleIDToken(ctx, token.IDToken, flow)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	email, _ := claims["email"].(string)
	subject, _ := claims["sub"].(string)
	name, _ := claims["name"].(string)
	verified, _ := claims["email_verified"].(bool)
	if NormalizeEmail(email) == "" || strings.TrimSpace(subject) == "" || !verified {
		return VerifiedIdentity{}, ErrSocialLoginRejected
	}
	return VerifiedIdentity{
		Provider:      SocialProviderGoogle,
		Subject:       subject,
		Email:         NormalizeEmail(email),
		DisplayName:   strings.TrimSpace(name),
		EmailVerified: true,
	}, nil
}

func (p *OAuthHTTPProvider) verifyGoogleIDToken(ctx context.Context, raw string, flow OAuthFlowSession) (map[string]any, error) {
	header, claims, signingInput, sig, err := parseJWT(raw)
	if err != nil {
		return nil, ErrSocialLoginRejected
	}
	if alg, _ := header["alg"].(string); alg != "RS256" {
		return nil, ErrSocialLoginRejected
	}
	kid, _ := header["kid"].(string)
	key, err := p.fetchRSAKey(ctx, kid)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return nil, ErrSocialLoginRejected
	}
	if iss, _ := claims["iss"].(string); iss != p.cfg.Issuer && iss != strings.TrimPrefix(p.cfg.Issuer, "https://") {
		return nil, ErrSocialLoginRejected
	}
	if aud, ok := claims["aud"].(string); !ok || aud != p.cfg.ClientID {
		return nil, ErrSocialLoginRejected
	}
	nonce, _ := claims["nonce"].(string)
	if !hmac.Equal(HashToken(nonce), flow.NonceHash) {
		return nil, ErrSocialLoginRejected
	}
	exp, ok := numericClaim(claims["exp"])
	if !ok || !time.Unix(exp, 0).After(p.now().UTC()) {
		return nil, ErrOAuthFlowExpired
	}
	return claims, nil
}

type jwksResponse struct {
	Keys []struct {
		Kid string   `json:"kid"`
		Kty string   `json:"kty"`
		Alg string   `json:"alg"`
		Use string   `json:"use"`
		N   string   `json:"n"`
		E   string   `json:"e"`
		X5C []string `json:"x5c"`
	} `json:"keys"`
}

func (p *OAuthHTTPProvider) fetchRSAKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, ErrSocialLoginRejected
	}
	var jwks jwksResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&jwks); err != nil {
		return nil, err
	}
	for _, key := range jwks.Keys {
		if kid != "" && key.Kid != kid {
			continue
		}
		if len(key.X5C) > 0 {
			certDER, err := base64.StdEncoding.DecodeString(key.X5C[0])
			if err != nil {
				return nil, err
			}
			cert, err := x509.ParseCertificate(certDER)
			if err != nil {
				return nil, err
			}
			rsaKey, ok := cert.PublicKey.(*rsa.PublicKey)
			if !ok {
				return nil, ErrSocialLoginRejected
			}
			return rsaKey, nil
		}
		if key.N != "" && key.E != "" {
			nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
			if err != nil {
				return nil, err
			}
			eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
			if err != nil {
				return nil, err
			}
			e := 0
			for _, b := range eBytes {
				e = e<<8 + int(b)
			}
			return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
		}
	}
	return nil, ErrSocialLoginRejected
}

func (p *OAuthHTTPProvider) githubIdentity(ctx context.Context, accessToken string) (VerifiedIdentity, error) {
	if strings.TrimSpace(accessToken) == "" {
		return VerifiedIdentity{}, ErrSocialLoginRejected
	}
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := p.getBearerJSON(ctx, p.cfg.UserURL, accessToken, &user); err != nil {
		return VerifiedIdentity{}, err
	}
	var emails []struct {
		Email    string `json:"email"`
		Verified bool   `json:"verified"`
		Primary  bool   `json:"primary"`
	}
	if err := p.getBearerJSON(ctx, p.cfg.EmailsURL, accessToken, &emails); err != nil {
		return VerifiedIdentity{}, err
	}
	chosen := ""
	for _, item := range emails {
		if item.Verified && item.Primary {
			chosen = item.Email
			break
		}
	}
	if chosen == "" {
		for _, item := range emails {
			if item.Verified {
				chosen = item.Email
				break
			}
		}
	}
	if user.ID <= 0 || NormalizeEmail(chosen) == "" {
		return VerifiedIdentity{}, ErrSocialLoginRejected
	}
	display := firstNonEmpty(user.Name, user.Login)
	return VerifiedIdentity{
		Provider:      SocialProviderGitHub,
		Subject:       strconv.FormatInt(user.ID, 10),
		Email:         NormalizeEmail(chosen),
		DisplayName:   strings.TrimSpace(display),
		EmailVerified: true,
	}, nil
}

func (p *OAuthHTTPProvider) getBearerJSON(ctx context.Context, endpoint, accessToken string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		logOAuthProviderUpstreamError(ctx, p.cfg.Provider, res.StatusCode,
			oauthProviderErrorFromJSON(body, []string{"error", "code"}, []string{"error_description", "message"}))
		return ErrSocialLoginRejected
	}
	return json.Unmarshal(body, dst)
}

func parseJWT(raw string) (map[string]any, map[string]any, string, []byte, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, nil, "", nil, ErrSocialLoginRejected
	}
	var header map[string]any
	var claims map[string]any
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, "", nil, err
	}
	claimBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, nil, "", nil, err
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, nil, "", nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(claimBytes))
	dec.UseNumber()
	if err := dec.Decode(&claims); err != nil {
		return nil, nil, "", nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, nil, "", nil, err
	}
	return header, claims, parts[0] + "." + parts[1], sig, nil
}

func numericClaim(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
