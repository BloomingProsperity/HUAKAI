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
	"net"
	"net/http"
	"net/netip"
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
	oauthErrorBodyMaxBytes    = 512
	antigravityLockScope      = "account"
	staticAccountType         = "upstream_static"
	oauthAccountType          = "oauth"
)

var (
	ErrTokenMalformed       = errors.New("ERR_TOKEN_MALFORMED")
	ErrRefreshUnavailable   = errors.New("token refresh unavailable")
	ErrRefreshLockContended = errors.New("refresh already in progress")
	ErrAccountUnavailable   = errors.New("provider account unavailable")
	ErrOAuthEndpointBlocked = errors.New("oauth_endpoint_blocked")
	lookupOAuthIPAddrs      = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return net.DefaultResolver.LookupIPAddr(ctx, host)
	}
	oauthSpecialUseDenyPrefixes = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("192.88.99.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("2001::/23"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("2002::/16"),
		netip.MustParsePrefix("3fff::/20"),
		netip.MustParsePrefix("5f00::/16"),
		netip.MustParsePrefix("64:ff9b::/96"),
		netip.MustParsePrefix("64:ff9b:1::/48"),
		netip.MustParsePrefix("100::/64"),
	}
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
	if logger == nil {
		logger = zap.NewNop()
	}
	return &AntigravityTokenProvider{
		store:     store,
		audit:     audit,
		cache:     cache,
		lock:      lock,
		marker:    marker,
		client:    newSSRFProtectedOAuthClient(client),
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
	endpoint, err := validateOAuthEndpoint(cred.OAuthEndpoint)
	if err != nil {
		return antigravityTokenResponse{}, err
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {cred.RefreshToken}}
	if strings.TrimSpace(cred.ClientID) != "" {
		form.Set("client_id", cred.ClientID)
	}
	if strings.TrimSpace(cred.ClientSecret) != "" {
		form.Set("client_secret", cred.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return antigravityTokenResponse{}, p.sanitizer.SanitizeError(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrOAuthEndpointBlocked) {
			return antigravityTokenResponse{}, ErrOAuthEndpointBlocked
		}
		return antigravityTokenResponse{}, p.sanitizer.SanitizeError(err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return antigravityTokenResponse{}, p.sanitizer.SanitizeError(readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return antigravityTokenResponse{}, p.sanitizer.SanitizeError(classifiedOAuthRefreshError(resp.StatusCode, body, p.sanitizer))
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

func validateOAuthEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", ErrOAuthEndpointBlocked
	}
	return u.String(), nil
}

func classifiedOAuthRefreshError(statusCode int, body []byte, sanitizer OAuthErrorSanitizer) error {
	snippet := strings.TrimSpace(string(body))
	truncated := false
	if len(snippet) > oauthErrorBodyMaxBytes {
		snippet = snippet[:oauthErrorBodyMaxBytes]
		truncated = true
	}
	if snippet == "" {
		return fmt.Errorf("oauth_refresh_upstream_error status=%d body_class=empty", statusCode)
	}
	snippet = sanitizer.Sanitize(snippet)
	return fmt.Errorf("oauth_refresh_upstream_error status=%d body_class=non_2xx body_redacted=%q truncated=%t", statusCode, snippet, truncated)
}

// NewSSRFProtectedOAuthClient 把 base client 包成带 SSRF / DNS-rebind
// 防御的 OAuth-grade client: transport.Proxy=nil, DialContext 拨号层
// 校验目标 IP 不是 loopback / private / link-local / metadata; CheckRedirect
// 禁 3xx (防 attacker redirect 把 client_secret/code 渗到自家 endpoint)。
// 供 credentialacq 等包出站 OAuth token endpoint 时复用
// P1 follow-up, 关闭 DNS-rebind 攻击面 (静态层在 39c66a3 已落地)。
func NewSSRFProtectedOAuthClient(base *http.Client) *http.Client {
	return newSSRFProtectedOAuthClient(base)
}

// SwapOAuthIPLookupForTesting 给跨包测试替换 lookupOAuthIPAddrs, 返
// restore closure 还原。生产代码不应调用; 测试 fixture 用 mock 返公网 IP
// (例 8.8.8.8) 绕过真 DNS 解析 — fake .test/.example 域名不可解析,
// 真 DNS lookup 会被 SSRF guard 拒。
func SwapOAuthIPLookupForTesting(fn func(ctx context.Context, host string) ([]net.IPAddr, error)) func() {
	original := lookupOAuthIPAddrs
	lookupOAuthIPAddrs = fn
	return func() { lookupOAuthIPAddrs = original }
}

func newSSRFProtectedOAuthClient(base *http.Client) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	clone := *base
	transport := &http.Transport{}
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok && defaultTransport != nil {
		transport = defaultTransport.Clone()
	}
	if existing, ok := base.Transport.(*http.Transport); ok && existing != nil {
		transport = existing.Clone()
	} else if base.Transport != nil {
		// OAuth SSRF 防护必须安装到 *http.Transport.DialContext 才能在拨号层校验目标 IP。
		// 非 *http.Transport 的自定义 RoundTripper 在这里有意不继承，避免绕过拨号级防护。
	}
	dial := transport.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	// OAuth refresh 携带 client_secret/refresh_token，必须直连真实 token endpoint；
	// 禁用代理避免只校验到代理 IP 后把密钥通过 CONNECT 转发到租户可控内网地址。
	transport.Proxy = nil
	transport.DialContext = ssrfGuardedDialContext(dial)
	transport.DialTLSContext = nil
	transport.DialTLS = nil
	clone.Transport = transport
	clone.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

func ssrfGuardedDialContext(base func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		dialAddresses, err := resolvePublicOAuthDialAddresses(ctx, address)
		if err != nil {
			return nil, err
		}
		var lastDialErr error
		for _, dialAddress := range dialAddresses {
			conn, err := base(ctx, network, dialAddress)
			if err != nil {
				lastDialErr = err
				continue
			}
			if !isPublicRemoteAddr(conn.RemoteAddr()) {
				_ = conn.Close()
				return nil, ErrOAuthEndpointBlocked
			}
			return conn, nil
		}
		if lastDialErr != nil {
			return nil, lastDialErr
		}
		return nil, ErrOAuthEndpointBlocked
	}
}

func resolvePublicOAuthDialAddresses(ctx context.Context, address string) ([]string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrOAuthEndpointBlocked
	}
	resolved, err := lookupOAuthIPAddrs(ctx, host)
	if err != nil || len(resolved) == 0 {
		return nil, ErrOAuthEndpointBlocked
	}
	dialAddresses := make([]string, 0, len(resolved))
	for _, candidate := range resolved {
		if !isPublicOAuthIP(candidate.IP) {
			return nil, ErrOAuthEndpointBlocked
		}
		dialAddresses = append(dialAddresses, net.JoinHostPort(candidate.IP.String(), port))
	}
	if len(dialAddresses) == 0 {
		return nil, ErrOAuthEndpointBlocked
	}
	return dialAddresses, nil
}

func isPublicRemoteAddr(addr net.Addr) bool {
	var ip net.IP
	switch a := addr.(type) {
	case *net.TCPAddr:
		ip = a.IP
	case *net.UDPAddr:
		ip = a.IP
	default:
		return false
	}
	return isPublicOAuthIP(ip)
}

// IsPublicOAuthIP 报告 ip 是否为可接受的公网 OAuth 目标(非环回/私有/链路本地/CGNAT/special-use/
// 组播/非全局单播)。导出供其它包(如 userauth 的 OAuth endpoint 静态门控)复用拨号期 guard 的同一套
// IP deny 策略,避免静态校验与拨号校验出现策略漂移。
func IsPublicOAuthIP(ip net.IP) bool {
	return isPublicOAuthIP(ip)
}

func isPublicOAuthIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	ip = ip.To16()
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() || !ip.IsGlobalUnicast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 0x40 {
		return false
	}
	if isOAuthSpecialUseIP(ip) {
		return false
	}
	return true
}

func isOAuthSpecialUseIP(ip net.IP) bool {
	addr, ok := oauthNetIPAddr(ip)
	if !ok {
		return false
	}
	for _, prefix := range oauthSpecialUseDenyPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func oauthNetIPAddr(ip net.IP) (netip.Addr, bool) {
	// IPv4-mapped IPv6 地址按 IPv4 归类，确保映射到特殊用途 IPv4 时仍被拒绝。
	if v4 := ip.To4(); v4 != nil {
		return netip.AddrFromSlice(v4)
	}
	v6 := ip.To16()
	if v6 == nil {
		return netip.Addr{}, false
	}
	return netip.AddrFromSlice(v6)
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
