package anthropicoauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const (
	AnthropicRefreshTokenURL = "https://console.anthropic.com/v1/oauth/token"

	defaultRefreshTimeout    = 10 * time.Second
	defaultRetryAfter        = time.Minute
	defaultMaxRetryAfter     = time.Hour
	defaultExpirySkewGrace   = 30 * time.Second
	defaultAccessTokenTTL    = time.Hour
	failureAuthExpired       = "auth_expired"
	failureRateLimitExceeded = "rate_limit_exceeded"
	failureNonRetryable      = "non_retryable"
	failureTemporary         = "temporary"
	failurePayloadInvalid    = "payload_invalid"
)

var (
	ErrAnthropicAuthExpired  = errors.New("anthropicoauth: authorization expired")
	ErrAnthropicRateLimited  = errors.New("anthropicoauth: rate limit exceeded")
	ErrAnthropicNonRetryable = errors.New("anthropicoauth: non-retryable refresh failure")
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

type RefreshFallback interface {
	Refresh(context.Context, int64) error
}

type ProviderAwareRefreshFallback interface {
	RefreshForProvider(context.Context, int64, int64) error
}

type Refresher struct {
	Store           RefreshStore
	Fallback        RefreshFallback
	Endpoint        string
	ClientID        string
	HTTPClient      *http.Client
	Now             func() time.Time
	ExpirySkewGrace time.Duration
	RetryAfterMax   time.Duration
}

type Option func(*Refresher)

func NewRefresher(store *credentialstore.Store, opts ...Option) *Refresher {
	r := &Refresher{Store: credentialStoreRefreshAdapter{store: store}}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

func WithFallbackRefresher(fallback RefreshFallback) Option {
	return func(r *Refresher) { r.Fallback = fallback }
}

func WithHTTPClient(client *http.Client) Option {
	return func(r *Refresher) { r.HTTPClient = client }
}

func WithEndpoint(endpoint string) Option {
	return func(r *Refresher) { r.Endpoint = endpoint }
}

func WithNow(now func() time.Time) Option {
	return func(r *Refresher) { r.Now = now }
}

func (r *Refresher) Refresh(ctx context.Context, accountID int64) error {
	return r.refresh(ctx, 0, accountID)
}

func (r *Refresher) RefreshForProvider(ctx context.Context, providerID, accountID int64) error {
	return r.refresh(ctx, providerID, accountID)
}

func (r *Refresher) refresh(ctx context.Context, providerID, accountID int64) error {
	if r == nil || r.Store == nil {
		if r != nil && r.Fallback != nil {
			if aware, ok := r.Fallback.(ProviderAwareRefreshFallback); ok {
				return aware.RefreshForProvider(ctx, providerID, accountID)
			}
			return r.Fallback.Refresh(ctx, accountID)
		}
		return errors.New("anthropicoauth: credential refresh store missing")
	}
	probe, err := r.Store.LoadForRefresh(ctx, accountID)
	if err != nil {
		return err
	}
	defer privacy.Zeroize(probe.PlaintextPayload)
	if !isAnthropicOAuthRecord(probe) {
		return r.refreshFallback(ctx, providerID, accountID)
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

func (r *Refresher) refreshFallback(ctx context.Context, providerID, accountID int64) error {
	if r == nil || r.Fallback == nil {
		return nil
	}
	if aware, ok := r.Fallback.(ProviderAwareRefreshFallback); ok {
		return aware.RefreshForProvider(ctx, providerID, accountID)
	}
	return r.Fallback.Refresh(ctx, accountID)
}

func (r *Refresher) refreshLockedRecord(ctx context.Context, txStore RefreshTxStore, accountID int64, rec credentialstore.CredentialRecord) (error, error) {
	if !isAnthropicOAuthRecord(rec) {
		return nil, nil
	}
	result, err := r.refreshCredential(ctx, accountID, rec.PlaintextPayload)
	if err != nil {
		failureClass := auth.RefreshFailureAuditOutcome(
			auth.ClassifyRefreshError(err, "anthropic", refreshErrorStatusCode(err)),
			classifyRefreshFailure(err),
		)
		nextAttempt := nextAttemptForRefreshError(err, r.now())
		if saveErr := txStore.SaveRefreshFailure(ctx, rec, failureClass, nextAttempt); saveErr != nil {
			return nil, saveErr
		}
		return auth.WithRefreshAuditOutcome(err, failureClass), nil
	}
	if err := txStore.SaveRefreshSuccess(ctx, rec, result.payload, result.expiresAt, "refresh_succeeded"); err != nil {
		return nil, err
	}
	return nil, nil
}

type refreshResult struct {
	payload   []byte
	expiresAt time.Time
}

func (r *Refresher) refreshCredential(ctx context.Context, accountID int64, currentCredential []byte) (refreshResult, error) {
	cred, err := parseCredentialPayload(currentCredential)
	if err != nil {
		return refreshResult{}, fmt.Errorf("anthropicoauth refresh account %d: %w", accountID, err)
	}
	refreshToken := mapString(cred, "refresh_token")
	if refreshToken == "" {
		return refreshResult{}, fmt.Errorf("anthropicoauth refresh account %d: %w: refresh_token is empty", accountID, credentialstore.ErrInvalidPayload)
	}
	clientID := firstNonEmpty(r.ClientID, mapString(cred, "client_id"), AnthropicPublicCLIClientID)
	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     clientID,
	})
	if err != nil {
		return refreshResult{}, err
	}
	token, err := r.postRefresh(ctx, body)
	if err != nil {
		return refreshResult{}, fmt.Errorf("anthropicoauth refresh account %d: %w", accountID, err)
	}
	accessToken := strings.TrimSpace(token.AccessToken)
	if accessToken == "" {
		return refreshResult{}, fmt.Errorf("anthropicoauth refresh account %d: %w: missing access_token", accountID, credentialstore.ErrInvalidPayload)
	}
	now := r.now()
	expiresAt, err := r.resolveExpiresAt(now, token)
	if err != nil {
		return refreshResult{}, fmt.Errorf("anthropicoauth refresh account %d: %w", accountID, err)
	}
	cred["access_token"] = accessToken
	cred["expires_at"] = expiresAt.Format(time.RFC3339)
	if refresh := strings.TrimSpace(token.RefreshToken); refresh != "" {
		cred["refresh_token"] = refresh
	}
	if idToken := strings.TrimSpace(token.IDToken); idToken != "" {
		cred["id_token"] = idToken
	}
	if email := firstNonEmpty(token.Email, token.Account.EmailAddress); email != "" {
		cred["email"] = email
	}
	if tokenType := strings.TrimSpace(token.TokenType); tokenType != "" {
		cred["token_type"] = tokenType
	}
	if scope := strings.TrimSpace(token.Scope); scope != "" {
		cred["scope"] = scope
	}
	cred["client_id"] = clientID
	out, err := json.Marshal(cred)
	if err != nil {
		return refreshResult{}, fmt.Errorf("anthropicoauth refresh account %d: marshal refreshed payload: %w", accountID, err)
	}
	return refreshResult{payload: out, expiresAt: expiresAt}, nil
}

func (r *Refresher) postRefresh(ctx context.Context, body []byte) (tokenResponse, error) {
	reqCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		reqCtx, cancel = context.WithTimeout(ctx, defaultRefreshTimeout)
	}
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, firstNonEmpty(r.Endpoint, AnthropicRefreshTokenURL), bytes.NewReader(body))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.httpClient().Do(req)
	if err != nil {
		return tokenResponse{}, &RefreshError{Class: failureTemporary, Retryable: true, Cause: err}
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, &RefreshError{Class: failureTemporary, Retryable: true, Cause: err}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return tokenResponse{}, classifyHTTPRefreshError(resp.StatusCode, resp.Header, respBody, r.now(), r.maxRetryAfter())
	}
	var decoded tokenResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return tokenResponse{}, fmt.Errorf("%w: token response decode failed: %v", credentialstore.ErrInvalidPayload, err)
	}
	return decoded, nil
}

func classifyHTTPRefreshError(status int, header http.Header, body []byte, now time.Time, maxRetryAfter time.Duration) error {
	code := oauthErrorCode(body)
	bodyRedacted := refreshErrorBody(body)
	switch {
	case status == http.StatusTooManyRequests:
		return &RefreshError{
			Class:      failureRateLimitExceeded,
			StatusCode: status,
			Retryable:  false,
			RetryAfter: now.Add(parseRetryAfter(header, now, maxRetryAfter)),
			Body:       bodyRedacted,
			Cause:      ErrAnthropicRateLimited,
		}
	case code == "invalid_grant" && (status == http.StatusUnauthorized || status == http.StatusBadRequest):
		return &RefreshError{Class: failureAuthExpired, StatusCode: status, Retryable: false, Body: bodyRedacted, Cause: ErrAnthropicAuthExpired}
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError:
		return &RefreshError{Class: failureNonRetryable, StatusCode: status, Retryable: false, Body: bodyRedacted, Cause: ErrAnthropicNonRetryable}
	default:
		return &RefreshError{Class: failureTemporary, StatusCode: status, Retryable: true, Body: bodyRedacted}
	}
}

type RefreshError struct {
	Class      string
	StatusCode int
	Retryable  bool
	RetryAfter time.Time
	Body       string
	Cause      error
}

func (e *RefreshError) Error() string {
	if e == nil {
		return "anthropicoauth: refresh error"
	}
	status := ""
	if e.StatusCode > 0 {
		status = fmt.Sprintf(" status=%d", e.StatusCode)
	}
	body := ""
	if e.Body != "" {
		body = fmt.Sprintf(" body=%q", e.Body)
	}
	return fmt.Sprintf("anthropicoauth: refresh failed class=%s%s%s", e.Class, status, body)
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

func isAnthropicOAuthRecord(rec credentialstore.CredentialRecord) bool {
	if credentialstore.Normalize(rec.Vendor) != credentialstore.VendorAnthropic {
		return false
	}
	switch credentialstore.Normalize(rec.AuthMode) {
	case credentialstore.AuthModeClaudeAIOAuth, credentialstore.AuthModeClaudeCode:
		return true
	default:
		return false
	}
}

func classifyRefreshFailure(err error) string {
	var refreshErr *RefreshError
	if errors.As(err, &refreshErr) && refreshErr.Class != "" {
		return refreshErr.Class
	}
	if errors.Is(err, credentialstore.ErrInvalidPayload) {
		return failurePayloadInvalid
	}
	return failureTemporary
}

func refreshErrorStatusCode(err error) int {
	var refreshErr *RefreshError
	if errors.As(err, &refreshErr) {
		return refreshErr.StatusCode
	}
	return 0
}

func refreshErrorBody(body []byte) string {
	const max = 512
	text := strings.TrimSpace(string(body))
	if len(text) > max {
		text = text[:max]
	}
	return auth.SanitizeOAuthMessage(text)
}

func nextAttemptForRefreshError(err error, now time.Time) time.Time {
	var refreshErr *RefreshError
	if errors.As(err, &refreshErr) && refreshErr.RetryAfter.After(now) {
		return refreshErr.RetryAfter.UTC()
	}
	return now.Add(defaultRetryAfter).UTC()
}

func (r *Refresher) resolveExpiresAt(now time.Time, token tokenResponse) (time.Time, error) {
	now = now.UTC()
	if token.ExpiresIn > 0 {
		return now.Add(time.Duration(token.ExpiresIn) * time.Second), nil
	}
	if parsed := parseExpiresAt(token.ExpiresAt); !parsed.IsZero() {
		if parsed.Before(now) {
			if now.Sub(parsed) <= r.expirySkewGrace() {
				return now.Add(r.expirySkewGrace()), nil
			}
			return time.Time{}, fmt.Errorf("%w: token expires_at is stale", credentialstore.ErrInvalidPayload)
		}
		return parsed, nil
	}
	return now.Add(defaultAccessTokenTTL), nil
}

func (r *Refresher) httpClient() *http.Client {
	if r != nil && r.HTTPClient != nil {
		return r.HTTPClient
	}
	return http.DefaultClient
}

func (r *Refresher) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Refresher) expirySkewGrace() time.Duration {
	if r != nil && r.ExpirySkewGrace > 0 {
		return r.ExpirySkewGrace
	}
	return defaultExpirySkewGrace
}

func (r *Refresher) maxRetryAfter() time.Duration {
	if r != nil && r.RetryAfterMax > 0 {
		return r.RetryAfterMax
	}
	return defaultMaxRetryAfter
}

func parseRetryAfter(header http.Header, now time.Time, maxDelay time.Duration) time.Duration {
	delay := defaultRetryAfter
	if raw := strings.TrimSpace(header.Get("Retry-After")); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
			delay = time.Duration(seconds) * time.Second
		} else if when, err := http.ParseTime(raw); err == nil {
			delay = when.Sub(now)
		}
	}
	if raw := strings.TrimSpace(header.Get("Retry-After-Ms")); raw != "" {
		if ms, err := strconv.ParseInt(raw, 10, 64); err == nil {
			delay = time.Duration(ms) * time.Millisecond
		}
	}
	if delay <= 0 {
		delay = defaultRetryAfter
	}
	if maxDelay > 0 && delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

func oauthErrorCode(body []byte) string {
	var decoded struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &decoded); err == nil && decoded.Error != "" {
		return strings.ToLower(strings.TrimSpace(decoded.Error))
	}
	text := strings.ToLower(string(body))
	switch {
	case strings.Contains(text, "invalid_grant"):
		return "invalid_grant"
	case strings.Contains(text, "rate_limit_exceeded"):
		return "rate_limit_exceeded"
	default:
		return ""
	}
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

func mapString(values map[string]any, key string) string {
	switch v := values[key].(type) {
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

type credentialStoreRefreshAdapter struct {
	store *credentialstore.Store
}

func (s credentialStoreRefreshAdapter) LoadForRefresh(ctx context.Context, accountID int64) (credentialstore.CredentialRecord, error) {
	if s.store == nil {
		return credentialstore.CredentialRecord{}, errors.New("anthropicoauth: credential store missing")
	}
	return s.store.LoadForRefresh(ctx, accountID)
}

func (s credentialStoreRefreshAdapter) WithRefreshTransaction(ctx context.Context, fn func(RefreshTxStore, db.DBTX) error) error {
	if s.store == nil {
		return errors.New("anthropicoauth: credential store missing")
	}
	return s.store.WithTransaction(ctx, func(txStore *credentialstore.Store, tx db.DBTX) error {
		if fn == nil {
			return nil
		}
		return fn(txStore, tx)
	})
}
