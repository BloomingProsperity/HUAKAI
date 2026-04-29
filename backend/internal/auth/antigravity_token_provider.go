package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const (
	antigravityProvider       = "antigravity"
	antigravityRefreshSkew    = 3 * time.Minute
	antigravityCacheSkew      = 5 * time.Minute
	antigravityRequestTimeout = 8 * time.Second
	antigravityLockWait       = 750 * time.Millisecond
	antigravityTempUnsched    = 5 * time.Minute
	antigravityLockScope      = "account"
	staticAccountType         = "upstream_static"
	oauthAccountType          = "oauth"
)

var (
	ErrTokenMalformed       = errors.New("ERR_TOKEN_MALFORMED")
	ErrRefreshUnavailable   = errors.New("token refresh unavailable")
	ErrRefreshLockContended = errors.New("refresh already in progress")
	ErrAccountUnavailable   = errors.New("provider account unavailable")
)

type TokenCache interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, token string, ttl time.Duration) error
}

type RefreshLock interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, key string) error
}

type AccountCredentialStore interface {
	LoadProviderAccount(ctx context.Context, tenantID, accountID int64) (ProviderAccountCredential, error)
	SaveRefreshedCredential(ctx context.Context, update RefreshedCredentialUpdate) (CredentialSaveResult, error)
}

type AccountStateMarker interface {
	MarkTempUnschedulable(ctx context.Context, tenantID, accountID int64, until time.Time, reason string) error
	MarkOperatorAttention(ctx context.Context, tenantID, accountID int64, reason string) error
}

type ProviderAccountCredential struct {
	TenantID                int64
	AccountID               int64
	Provider                string
	AccountType             string
	Enabled                 bool
	CredentialJSON          []byte
	TokenVersion            int64
	RefreshTokenFingerprint string
	TempUnschedulableUntil  *time.Time
}

type RefreshedCredentialUpdate struct {
	TenantID                int64
	AccountID               int64
	CredentialJSON          []byte
	TokenVersion            int64
	RefreshTokenFingerprint string
	Outcome                 Outcome
}

type CredentialSaveResult struct {
	RowsAffected int64
	Winning      *ProviderAccountCredential
}

type AntigravityTokenProvider struct {
	store     AccountCredentialStore
	audit     AuditWriter
	cache     TokenCache
	lock      RefreshLock
	marker    AccountStateMarker
	client    *http.Client
	logger    *zap.Logger
	now       func() time.Time
	sanitizer OAuthErrorSanitizer
}

func NewAntigravityTokenProvider(store AccountCredentialStore, audit AuditWriter, cache TokenCache, lock RefreshLock, marker AccountStateMarker, client *http.Client, logger *zap.Logger) *AntigravityTokenProvider {
	if client == nil {
		client = http.DefaultClient
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &AntigravityTokenProvider{
		store:     store,
		audit:     audit,
		cache:     cache,
		lock:      lock,
		marker:    marker,
		client:    client,
		logger:    logger,
		now:       time.Now,
		sanitizer: OAuthErrorSanitizer{},
	}
}

func (p *AntigravityTokenProvider) GetAccessToken(ctx context.Context, tenantID, accountID int64) (string, error) {
	key := p.cacheKey(tenantID, accountID)
	if p.cache != nil {
		if token, err := p.cache.Get(ctx, key); err == nil && attestTokenShape(token) {
			_ = p.writeAudit(ctx, RefreshAuditEntry{TenantID: tenantID, ProviderAccountID: accountID, Outcome: OutcomeCacheHit})
			return token, nil
		}
	}

	if p.store == nil {
		return "", ErrRefreshUnavailable
	}
	account, err := p.store.LoadProviderAccount(ctx, tenantID, accountID)
	if err != nil {
		return "", p.sanitizer.SanitizeError(err)
	}
	if err := p.validateAccount(account); err != nil {
		return "", err
	}
	cred, err := decodeAntigravityCredential(account.CredentialJSON)
	if err != nil {
		return "", p.recordFailure(ctx, account, OutcomePermanentDisable, "", err)
	}
	if account.AccountType == staticAccountType {
		if strings.TrimSpace(cred.APIKey) == "" {
			return "", p.recordFailure(ctx, account, OutcomeTokenMalformed, "", ErrTokenMalformed)
		}
		return cred.APIKey, nil
	}
	if !needsAntigravityRefresh(p.now(), cred.ExpiresAt) {
		if !attestTokenShape(cred.AccessToken) {
			return "", p.recordMalformed(ctx, account)
		}
		_ = p.populateCache(ctx, key, cred.AccessToken, cred.ExpiresAt)
		return cred.AccessToken, nil
	}

	lockKey := p.lockKey(tenantID, accountID)
	locked, err := p.acquireLock(ctx, lockKey)
	if err != nil {
		return "", p.recordFailure(ctx, account, OutcomeStormBudgetExhausted, antigravityLockScope, err)
	}
	if !locked {
		return p.waitForPeerRefresh(ctx, account, key, cred)
	}
	defer func() { _ = p.releaseLock(context.Background(), lockKey) }()

	refreshCtx, cancel := context.WithTimeout(ctx, antigravityRequestTimeout)
	defer cancel()
	response, err := p.refresh(refreshCtx, cred)
	if err != nil {
		return "", p.recordFailure(ctx, account, OutcomePermanentDisable, "", err)
	}
	if !attestTokenShape(response.AccessToken) {
		return "", p.recordMalformed(ctx, account)
	}

	next := cred
	next.AccessToken = response.AccessToken
	next.ExpiresAt = response.ExpiresAt
	if strings.TrimSpace(response.RefreshToken) != "" {
		next.RefreshToken = response.RefreshToken
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return "", p.recordFailure(ctx, account, OutcomePermanentDisable, "", err)
	}

	newFingerprint := refreshFingerprint(tenantID, next.RefreshToken)
	outcome := OutcomeRefreshSucceeded
	if strings.TrimSpace(response.RefreshToken) != "" {
		outcome = OutcomeRefreshTokenRotated
	}
	result, err := p.store.SaveRefreshedCredential(ctx, RefreshedCredentialUpdate{
		TenantID: tenantID, AccountID: accountID, CredentialJSON: encoded,
		TokenVersion: account.TokenVersion, RefreshTokenFingerprint: newFingerprint, Outcome: outcome,
	})
	if err != nil {
		return "", p.recordFailure(ctx, account, OutcomeCASLost, "", err)
	}
	if result.RowsAffected == 0 {
		return p.useWinningCredential(ctx, account, result.Winning, key)
	}

	_ = p.writeAudit(ctx, RefreshAuditEntry{
		TenantID: tenantID, ProviderAccountID: accountID, Outcome: outcome,
		OldRefreshTokenFingerprint: account.RefreshTokenFingerprint,
		NewRefreshTokenFingerprint: newFingerprint, RequestID: uuid.NewString(),
		OccurredAt: p.now(),
	})
	_ = p.populateCache(ctx, key, response.AccessToken, response.ExpiresAt)
	return response.AccessToken, nil
}

func (p *AntigravityTokenProvider) validateAccount(account ProviderAccountCredential) error {
	if !account.Enabled || (account.TempUnschedulableUntil != nil && account.TempUnschedulableUntil.After(p.now())) {
		return ErrAccountUnavailable
	}
	if account.Provider != "" && account.Provider != antigravityProvider {
		return fmt.Errorf("provider mismatch: %s", account.Provider)
	}
	if account.AccountType != oauthAccountType && account.AccountType != staticAccountType {
		return fmt.Errorf("unsupported credential type: %s", account.AccountType)
	}
	return nil
}

func (p *AntigravityTokenProvider) acquireLock(ctx context.Context, key string) (bool, error) {
	if p.lock == nil {
		return true, nil
	}
	return p.lock.Acquire(ctx, key, antigravityRequestTimeout)
}

func (p *AntigravityTokenProvider) releaseLock(ctx context.Context, key string) error {
	if p.lock == nil {
		return nil
	}
	return p.lock.Release(ctx, key)
}

func (p *AntigravityTokenProvider) waitForPeerRefresh(ctx context.Context, account ProviderAccountCredential, key string, cred antigravityCredential) (string, error) {
	timer := time.NewTimer(antigravityLockWait)
	defer timer.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			if attestTokenShape(cred.AccessToken) && cred.ExpiresAt.After(p.now()) {
				_ = p.writeAudit(ctx, RefreshAuditEntry{TenantID: account.TenantID, ProviderAccountID: account.AccountID, Outcome: OutcomeRefreshLockHeld})
				return cred.AccessToken, nil
			}
			return "", p.recordFailure(ctx, account, OutcomeRefreshLockHeld, antigravityLockScope, ErrRefreshLockContended)
		case <-tick.C:
			if p.cache == nil {
				continue
			}
			if token, err := p.cache.Get(ctx, key); err == nil && attestTokenShape(token) {
				_ = p.writeAudit(ctx, RefreshAuditEntry{TenantID: account.TenantID, ProviderAccountID: account.AccountID, Outcome: OutcomeRefreshLockHeld})
				return token, nil
			}
		}
	}
}

func (p *AntigravityTokenProvider) refresh(ctx context.Context, cred antigravityCredential) (antigravityTokenResponse, error) {
	if strings.TrimSpace(cred.OAuthEndpoint) == "" || strings.TrimSpace(cred.RefreshToken) == "" {
		return antigravityTokenResponse{}, ErrRefreshUnavailable
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {cred.RefreshToken}}
	if strings.TrimSpace(cred.ClientID) != "" {
		form.Set("client_id", cred.ClientID)
	}
	if strings.TrimSpace(cred.ClientSecret) != "" {
		form.Set("client_secret", cred.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cred.OAuthEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return antigravityTokenResponse{}, p.sanitizer.SanitizeError(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return antigravityTokenResponse{}, p.sanitizer.SanitizeError(err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return antigravityTokenResponse{}, p.sanitizer.SanitizeError(readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return antigravityTokenResponse{}, p.sanitizer.SanitizeError(fmt.Errorf("oauth refresh status %d: %s", resp.StatusCode, string(body)))
	}
	var wire struct {
		AccessToken  string          `json:"access_token"`
		RefreshToken string          `json:"refresh_token"`
		ExpiresIn    json.RawMessage `json:"expires_in"`
		ExpiresAt    string          `json:"expires_at"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&wire); err != nil {
		return antigravityTokenResponse{}, p.sanitizer.SanitizeError(err)
	}
	expiresAt, err := p.responseExpiry(wire.ExpiresIn, wire.ExpiresAt)
	if err != nil {
		return antigravityTokenResponse{}, p.sanitizer.SanitizeError(err)
	}
	return antigravityTokenResponse{AccessToken: wire.AccessToken, RefreshToken: wire.RefreshToken, ExpiresAt: expiresAt}, nil
}

func (p *AntigravityTokenProvider) responseExpiry(raw json.RawMessage, explicit string) (time.Time, error) {
	if len(raw) > 0 && string(raw) != "null" {
		seconds, err := decimal.NewFromString(strings.Trim(string(raw), `"`))
		if err != nil {
			return time.Time{}, err
		}
		return p.now().Add(time.Duration(seconds.IntPart()) * time.Second), nil
	}
	if strings.TrimSpace(explicit) != "" {
		return time.Parse(time.RFC3339, explicit)
	}
	return p.now().Add(time.Hour), nil
}

func (p *AntigravityTokenProvider) useWinningCredential(ctx context.Context, original ProviderAccountCredential, winning *ProviderAccountCredential, key string) (string, error) {
	if winning == nil {
		return "", p.recordFailure(ctx, original, OutcomeCASLost, "", ErrRefreshUnavailable)
	}
	cred, err := decodeAntigravityCredential(winning.CredentialJSON)
	if err != nil || !attestTokenShape(cred.AccessToken) {
		return "", p.recordMalformed(ctx, *winning)
	}
	_ = p.writeAudit(ctx, RefreshAuditEntry{TenantID: winning.TenantID, ProviderAccountID: winning.AccountID, Outcome: OutcomeDBVersionConflict, RequestID: uuid.NewString(), OccurredAt: p.now()})
	_ = p.populateCache(ctx, key, cred.AccessToken, cred.ExpiresAt)
	return cred.AccessToken, nil
}

func (p *AntigravityTokenProvider) populateCache(ctx context.Context, key, token string, expiresAt time.Time) error {
	if p.cache == nil {
		return nil
	}
	ttl := time.Until(expiresAt) - antigravityCacheSkew
	if ttl <= 0 {
		return nil
	}
	return p.cache.Set(ctx, key, token, ttl)
}

func (p *AntigravityTokenProvider) recordMalformed(ctx context.Context, account ProviderAccountCredential) error {
	if p.marker != nil {
		_ = p.marker.MarkOperatorAttention(ctx, account.TenantID, account.AccountID, ErrTokenMalformed.Error())
	}
	_ = p.writeAudit(ctx, RefreshAuditEntry{TenantID: account.TenantID, ProviderAccountID: account.AccountID, Outcome: OutcomeTokenMalformed, ErrorClass: "token_shape", ErrorMessageRedacted: ErrTokenMalformed.Error(), RequestID: uuid.NewString(), OccurredAt: p.now()})
	return ErrTokenMalformed
}

func (p *AntigravityTokenProvider) recordFailure(ctx context.Context, account ProviderAccountCredential, outcome Outcome, scope string, err error) error {
	safeErr := p.sanitizer.SanitizeError(err)
	if p.marker != nil {
		_ = p.marker.MarkTempUnschedulable(ctx, account.TenantID, account.AccountID, p.now().Add(antigravityTempUnsched), string(outcome))
	}
	_ = p.writeAudit(ctx, RefreshAuditEntry{TenantID: account.TenantID, ProviderAccountID: account.AccountID, Outcome: outcome, StormScope: scope, ErrorClass: fmt.Sprintf("%T", err), ErrorMessageRedacted: safeErr.Error(), RequestID: uuid.NewString(), OccurredAt: p.now()})
	p.logger.Warn("antigravity refresh failed", zap.Int64("tenant_id", account.TenantID), zap.Int64("provider_account_id", account.AccountID), zap.String("error", safeErr.Error()))
	return safeErr
}

func (p *AntigravityTokenProvider) writeAudit(ctx context.Context, entry RefreshAuditEntry) error {
	if p.audit == nil {
		return nil
	}
	if entry.RequestID == "" {
		entry.RequestID = uuid.NewString()
	}
	if entry.OccurredAt.IsZero() {
		entry.OccurredAt = p.now()
	}
	return p.audit.WriteRefreshAudit(ctx, &entry)
}

func (p *AntigravityTokenProvider) cacheKey(tenantID, accountID int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:access_token", tenantID, accountID, antigravityProvider)))
	return "auth:token:" + hex.EncodeToString(sum[:])
}

func (p *AntigravityTokenProvider) lockKey(tenantID, accountID int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:refresh", tenantID, accountID, antigravityProvider)))
	return "auth:refresh:" + hex.EncodeToString(sum[:])
}

type antigravityCredential struct {
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token"`
	ExpiresAt     time.Time `json:"expires_at"`
	APIKey        string    `json:"api_key,omitempty"`
	OAuthEndpoint string    `json:"oauth_endpoint,omitempty"`
	ClientID      string    `json:"client_id,omitempty"`
	ClientSecret  string    `json:"client_secret,omitempty"`
}

type antigravityTokenResponse struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

func decodeAntigravityCredential(raw []byte) (antigravityCredential, error) {
	var wire struct {
		AccessToken   string `json:"access_token"`
		RefreshToken  string `json:"refresh_token"`
		ExpiresAt     string `json:"expires_at"`
		APIKey        string `json:"api_key"`
		OAuthEndpoint string `json:"oauth_endpoint"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
	}
	if len(raw) == 0 {
		return antigravityCredential{}, errors.New("credential json is empty")
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return antigravityCredential{}, err
	}
	var expiresAt time.Time
	if strings.TrimSpace(wire.ExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, wire.ExpiresAt)
		if err != nil {
			return antigravityCredential{}, err
		}
		expiresAt = parsed
	}
	return antigravityCredential{AccessToken: wire.AccessToken, RefreshToken: wire.RefreshToken, ExpiresAt: expiresAt, APIKey: wire.APIKey, OAuthEndpoint: wire.OAuthEndpoint, ClientID: wire.ClientID, ClientSecret: wire.ClientSecret}, nil
}

func needsAntigravityRefresh(now, expiresAt time.Time) bool {
	return expiresAt.IsZero() || !expiresAt.After(now.Add(antigravityRefreshSkew))
}

func attestTokenShape(token string) bool {
	value := strings.TrimSpace(token)
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) == 3 && len(parts[0]) >= 8 && len(parts[1]) >= 8 && len(parts[2]) >= 8 {
		return true
	}
	if len(value) < 20 || len(value) > 8192 {
		return false
	}
	for _, r := range value {
		if !(r == '_' || r == '-' || r == '.' || r == ':' || r == '/' || r == '+' || r == '=' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}

func refreshFingerprint(tenantID int64, refreshToken string) string {
	if strings.TrimSpace(refreshToken) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", tenantID, refreshToken)))
	return hex.EncodeToString(sum[:])
}

var _ TokenProvider = (*AntigravityTokenProvider)(nil)
