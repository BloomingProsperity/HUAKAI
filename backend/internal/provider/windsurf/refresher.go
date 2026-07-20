package windsurf

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
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const (
	defaultRefreshTimeout = 10 * time.Second
	defaultRetryAfter     = time.Minute
	defaultMaxRetryAfter  = time.Hour
	defaultAccessTokenTTL = time.Hour
)

var (
	ErrWindsurfAuthExpired = errors.New("windsurf refresh: authorization expired")
	ErrWindsurfRateLimited = errors.New("windsurf refresh: rate limit exceeded")
	ErrWindsurfTransient   = errors.New("windsurf refresh: transient upstream failure")
)

type RefreshAdapter struct {
	TokenURL   string
	ClientID   string
	Scope      string
	HTTPClient *http.Client
	Now        func() time.Time
}

func (a RefreshAdapter) RefreshForProvider(ctx context.Context, accountID int64, _ string, currentCredential []byte) ([]byte, time.Time, error) {
	cred, err := parseCredentialPayload(currentCredential)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("windsurf refresh account %d: %w", accountID, err)
	}
	refreshToken := credentialString(cred, "refresh_token")
	if refreshToken == "" {
		return nil, time.Time{}, fmt.Errorf("windsurf refresh account %d: %w: refresh_token is empty", accountID, credentialstore.ErrInvalidPayload)
	}
	tokenURL := strings.TrimSpace(a.TokenURL)
	if tokenURL == "" {
		return nil, time.Time{}, fmt.Errorf("windsurf refresh account %d: %w: token_url", accountID, ErrWindsurfOAuthConfigRequired)
	}
	clientID := strings.TrimSpace(a.ClientID)
	if clientID == "" {
		return nil, time.Time{}, fmt.Errorf("windsurf refresh account %d: %w: client_id", accountID, ErrWindsurfOAuthConfigRequired)
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	if scope := strings.TrimSpace(a.Scope); scope != "" {
		form.Set("scope", scope)
	}
	token, err := a.postRefresh(ctx, tokenURL, form)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("windsurf refresh account %d: %w", accountID, err)
	}
	payload, expiresAt, err := a.mergeToken(cred, token, clientID)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("windsurf refresh account %d: %w", accountID, err)
	}
	return payload, expiresAt, nil
}

func (a RefreshAdapter) postRefresh(ctx context.Context, tokenURL string, form url.Values) (tokenResponse, error) {
	reqCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		reqCtx, cancel = context.WithTimeout(ctx, defaultRefreshTimeout)
	}
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.httpClient().Do(req)
	if err != nil {
		return tokenResponse{}, &RefreshError{Outcome: string(auth.OutcomeTransientError), Retryable: true, Cause: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, &RefreshError{Outcome: string(auth.OutcomeTransientError), Retryable: true, Cause: err}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return tokenResponse{}, classifyHTTPRefreshError(resp.StatusCode, resp.Header, body, a.now())
	}
	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return tokenResponse{}, fmt.Errorf("%w: token response decode failed: %v", credentialstore.ErrInvalidPayload, err)
	}
	return token, nil
}

func (a RefreshAdapter) mergeToken(cred map[string]any, token tokenResponse, clientID string) ([]byte, time.Time, error) {
	accessToken := strings.TrimSpace(token.AccessToken)
	if accessToken == "" {
		return nil, time.Time{}, fmt.Errorf("%w: token response missing access_token", credentialstore.ErrInvalidPayload)
	}
	expiresAt, err := resolveTokenExpiry(a.now(), token)
	if err != nil {
		return nil, time.Time{}, err
	}
	cred["access_token"] = accessToken
	cred["session_token"] = accessToken
	cred["expires_at"] = expiresAt.Format(time.RFC3339)
	cred["client_id"] = clientID
	if refresh := strings.TrimSpace(token.RefreshToken); refresh != "" {
		cred["refresh_token"] = refresh
	}
	if tokenType := strings.TrimSpace(token.TokenType); tokenType != "" {
		cred["token_type"] = tokenType
	}
	if scope := strings.TrimSpace(token.Scope); scope != "" {
		cred["scope"] = scope
	}
	if idToken := strings.TrimSpace(token.IDToken); idToken != "" {
		cred["id_token"] = idToken
	}
	out, err := json.Marshal(cred)
	return out, expiresAt, err
}

func (a RefreshAdapter) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	// S2-054: 兜底不用裸 http.DefaultClient——vendor refresher 把 refresh token(kiro/gemini 还含 client
	// secret)POST 到 operator 配置的 token endpoint,裸 client 无拨号层 IP 校验,会被 DNS-rebind/内网地址
	// 骗到本机或元数据服务。与已修的 gemini adapter 一致,兜底改用 SSRF-protected client。
	return auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
}

func (a RefreshAdapter) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

type RefreshError struct {
	Outcome    string
	StatusCode int
	Retryable  bool
	RetryAfter time.Time
	Cause      error
}

func (e *RefreshError) Error() string {
	if e == nil {
		return "windsurf refresh: failed"
	}
	status := ""
	if e.StatusCode > 0 {
		status = fmt.Sprintf(" status=%d", e.StatusCode)
	}
	return fmt.Sprintf("windsurf refresh: failed outcome=%s%s", e.Outcome, status)
}

func (e *RefreshError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *RefreshError) RetryableRefresh() bool {
	return e != nil && e.Retryable
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	ExpiresAt    string `json:"expires_at"`
	Scope        string `json:"scope"`
}

func classifyHTTPRefreshError(status int, header http.Header, body []byte, now time.Time) error {
	code := oauthErrorCode(body)
	switch {
	case status == http.StatusTooManyRequests:
		return &RefreshError{
			Outcome:    string(auth.OutcomeRateLimit),
			StatusCode: status,
			Retryable:  false,
			RetryAfter: now.Add(parseRetryAfter(header, now)),
			Cause:      ErrWindsurfRateLimited,
		}
	case status >= http.StatusInternalServerError && status <= 599:
		return &RefreshError{Outcome: string(auth.OutcomeTransientError), StatusCode: status, Retryable: true, Cause: ErrWindsurfTransient}
	case status == http.StatusUnauthorized || code == "invalid_grant":
		return &RefreshError{Outcome: string(auth.OutcomeAuthExpired), StatusCode: status, Retryable: false, Cause: ErrWindsurfAuthExpired}
	default:
		return &RefreshError{Outcome: string(auth.ClassifyRefreshError(errors.New(string(body)), WindsurfVendor, status)), StatusCode: status, Retryable: false}
	}
}

func oauthErrorCode(body []byte) string {
	var decoded struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &decoded); err == nil {
		if code := strings.ToLower(strings.TrimSpace(decoded.Error)); code != "" {
			return code
		}
		if strings.Contains(strings.ToLower(decoded.ErrorDescription), "invalid_grant") {
			return "invalid_grant"
		}
	}
	text := strings.ToLower(string(body))
	if strings.Contains(text, "invalid_grant") || strings.Contains(text, "invalid grant") {
		return "invalid_grant"
	}
	return ""
}

func parseRetryAfter(header http.Header, now time.Time) time.Duration {
	delay := defaultRetryAfter
	if raw := strings.TrimSpace(header.Get("Retry-After")); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
			delay = time.Duration(seconds) * time.Second
		} else if when, err := http.ParseTime(raw); err == nil {
			delay = when.Sub(now)
		}
	}
	if delay <= 0 {
		delay = defaultRetryAfter
	}
	if delay > defaultMaxRetryAfter {
		delay = defaultMaxRetryAfter
	}
	return delay
}

func resolveTokenExpiry(now time.Time, token tokenResponse) (time.Time, error) {
	now = now.UTC()
	if token.ExpiresIn > 0 {
		return now.Add(time.Duration(token.ExpiresIn) * time.Second), nil
	}
	if raw := strings.TrimSpace(token.ExpiresAt); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil && parsed.After(now) {
			return parsed.UTC(), nil
		}
	}
	return now.Add(defaultAccessTokenTTL), nil
}

func parseCredentialPayload(raw []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("%w: empty credential payload", credentialstore.ErrInvalidPayload)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var cred map[string]any
	if err := decoder.Decode(&cred); err != nil {
		return nil, fmt.Errorf("%w: %v", credentialstore.ErrInvalidPayload, err)
	}
	if cred == nil {
		return nil, fmt.Errorf("%w: credential payload is not an object", credentialstore.ErrInvalidPayload)
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
