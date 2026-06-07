package userauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	defaultGoogleAuthURL   = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleTokenURL  = "https://oauth2.googleapis.com/token"
	defaultGoogleJWKSURL   = "https://www.googleapis.com/oauth2/v3/certs"
	defaultGoogleIssuer    = "https://accounts.google.com"
	defaultGitHubAuthURL   = "https://github.com/login/oauth/authorize"
	defaultGitHubTokenURL  = "https://github.com/login/oauth/access_token"
	defaultGitHubUserURL   = "https://api.github.com/user"
	defaultGitHubEmailURL  = "https://api.github.com/user/emails"
	defaultQQAuthURL       = "https://graph.qq.com/oauth2.0/authorize"
	defaultQQTokenURL      = "https://graph.qq.com/oauth2.0/token"
	defaultQQOpenIDURL     = "https://graph.qq.com/oauth2.0/me"
	defaultQQUserURL       = "https://graph.qq.com/user/get_user_info"
	defaultDingAuthURL     = "https://login.dingtalk.com/oauth2/auth"
	defaultDingTokenURL    = "https://api.dingtalk.com/v1.0/oauth2/userAccessToken"
	defaultDingUserURL     = "https://api.dingtalk.com/v1.0/contact/users/me"
	defaultDiscordAuthURL  = "https://discord.com/oauth2/authorize"
	defaultDiscordTokenURL = "https://discord.com/api/oauth2/token"
	defaultDiscordUserURL  = "https://discord.com/api/users/@me"
)

func applyOAuthProviderDefaults(cfg OAuthConfig) (OAuthConfig, error) {
	switch cfg.Provider {
	case SocialProviderGoogle:
		if cfg.AuthURL == "" {
			cfg.AuthURL = defaultGoogleAuthURL
		}
		if cfg.TokenURL == "" {
			cfg.TokenURL = defaultGoogleTokenURL
		}
		if cfg.JWKSURL == "" {
			cfg.JWKSURL = defaultGoogleJWKSURL
		}
		if cfg.Issuer == "" {
			cfg.Issuer = defaultGoogleIssuer
		}
		if len(cfg.Scopes) == 0 {
			cfg.Scopes = []string{"openid", "email", "profile"}
		}
	case SocialProviderGitHub:
		if cfg.AuthURL == "" {
			cfg.AuthURL = defaultGitHubAuthURL
		}
		if cfg.TokenURL == "" {
			cfg.TokenURL = defaultGitHubTokenURL
		}
		if cfg.UserURL == "" {
			cfg.UserURL = defaultGitHubUserURL
		}
		if cfg.EmailsURL == "" {
			cfg.EmailsURL = defaultGitHubEmailURL
		}
		if len(cfg.Scopes) == 0 {
			cfg.Scopes = []string{"read:user", "user:email"}
		}
	case SocialProviderQQ:
		if cfg.AuthURL == "" {
			cfg.AuthURL = defaultQQAuthURL
		}
		if cfg.TokenURL == "" {
			cfg.TokenURL = defaultQQTokenURL
		}
		if cfg.OpenIDURL == "" {
			cfg.OpenIDURL = defaultQQOpenIDURL
		}
		if cfg.UserURL == "" {
			cfg.UserURL = defaultQQUserURL
		}
	case SocialProviderDingTalk:
		if cfg.AuthURL == "" {
			cfg.AuthURL = defaultDingAuthURL
		}
		if cfg.TokenURL == "" {
			cfg.TokenURL = defaultDingTokenURL
		}
		if cfg.UserURL == "" {
			cfg.UserURL = defaultDingUserURL
		}
		if len(cfg.Scopes) == 0 {
			cfg.Scopes = []string{"openid"}
		}
	case SocialProviderNodeSeek:
		if strings.TrimSpace(cfg.SubjectField) == "" ||
			strings.TrimSpace(cfg.AuthURL) == "" ||
			strings.TrimSpace(cfg.UserURL) == "" {
			return OAuthConfig{}, ErrInvalidInput
		}
	case SocialProviderLinuxDo:
		if strings.TrimSpace(cfg.AuthURL) == "" ||
			strings.TrimSpace(cfg.UserURL) == "" {
			return OAuthConfig{}, ErrInvalidInput
		}
		if strings.TrimSpace(cfg.SubjectField) == "" {
			cfg.SubjectField = "id"
		}
		if strings.TrimSpace(cfg.EmailField) == "" {
			cfg.EmailField = "email"
		}
		if strings.TrimSpace(cfg.EmailVerifiedField) == "" {
			cfg.EmailVerifiedField = "email_verified"
		}
		if strings.TrimSpace(cfg.DisplayNameField) == "" {
			cfg.DisplayNameField = "username"
		}
	case SocialProviderDiscord:
		if cfg.AuthURL == "" {
			cfg.AuthURL = defaultDiscordAuthURL
		}
		if cfg.TokenURL == "" {
			cfg.TokenURL = defaultDiscordTokenURL
		}
		if cfg.UserURL == "" {
			cfg.UserURL = defaultDiscordUserURL
		}
		if len(cfg.Scopes) == 0 {
			cfg.Scopes = []string{"identify", "email"}
		}
		if strings.TrimSpace(cfg.SubjectField) == "" {
			cfg.SubjectField = "id"
		}
		if strings.TrimSpace(cfg.EmailField) == "" {
			cfg.EmailField = "email"
		}
		if strings.TrimSpace(cfg.EmailVerifiedField) == "" {
			cfg.EmailVerifiedField = "verified"
		}
		if strings.TrimSpace(cfg.DisplayNameField) == "" {
			cfg.DisplayNameField = "global_name"
		}
	}
	return cfg, nil
}

func (p *OAuthHTTPProvider) qqIdentity(ctx context.Context, accessToken string) (VerifiedIdentity, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return VerifiedIdentity{}, ErrSocialLoginRejected
	}
	openID, err := p.qqOpenID(ctx, accessToken)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	var profile struct {
		Ret      int    `json:"ret"`
		Msg      string `json:"msg"`
		Nickname string `json:"nickname"`
	}
	userURL, err := url.Parse(p.cfg.UserURL)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	q := userURL.Query()
	q.Set("access_token", accessToken)
	q.Set("oauth_consumer_key", p.cfg.ClientID)
	q.Set("openid", openID)
	userURL.RawQuery = q.Encode()
	if err := p.getURLJSON(ctx, userURL.String(), &profile); err != nil {
		return VerifiedIdentity{}, err
	}
	if profile.Ret != 0 {
		logOAuthProviderUpstreamError(ctx, p.cfg.Provider, http.StatusOK, oauthProviderErrorDetails{
			Code: strconv.Itoa(profile.Ret), Message: profile.Msg,
		})
		return VerifiedIdentity{}, ErrSocialLoginRejected
	}
	return VerifiedIdentity{
		Provider:      SocialProviderQQ,
		Subject:       openID,
		Email:         syntheticOAuthEmail(SocialProviderQQ, openID),
		DisplayName:   strings.TrimSpace(profile.Nickname),
		EmailVerified: false,
	}, nil
}

func (p *OAuthHTTPProvider) qqOpenID(ctx context.Context, accessToken string) (string, error) {
	openIDURL, err := url.Parse(p.cfg.OpenIDURL)
	if err != nil {
		return "", err
	}
	q := openIDURL.Query()
	q.Set("access_token", accessToken)
	openIDURL.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openIDURL.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		logOAuthProviderUpstreamError(ctx, p.cfg.Provider, res.StatusCode,
			oauthProviderErrorFromJSON(body, []string{"error", "ret", "code"}, []string{"error_description", "msg", "message"}))
		return "", ErrSocialLoginRejected
	}
	raw, err := unwrapQQCallbackJSON(body)
	if err != nil {
		return "", err
	}
	var payload struct {
		ClientID string `json:"client_id"`
		OpenID   string `json:"openid"`
		Error    int    `json:"error"`
		ErrorMsg string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	if payload.Error != 0 || strings.TrimSpace(payload.OpenID) == "" {
		logOAuthProviderUpstreamError(ctx, p.cfg.Provider, http.StatusOK, oauthProviderErrorDetails{
			Code: strconv.Itoa(payload.Error), Message: payload.ErrorMsg,
		})
		return "", ErrSocialLoginRejected
	}
	if payload.ClientID != "" && payload.ClientID != p.cfg.ClientID {
		return "", ErrSocialLoginRejected
	}
	return strings.TrimSpace(payload.OpenID), nil
}

func unwrapQQCallbackJSON(raw []byte) ([]byte, error) {
	s := strings.TrimSpace(strings.TrimSuffix(string(raw), ";"))
	if strings.HasPrefix(s, "callback") {
		start := strings.Index(s, "(")
		end := strings.LastIndex(s, ")")
		if start < 0 || end <= start {
			return nil, ErrSocialLoginRejected
		}
		s = s[start+1 : end]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrSocialLoginRejected
	}
	return []byte(s), nil
}

func (p *OAuthHTTPProvider) dingTalkIdentity(ctx context.Context, flow OAuthFlowSession, code string) (VerifiedIdentity, error) {
	token, err := p.exchangeDingTalkCode(ctx, flow, code)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	var profile struct {
		UnionID       string `json:"unionId"`
		OpenID        string `json:"openId"`
		Nick          string `json:"nick"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"emailVerified"`
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.UserURL, nil)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)
	res, err := p.client.Do(req)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return VerifiedIdentity{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		logOAuthProviderUpstreamError(ctx, p.cfg.Provider, res.StatusCode,
			oauthProviderErrorFromJSON(body, []string{"code", "error"}, []string{"message", "error_description"}))
		return VerifiedIdentity{}, ErrSocialLoginRejected
	}
	if err := json.Unmarshal(body, &profile); err != nil {
		return VerifiedIdentity{}, err
	}
	subject := strings.TrimSpace(profile.UnionID)
	if subject == "" {
		return VerifiedIdentity{}, ErrSocialLoginRejected
	}
	email := NormalizeEmail(profile.Email)
	verified := profile.EmailVerified && email != ""
	if email == "" {
		email = syntheticOAuthEmail(SocialProviderDingTalk, subject)
	}
	return VerifiedIdentity{
		Provider:      SocialProviderDingTalk,
		Subject:       subject,
		Email:         email,
		DisplayName:   strings.TrimSpace(profile.Nick),
		EmailVerified: verified,
	}, nil
}

func (p *OAuthHTTPProvider) exchangeDingTalkCode(ctx context.Context, _ OAuthFlowSession, code string) (string, error) {
	payload := map[string]string{
		"clientId":     p.cfg.ClientID,
		"clientSecret": p.cfg.ClientSecret,
		"code":         strings.TrimSpace(code),
		"grantType":    "authorization_code",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.TokenURL, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var out struct {
		AccessToken string `json:"accessToken"`
		ErrorCode   string `json:"code"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 ||
		strings.TrimSpace(out.AccessToken) == "" ||
		strings.TrimSpace(out.ErrorCode) != "" {
		logOAuthProviderUpstreamError(ctx, p.cfg.Provider, res.StatusCode, oauthProviderErrorDetails{
			Code: out.ErrorCode, Message: out.Message,
		})
		return "", ErrSocialLoginRejected
	}
	return strings.TrimSpace(out.AccessToken), nil
}

func (p *OAuthHTTPProvider) genericUserInfoIdentity(ctx context.Context, accessToken string) (VerifiedIdentity, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return VerifiedIdentity{}, ErrSocialLoginRejected
	}
	var raw map[string]any
	if err := p.getBearerJSON(ctx, p.cfg.UserURL, accessToken, &raw); err != nil {
		return VerifiedIdentity{}, err
	}
	if err := p.requireMinimumNumericClaim(raw); err != nil {
		return VerifiedIdentity{}, err
	}
	subject := stringField(raw, p.cfg.SubjectField)
	if subject == "" {
		return VerifiedIdentity{}, ErrSocialLoginRejected
	}
	email := NormalizeEmail(stringField(raw, p.cfg.EmailField))
	verified := boolField(raw, p.cfg.EmailVerifiedField) && email != ""
	if email == "" {
		email = syntheticOAuthEmail(p.cfg.Provider, subject)
	}
	return VerifiedIdentity{
		Provider:      p.cfg.Provider,
		Subject:       subject,
		Email:         email,
		DisplayName:   p.genericDisplayName(raw),
		EmailVerified: verified,
	}, nil
}

func (p *OAuthHTTPProvider) requireMinimumNumericClaim(raw map[string]any) error {
	field := strings.TrimSpace(p.cfg.MinimumNumericClaimField)
	if field == "" {
		return nil
	}
	value, ok := mapField(raw, field)
	if !ok {
		return ErrSocialLoginRejected
	}
	n, ok := int64ClaimValue(value)
	if !ok || n < p.cfg.MinimumNumericClaimValue {
		return ErrSocialLoginRejected
	}
	return nil
}

func (p *OAuthHTTPProvider) genericDisplayName(raw map[string]any) string {
	display := stringField(raw, p.cfg.DisplayNameField)
	if display == "" && p.cfg.Provider == SocialProviderDiscord {
		display = stringField(raw, "username")
	}
	return display
}

func (p *OAuthHTTPProvider) getURLJSON(ctx context.Context, endpoint string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
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
			oauthProviderErrorFromJSON(body, []string{"error", "ret", "code"}, []string{"error_description", "msg", "message"}))
		return ErrSocialLoginRejected
	}
	return json.Unmarshal(body, dst)
}

type oauthProviderErrorDetails struct {
	Code    string
	Message string
}

func logOAuthProviderUpstreamError(ctx context.Context, provider string, status int, details oauthProviderErrorDetails) {
	slog.WarnContext(ctx, "oauth provider upstream rejected social login",
		slog.String("provider", normalizeSocialProvider(provider)),
		slog.Int("status", status),
		slog.String("errcode", strings.TrimSpace(details.Code)),
		slog.String("errmsg", strings.TrimSpace(details.Message)),
	)
}

func oauthProviderErrorFromJSON(body []byte, codeFields, messageFields []string) oauthProviderErrorDetails {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || !bytes.HasPrefix(body, []byte("{")) {
		return oauthProviderErrorDetails{}
	}
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return oauthProviderErrorDetails{}
	}
	return oauthProviderErrorDetails{
		Code:    firstOAuthProviderErrorField(raw, codeFields),
		Message: firstOAuthProviderErrorField(raw, messageFields),
	}
}

func firstOAuthProviderErrorField(raw map[string]any, fields []string) string {
	for _, field := range fields {
		if value := stringField(raw, field); value != "" {
			return value
		}
	}
	return ""
}

func stringField(m map[string]any, field string) string {
	value, ok := mapField(m, field)
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
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
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func boolField(m map[string]any, field string) bool {
	value, ok := mapField(m, field)
	if !ok || value == nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		out, _ := strconv.ParseBool(strings.TrimSpace(v))
		return out
	case json.Number:
		i, err := v.Int64()
		return err == nil && i != 0
	case float64:
		return v != 0
	default:
		return false
	}
}

func int64ClaimValue(value any) (int64, bool) {
	switch v := value.(type) {
	case json.Number:
		i, err := v.Int64()
		return i, err == nil
	case float64:
		i := int64(v)
		if v != float64(i) {
			return 0, false
		}
		return i, true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return i, err == nil
	case int:
		return int64(v), true
	case int64:
		return v, true
	default:
		return 0, false
	}
}

func mapField(m map[string]any, field string) (any, bool) {
	field = strings.TrimSpace(field)
	if field == "" {
		return nil, false
	}
	var current any = m
	for _, part := range strings.Split(field, ".") {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = obj[strings.TrimSpace(part)]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func syntheticOAuthEmail(provider, subject string) string {
	return SyntheticOAuthEmail(provider, subject)
}

func SyntheticOAuthEmail(provider, subject string) string {
	provider = normalizeSocialProvider(provider)
	sum := sha256.Sum256([]byte(provider + ":" + strings.TrimSpace(subject)))
	local := strings.ToLower(base64.RawURLEncoding.EncodeToString(sum[:12]))
	return provider + "+" + local + "@oauth.local"
}
