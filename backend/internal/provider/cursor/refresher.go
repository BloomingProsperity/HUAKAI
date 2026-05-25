package cursor

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
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const (
	defaultRefreshTimeout = 10 * time.Second
	defaultRetryAfter     = time.Minute
	defaultMaxRetryAfter  = time.Hour
	defaultAccessTokenTTL = time.Hour
	refreshSucceeded      = "refresh_succeeded"
	failurePayloadInvalid = "payload_invalid"
	failureUnknown        = "unknown"
	failureCursorMismatch = "provider_mismatch"
	cursorCredentialMode  = "cursor_session"
	cursorOAuthModeAlias  = "cursor_oauth"
)

type RefreshOutcome string

const (
	OutcomeSuccess        RefreshOutcome = "success"
	OutcomeAuthExpired    RefreshOutcome = "auth_expired"
	OutcomeRateLimit      RefreshOutcome = "rate_limit_exceeded"
	OutcomeTransientError RefreshOutcome = "transient_error"
	OutcomeUnknown        RefreshOutcome = "unknown"
)

var (
	ErrCursorAuthExpired    = errors.New("cursor refresh: authorization expired")
	ErrCursorRateLimited    = errors.New("cursor refresh: rate limit exceeded")
	ErrCursorTransient      = errors.New("cursor refresh: transient upstream failure")
	ErrCursorRecordMismatch = errors.New("cursor refresh: credential record is not cursor oauth")
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

type Option func(*Refresher)

func WithRefreshAdapter(adapter RefreshAdapter) Option {
	return func(r *Refresher) { r.Adapter = adapter }
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
		return errors.New("cursor refresh: credential refresh store missing")
	}
	probe, err := r.Store.LoadForRefresh(ctx, accountID)
	if err != nil {
		return err
	}
	defer privacy.Zeroize(probe.PlaintextPayload)
	if !isCursorOAuthRecord(probe) {
		return fmt.Errorf("%w: vendor=%s auth_mode=%s account_id=%d", ErrCursorRecordMismatch, probe.Vendor, probe.AuthMode, accountID)
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
	if !isCursorOAuthRecord(rec) {
		err := fmt.Errorf("%w: vendor=%s auth_mode=%s account_id=%d", ErrCursorRecordMismatch, rec.Vendor, rec.AuthMode, accountID)
		if saveErr := txStore.SaveRefreshFailure(ctx, rec, failureCursorMismatch, r.now().Add(defaultRetryAfter)); saveErr != nil {
			return nil, saveErr
		}
		return err, nil
	}
	payload, expiresAt, err := r.Adapter.RefreshForProvider(ctx, accountID, CursorVendor, rec.PlaintextPayload)
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
		return nil, time.Time{}, fmt.Errorf("cursor refresh account %d: %w", accountID, err)
	}
	refreshToken := credentialString(cred, "refresh_token")
	if refreshToken == "" {
		return nil, time.Time{}, fmt.Errorf("cursor refresh account %d: %w: refresh_token is empty", accountID, credentialstore.ErrInvalidPayload)
	}
	tokenURL := strings.TrimSpace(a.TokenURL)
	if tokenURL == "" {
		return nil, time.Time{}, fmt.Errorf("cursor refresh account %d: %w: token_url", accountID, ErrCursorOAuthConfigRequired)
	}
	clientID := strings.TrimSpace(a.ClientID)
	if clientID == "" {
		return nil, time.Time{}, fmt.Errorf("cursor refresh account %d: %w: client_id", accountID, ErrCursorOAuthConfigRequired)
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
		return nil, time.Time{}, fmt.Errorf("cursor refresh account %d: %w", accountID, err)
	}
	payload, expiresAt, err := a.mergeToken(cred, token, clientID)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("cursor refresh account %d: %w", accountID, err)
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
		return tokenResponse{}, &RefreshError{Outcome: string(OutcomeTransientError), Retryable: true, Cause: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, &RefreshError{Outcome: string(OutcomeTransientError), Retryable: true, Cause: err}
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
	return http.DefaultClient
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
		return "cursor refresh: failed"
	}
	status := ""
	if e.StatusCode > 0 {
		status = fmt.Sprintf(" status=%d", e.StatusCode)
	}
	return fmt.Sprintf("cursor refresh: failed outcome=%s%s", e.Outcome, status)
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
			Outcome:    string(OutcomeRateLimit),
			StatusCode: status,
			Retryable:  false,
			RetryAfter: now.Add(parseRetryAfter(header, now)),
			Cause:      ErrCursorRateLimited,
		}
	case status >= http.StatusInternalServerError && status <= 599:
		return &RefreshError{Outcome: string(OutcomeTransientError), StatusCode: status, Retryable: true, Cause: ErrCursorTransient}
	case status == http.StatusUnauthorized || code == "invalid_grant":
		return &RefreshError{Outcome: string(OutcomeAuthExpired), StatusCode: status, Retryable: false, Cause: ErrCursorAuthExpired}
	default:
		return &RefreshError{Outcome: string(OutcomeUnknown), StatusCode: status, Retryable: false}
	}
}

func classifyRefreshFailure(err error) string {
	var refreshErr *RefreshError
	if errors.As(err, &refreshErr) && refreshErr.Outcome != "" {
		return refreshErr.Outcome
	}
	if errors.Is(err, credentialstore.ErrInvalidPayload) {
		return failurePayloadInvalid
	}
	if errors.Is(err, ErrCursorRecordMismatch) {
		return failureCursorMismatch
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func isCursorOAuthRecord(rec credentialstore.CredentialRecord) bool {
	if credentialstore.Normalize(rec.Vendor) != CursorVendor {
		return false
	}
	switch credentialstore.Normalize(rec.AuthMode) {
	case CursorAuthModeOAuth, cursorOAuthModeAlias, cursorCredentialMode:
		return true
	default:
		return false
	}
}
