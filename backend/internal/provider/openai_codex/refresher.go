package openai_codex

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
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const (
	defaultRefreshTimeout = 10 * time.Second
	defaultRetryAfter     = time.Minute
	defaultMaxRetryAfter  = time.Hour
	defaultAccessTokenTTL = time.Hour
	maxRefreshErrorBody   = 1024
	refreshSucceeded      = "refresh_succeeded"
	failurePayloadInvalid = "payload_invalid"
	failureUnknown        = "unknown"
	failureVendorMismatch = "provider_mismatch"
)

var (
	ErrOpenAICodexStoreMissing   = errors.New("openai codex refresh: credential store missing")
	ErrOpenAICodexAuthExpired    = errors.New("openai codex refresh: authorization expired")
	ErrOpenAICodexRateLimited    = errors.New("openai codex refresh: rate limit exceeded")
	ErrOpenAICodexRiskControl    = errors.New("openai codex refresh: risk control triggered")
	ErrOpenAICodexTransient      = errors.New("openai codex refresh: transient upstream failure")
	ErrOpenAICodexRecordMismatch = errors.New("openai codex refresh: credential record is not codex oauth")
)

type RefreshTxStore interface {
	LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error)
	SaveRefreshSuccess(context.Context, credentialstore.CredentialRecord, []byte, time.Time, string) error
	SaveRefreshFailure(context.Context, credentialstore.CredentialRecord, string, time.Time) error
}

type RefreshStore interface {
	LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error)
	WithRefreshTransaction(context.Context, func(RefreshTxStore, db.DBTX) error) error
}

type Refresher struct {
	Store   RefreshStore
	Adapter RefreshAdapter
	Now     func() time.Time
}

type VendorRefresher = Refresher

type Option func(*Refresher)

func NewVendorRefresher(store *credentialstore.Store, cfg credentialacq.OAuthClientConfig, opts ...Option) (*VendorRefresher, error) {
	adapter, err := RefreshAdapterFromOAuthConfig(cfg)
	if err != nil {
		return nil, err
	}
	all := append([]Option{WithRefreshAdapter(adapter)}, opts...)
	return NewRefresher(store, all...), nil
}

func WithRefreshAdapter(adapter RefreshAdapter) Option {
	return func(r *Refresher) { r.Adapter = adapter }
}

func WithOAuthConfig(cfg credentialacq.OAuthClientConfig) Option {
	return func(r *Refresher) {
		r.Adapter.TokenURL = strings.TrimSpace(cfg.TokenURL)
		r.Adapter.ClientID = strings.TrimSpace(cfg.ClientID)
		r.Adapter.Scope = scopeString(cfg)
		if cfg.HTTPClient != nil {
			r.Adapter.HTTPClient = cfg.HTTPClient
		}
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(r *Refresher) { r.Adapter.HTTPClient = client }
}

func WithTokenURL(tokenURL string) Option {
	return func(r *Refresher) { r.Adapter.TokenURL = tokenURL }
}

func WithClientID(clientID string) Option {
	return func(r *Refresher) { r.Adapter.ClientID = clientID }
}

func WithScope(scope string) Option {
	return func(r *Refresher) { r.Adapter.Scope = scope }
}

func WithNow(now func() time.Time) Option {
	return func(r *Refresher) {
		r.Now = now
		r.Adapter.Now = now
	}
}

func (r *Refresher) Refresh(ctx context.Context, accountID int64) error {
	return r.refresh(ctx, 0, accountID)
}

func (r *Refresher) RefreshForProvider(ctx context.Context, _ int64, accountID int64) error {
	return r.refresh(ctx, 0, accountID)
}

func (r *Refresher) refresh(ctx context.Context, _ int64, accountID int64) error {
	if r == nil || r.Store == nil {
		return ErrOpenAICodexStoreMissing
	}
	probe, err := r.Store.LoadForRefresh(ctx, accountID)
	if err != nil {
		return err
	}
	defer privacy.Zeroize(probe.PlaintextPayload)
	if !isOpenAICodexOAuthRecord(probe) {
		return fmt.Errorf("%w: vendor=%s auth_mode=%s account_id=%d", ErrOpenAICodexRecordMismatch, probe.Vendor, probe.AuthMode, accountID)
	}

	var refreshErr error
	txErr := r.Store.WithRefreshTransaction(ctx, func(txStore RefreshTxStore, tx db.DBTX) error {
		return credentialacq.WithRefreshLock(ctx, tx, probe.ID, func(db.DBTX) error {
			rec, err := txStore.LoadForRefresh(ctx, accountID)
			if err != nil {
				return err
			}
			defer privacy.Zeroize(rec.PlaintextPayload)
			if rec.ID != probe.ID {
				return credentialacq.WithRefreshLock(ctx, tx, rec.ID, func(db.DBTX) error {
					lockedRec, err := txStore.LoadForRefresh(ctx, accountID)
					if err != nil {
						return err
					}
					defer privacy.Zeroize(lockedRec.PlaintextPayload)
					refreshErr, err = r.refreshLockedRecord(ctx, txStore, accountID, lockedRec)
					return err
				})
			}
			refreshErr, err = r.refreshLockedRecord(ctx, txStore, accountID, rec)
			return err
		})
	})
	return errors.Join(refreshErr, txErr)
}

func (r *Refresher) refreshLockedRecord(ctx context.Context, txStore RefreshTxStore, accountID int64, rec credentialstore.CredentialRecord) (error, error) {
	if !isOpenAICodexOAuthRecord(rec) {
		err := fmt.Errorf("%w: vendor=%s auth_mode=%s account_id=%d", ErrOpenAICodexRecordMismatch, rec.Vendor, rec.AuthMode, accountID)
		if saveErr := txStore.SaveRefreshFailure(ctx, rec, failureVendorMismatch, r.now().Add(defaultRetryAfter)); saveErr != nil {
			return nil, saveErr
		}
		return err, nil
	}
	payload, expiresAt, err := r.Adapter.RefreshForProvider(ctx, accountID, OpenAICodexClassifierVendor, rec.PlaintextPayload)
	if err != nil {
		failureClass := classifyRefreshFailure(err)
		if saveErr := txStore.SaveRefreshFailure(ctx, rec, failureClass, nextAttemptForRefreshError(err, r.now())); saveErr != nil {
			return nil, saveErr
		}
		return auth.WithRefreshAuditOutcome(err, failureClass), nil
	}
	if err := txStore.SaveRefreshSuccess(ctx, rec, payload, expiresAt, refreshSucceeded); err != nil {
		return nil, err
	}
	return nil, nil
}

func (r *Refresher) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

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
		return nil, time.Time{}, fmt.Errorf("openai codex refresh account %d: %w", accountID, err)
	}
	refreshToken := credentialString(cred, "refresh_token")
	if refreshToken == "" {
		return nil, time.Time{}, fmt.Errorf("openai codex refresh account %d: %w: refresh_token is empty", accountID, credentialstore.ErrInvalidPayload)
	}
	tokenURL := strings.TrimSpace(a.TokenURL)
	if tokenURL == "" {
		return nil, time.Time{}, fmt.Errorf("openai codex refresh account %d: %w: token_url", accountID, ErrOpenAICodexOAuthConfigRequired)
	}
	clientID := strings.TrimSpace(a.ClientID)
	if clientID == "" {
		return nil, time.Time{}, fmt.Errorf("openai codex refresh account %d: %w: client_id", accountID, ErrOpenAICodexOAuthConfigRequired)
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
		return nil, time.Time{}, fmt.Errorf("openai codex refresh account %d: %w", accountID, err)
	}
	payload, expiresAt, err := a.mergeToken(cred, token, clientID)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("openai codex refresh account %d: %w", accountID, err)
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
		return tokenResponse{}, &RefreshError{Outcome: string(credentialworker.OutcomeTransientError), Retryable: true, Cause: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, &RefreshError{Outcome: string(credentialworker.OutcomeTransientError), Retryable: true, Cause: err}
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
	Body       string
	Cause      error
}

func (e *RefreshError) Error() string {
	if e == nil {
		return "openai codex refresh: failed"
	}
	status := ""
	if e.StatusCode > 0 {
		status = fmt.Sprintf(" status=%d", e.StatusCode)
	}
	body := ""
	if e.Body != "" {
		body = fmt.Sprintf(" body=%q", e.Body)
	}
	return fmt.Sprintf("openai codex refresh: failed outcome=%s%s%s", e.Outcome, status, body)
}

// RefreshFailureOutcome 返回供 credentialworker 归一化的刷新结果。
func (e *RefreshError) RefreshFailureOutcome() string {
	if e == nil {
		return ""
	}
	return e.Outcome
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
	bodyText := sanitizedRefreshBody(body)
	outcome := credentialworker.ClassifyRefreshError(errors.New(bodyText), OpenAICodexClassifierVendor, status)
	refreshErr := &RefreshError{
		Outcome:    string(outcome),
		StatusCode: status,
		Retryable:  status >= http.StatusInternalServerError && status <= 599,
		Body:       bodyText,
	}
	switch outcome {
	case credentialworker.OutcomeAuthExpired:
		refreshErr.Cause = ErrOpenAICodexAuthExpired
	case credentialworker.OutcomeRateLimit:
		refreshErr.Cause = ErrOpenAICodexRateLimited
		refreshErr.RetryAfter = now.Add(parseRetryAfter(header, now))
	case credentialworker.OutcomeRiskControl:
		refreshErr.Cause = ErrOpenAICodexRiskControl
	case credentialworker.OutcomeTransientError:
		refreshErr.Cause = ErrOpenAICodexTransient
		refreshErr.Retryable = true
	default:
		refreshErr.Outcome = string(credentialworker.OutcomeUnknown)
	}
	return refreshErr
}

func classifyRefreshFailure(err error) string {
	var refreshErr *RefreshError
	if errors.As(err, &refreshErr) && refreshErr.Outcome != "" {
		return refreshErr.Outcome
	}
	if errors.Is(err, credentialstore.ErrInvalidPayload) {
		return failurePayloadInvalid
	}
	if errors.Is(err, ErrOpenAICodexRecordMismatch) {
		return failureVendorMismatch
	}
	if outcome := credentialworker.ClassifyRefreshError(err, OpenAICodexClassifierVendor, 0); outcome != credentialworker.OutcomeUnknown {
		return string(outcome)
	}
	return failureUnknown
}

func nextAttemptForRefreshError(err error, now time.Time) time.Time {
	var refreshErr *RefreshError
	if errors.As(err, &refreshErr) && refreshErr.RetryAfter.After(now) {
		return refreshErr.RetryAfter.UTC()
	}
	return now.Add(defaultRetryAfter).UTC()
}

func sanitizedRefreshBody(body []byte) string {
	text := auth.SanitizeOAuthMessage(string(bytes.TrimSpace(body)))
	if len(text) <= maxRefreshErrorBody {
		return text
	}
	return text[:maxRefreshErrorBody] + "...<truncated>"
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

func isOpenAICodexOAuthRecord(rec credentialstore.CredentialRecord) bool {
	if credentialstore.Normalize(rec.Vendor) != credentialstore.VendorOpenAI {
		return false
	}
	mode := credentialstore.Normalize(rec.AuthMode)
	// codex_cli_oauth(device-code)与 codex_web_oauth(authorization-code/PKCE)是 Codex 凭据的两个
	// 并列获取模式,token 形状相同,均由本 codex refresher 续期。
	return mode == credentialstore.AuthModeCodexCLIOAuth || mode == credentialstore.AuthModeCodexWebOAuth
}
