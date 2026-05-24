package kiro

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
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
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
	failureVendorMismatch = "provider_mismatch"
)

var (
	ErrKiroAuthExpired    = errors.New("kiro refresh: authorization expired")
	ErrKiroRateLimited    = errors.New("kiro refresh: rate limit exceeded")
	ErrKiroRiskControl    = errors.New("kiro refresh: risk control triggered")
	ErrKiroTransient      = errors.New("kiro refresh: transient upstream failure")
	ErrKiroRecordMismatch = errors.New("kiro refresh: credential record is not kiro aws-sso")
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

func WithClientSecret(clientSecret string) Option {
	return func(r *Refresher) { r.Adapter.ClientSecret = clientSecret }
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
		return errors.New("kiro refresh: credential refresh store missing")
	}
	probe, err := r.Store.LoadForRefresh(ctx, accountID)
	if err != nil {
		return err
	}
	defer privacy.Zeroize(probe.PlaintextPayload)
	if !isKiroSSORecord(probe) {
		return fmt.Errorf("%w: vendor=%s auth_mode=%s account_id=%d", ErrKiroRecordMismatch, probe.Vendor, probe.AuthMode, accountID)
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
	if !isKiroSSORecord(rec) {
		err := fmt.Errorf("%w: vendor=%s auth_mode=%s account_id=%d", ErrKiroRecordMismatch, rec.Vendor, rec.AuthMode, accountID)
		if saveErr := txStore.SaveRefreshFailure(ctx, rec, failureVendorMismatch, r.now().Add(defaultRetryAfter)); saveErr != nil {
			return nil, saveErr
		}
		return err, nil
	}
	payload, expiresAt, err := r.Adapter.RefreshForProvider(ctx, accountID, KiroVendor, rec.PlaintextPayload)
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
	TokenURL     string
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
	Now          func() time.Time
}

func (a RefreshAdapter) RefreshForProvider(ctx context.Context, accountID int64, _ string, currentCredential []byte) ([]byte, time.Time, error) {
	cred, err := parseCredentialPayload(currentCredential)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("kiro refresh account %d: %w", accountID, err)
	}
	refreshToken := credentialString(cred, "refresh_token")
	if refreshToken == "" {
		refreshToken = credentialString(cred, "refreshToken")
	}
	if refreshToken == "" {
		return nil, time.Time{}, fmt.Errorf("kiro refresh account %d: %w: refresh_token is empty", accountID, credentialstore.ErrInvalidPayload)
	}
	tokenURL := strings.TrimSpace(a.TokenURL)
	if tokenURL == "" {
		return nil, time.Time{}, fmt.Errorf("kiro refresh account %d: %w: token_url", accountID, ErrKiroSSOConfigRequired)
	}
	clientID := strings.TrimSpace(a.ClientID)
	if clientID == "" {
		return nil, time.Time{}, fmt.Errorf("kiro refresh account %d: %w: client_id", accountID, ErrKiroSSOConfigRequired)
	}
	clientSecret := strings.TrimSpace(a.ClientSecret)
	if clientSecret == "" {
		return nil, time.Time{}, fmt.Errorf("kiro refresh account %d: %w: client_secret", accountID, ErrKiroSSOConfigRequired)
	}
	token, err := a.postRefresh(ctx, tokenURL, createTokenRequest{
		ClientID: clientID, ClientSecret: clientSecret, GrantType: "refresh_token", RefreshToken: refreshToken,
	})
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("kiro refresh account %d: %w", accountID, err)
	}
	payload, expiresAt, err := a.mergeToken(cred, token, clientID)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("kiro refresh account %d: %w", accountID, err)
	}
	return payload, expiresAt, nil
}

func (a RefreshAdapter) postRefresh(ctx context.Context, tokenURL string, body createTokenRequest) (tokenResponse, error) {
	reqCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		reqCtx, cancel = context.WithTimeout(ctx, defaultRefreshTimeout)
	}
	defer cancel()
	raw, err := json.Marshal(body)
	if err != nil {
		return tokenResponse{}, err
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, tokenURL, bytes.NewReader(raw))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient().Do(req)
	if err != nil {
		return tokenResponse{}, &RefreshError{Outcome: string(credentialworker.OutcomeTransientError), Retryable: true, Cause: err}
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, &RefreshError{Outcome: string(credentialworker.OutcomeTransientError), Retryable: true, Cause: err}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return tokenResponse{}, classifyHTTPRefreshError(resp.StatusCode, resp.Header, respBody, a.now())
	}
	var token tokenResponse
	if err := json.Unmarshal(respBody, &token); err != nil {
		return tokenResponse{}, fmt.Errorf("%w: token response decode failed: %v", credentialstore.ErrInvalidPayload, err)
	}
	return token, nil
}

func (a RefreshAdapter) mergeToken(cred map[string]any, token tokenResponse, clientID string) ([]byte, time.Time, error) {
	accessToken := token.accessToken()
	if accessToken == "" {
		return nil, time.Time{}, fmt.Errorf("%w: token response missing access token", credentialstore.ErrInvalidPayload)
	}
	expiresAt := resolveTokenExpiry(a.now(), token)
	cred["access_token"] = accessToken
	cred["session_token"] = accessToken
	cred["expires_at"] = expiresAt.Format(time.RFC3339)
	cred["client_id"] = clientID
	if refresh := token.refreshToken(); refresh != "" {
		cred["refresh_token"] = refresh
	}
	if tokenType := token.tokenType(); tokenType != "" {
		cred["token_type"] = tokenType
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

type createTokenRequest struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	GrantType    string `json:"grantType"`
	RefreshToken string `json:"refreshToken"`
}

type tokenResponse struct {
	AccessToken       string `json:"accessToken"`
	AccessTokenSnake  string `json:"access_token"`
	RefreshToken      string `json:"refreshToken"`
	RefreshTokenSnake string `json:"refresh_token"`
	TokenType         string `json:"tokenType"`
	TokenTypeSnake    string `json:"token_type"`
	ExpiresIn         int64  `json:"expiresIn"`
	ExpiresInSnake    int64  `json:"expires_in"`
	ExpiresAt         string `json:"expiresAt"`
	ExpiresAtSnake    string `json:"expires_at"`
}

func (t tokenResponse) accessToken() string {
	return firstNonEmpty(t.AccessToken, t.AccessTokenSnake)
}

func (t tokenResponse) refreshToken() string {
	return firstNonEmpty(t.RefreshToken, t.RefreshTokenSnake)
}

func (t tokenResponse) tokenType() string {
	return firstNonEmpty(t.TokenType, t.TokenTypeSnake)
}

func (t tokenResponse) expiresIn() int64 {
	if t.ExpiresIn > 0 {
		return t.ExpiresIn
	}
	return t.ExpiresInSnake
}

func (t tokenResponse) expiresAt() string {
	return firstNonEmpty(t.ExpiresAt, t.ExpiresAtSnake)
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
		return "kiro refresh: failed"
	}
	status := ""
	if e.StatusCode > 0 {
		status = fmt.Sprintf(" status=%d", e.StatusCode)
	}
	return fmt.Sprintf("kiro refresh: failed outcome=%s%s", e.Outcome, status)
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

func classifyHTTPRefreshError(status int, header http.Header, body []byte, now time.Time) error {
	classified := credentialworker.ClassifyRefreshError(errors.New(string(body)), KiroVendor, status)
	switch classified {
	case credentialworker.OutcomeAuthExpired:
		return &RefreshError{Outcome: string(classified), StatusCode: status, Retryable: false, Cause: ErrKiroAuthExpired}
	case credentialworker.OutcomeRateLimit:
		return &RefreshError{
			Outcome:    string(classified),
			StatusCode: status,
			Retryable:  false,
			RetryAfter: now.Add(parseRetryAfter(header, now)),
			Cause:      ErrKiroRateLimited,
		}
	case credentialworker.OutcomeRiskControl:
		return &RefreshError{Outcome: string(classified), StatusCode: status, Retryable: false, Cause: ErrKiroRiskControl}
	case credentialworker.OutcomeTransientError:
		return &RefreshError{Outcome: string(classified), StatusCode: status, Retryable: true, Cause: ErrKiroTransient}
	default:
		return &RefreshError{Outcome: string(credentialworker.OutcomeUnknown), StatusCode: status, Retryable: false}
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
	if errors.Is(err, ErrKiroRecordMismatch) {
		return failureVendorMismatch
	}
	if outcome := credentialworker.ClassifyRefreshError(err, KiroVendor, 0); outcome != credentialworker.OutcomeUnknown {
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

func resolveTokenExpiry(now time.Time, token tokenResponse) time.Time {
	now = now.UTC()
	if expiresIn := token.expiresIn(); expiresIn > 0 {
		return now.Add(time.Duration(expiresIn) * time.Second)
	}
	if raw := token.expiresAt(); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil && parsed.After(now) {
			return parsed.UTC()
		}
	}
	return now.Add(defaultAccessTokenTTL)
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

func isKiroSSORecord(rec credentialstore.CredentialRecord) bool {
	if credentialstore.Normalize(rec.Vendor) != KiroVendor {
		return false
	}
	switch credentialstore.Normalize(rec.AuthMode) {
	case KiroAuthModeAWSSSO, kiroAuthModeSSOAlias, kiroCredentialMode:
		return true
	default:
		return false
	}
}
