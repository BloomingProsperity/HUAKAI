package credentialacq

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const (
	// 在 Phase B 校验通道记录当前已批准的公开来源之前,公共 client ID 刻意不做硬编码。
	// operator 配置与按账号覆盖仍是可用的生产路径。
	OpenAICodexPublicClientID  = ""
	GoogleGeminiPublicClientID = ""
)

type OAuthClientConfig struct {
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	RedirectURI  string
	Scopes       []string
	Source       string
	HTTPClient   *http.Client
}

type OAuthStartResult struct {
	Session                 Session  `json:"flow"`
	AuthType                AuthType `json:"auth_type,omitempty"`
	State                   string   `json:"state"`
	CodeVerifier            string   `json:"-"`
	CodeChallenge           string   `json:"code_challenge"`
	AuthorizeURL            string   `json:"authorize_url,omitempty"`
	UserCode                string   `json:"user_code,omitempty"`
	VerificationURI         string   `json:"verification_uri,omitempty"`
	VerificationURIComplete string   `json:"verification_uri_complete,omitempty"`
	PollIntervalSeconds     int      `json:"poll_interval_seconds,omitempty"`
	ExpiresInSeconds        int      `json:"expires_in_seconds,omitempty"`
}

type OAuthExchanger func(context.Context, Session, string) (CredentialCandidate, error)

type OAuthCandidatePersister func(context.Context, Session, CredentialCandidate) error

type DeviceCodePoller func(context.Context, Session) (CredentialCandidate, error)

const DefaultOAuthCallbackLease = 2 * time.Minute

func StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	return StartOAuthFlowWithRegistry(ctx, store, in, cfg, defaultExchangers)
}

func StartOAuthFlowWithRegistry(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig, registry *ExchangerRegistry) (OAuthStartResult, error) {
	if registry == nil {
		registry = defaultExchangers
	}
	if exc, ok := registry.Lookup(exchangerKey(in.Vendor, in.AuthMode)); ok {
		return exc.StartOAuthFlow(ctx, store, in, cfg)
	}
	return startPKCEOAuthFlow(ctx, store, in, cfg)
}

func validateDeviceCodePollEndpoints(session Session, cfg OAuthClientConfig) error {
	payload := session.DeviceCodePayload
	endpoints := []struct{ name, raw string }{
		{"token_url", firstNonEmpty(cfg.TokenURL, stringFromPayload(payload, "token_url"))},
	}
	if raw := stringFromPayload(payload, "oauth_token_url"); raw != "" {
		endpoints = append(endpoints, struct{ name, raw string }{"oauth_token_url", raw})
	}
	for _, endpoint := range endpoints {
		if err := validateOAuthEndpointURL(endpoint.raw); err != nil {
			return fmt.Errorf("%w: device-code %s rejected (%v)", ErrFeatureDisabled, endpoint.name, err)
		}
	}
	return nil
}

func startPKCEOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
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
	ciphertext, metadata, _, err := store.EncryptTransientPayload(ctx, []byte(verifier), pkceAADFromStart(in))
	if err != nil {
		return OAuthStartResult{}, err
	}
	in.EncryptedPKCEVerifier = ciphertext
	in.NonceHash = metadata
	if cfg.Source != "" {
		in.ClientIdentitySource = cfg.Source
	}
	if in.ClientIdentitySource == "" {
		in.ClientIdentitySource = ClientSourceOperatorConfig
	}
	if strings.TrimSpace(cfg.ClientID) == "" && in.ClientIdentitySource == ClientSourcePublicCLI {
		in.ClientIdentitySource = ClientSourceDisabledMissingConfig
	}
	if in.RedirectURI == "" {
		in.RedirectURI = cfg.RedirectURI
	}
	if len(in.RequestedScopes) == 0 {
		in.RequestedScopes = cfg.Scopes
	}
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

func CompleteOAuthCallbackWithRegistry(ctx context.Context, store *PostgresSessionStore, flowID, state, code string, registry *ExchangerRegistry) (CredentialCandidate, Session, error) {
	return CompleteOAuthCallbackWithRegistryAndPersist(ctx, store, flowID, state, code, registry, nil)
}

func CompleteOAuthCallbackWithRegistryAndPersist(ctx context.Context, store *PostgresSessionStore, flowID, state, code string, registry *ExchangerRegistry, persist OAuthCandidatePersister) (CredentialCandidate, Session, error) {
	if registry == nil {
		registry = defaultExchangers
	}
	return completeOAuthCallbackWithPersistence(ctx, store, flowID, state, code,
		func(ctx context.Context, session Session, code string) (CredentialCandidate, error) {
			exc, ok := registry.Lookup(exchangerKey(session.Vendor, session.AuthMode))
			if !ok {
				exc, ok = registry.Lookup(session.Vendor)
			}
			if !ok {
				return CredentialCandidate{}, fmt.Errorf("%w: %s", ErrOAuthExchangerMissing, exchangerKey(session.Vendor, session.AuthMode))
			}
			if storeAware, ok := exc.(StoreAwareExchanger); ok {
				return storeAware.ExchangeOAuthCodeWithStore(ctx, store, session, state, code)
			}
			return exc.ExchangeOAuthCode(ctx, session, code)
		}, persist)
}

func CompleteOAuthCallback(ctx context.Context, store *PostgresSessionStore, flowID, state, code string, exchange OAuthExchanger) (CredentialCandidate, Session, error) {
	return completeOAuthCallbackWithPersistence(ctx, store, flowID, state, code, exchange, nil)
}

func completeOAuthCallbackWithPersistence(
	ctx context.Context,
	store *PostgresSessionStore,
	flowID, state, code string,
	exchange OAuthExchanger,
	persist OAuthCandidatePersister,
) (CredentialCandidate, Session, error) {
	if store == nil {
		return CredentialCandidate{}, Session{}, errors.New("credentialacq: session store not configured")
	}
	session, err := store.Get(ctx, flowID)
	if err != nil {
		return CredentialCandidate{}, Session{}, err
	}
	// 终态或已消费流程不得被回调复活，必须在 state/PKCE 校验前直接拒绝。
	if !session.ConsumedAt.IsZero() || isTerminalStatus(session.Status) {
		return CredentialCandidate{}, session, ErrFlowReplay
	}
	now := store.now().UTC()
	if now.After(session.ExpiresAt) {
		expired, markErr := store.UpdateStatusFrom(ctx, flowID, StatusExpired, "expired", "acquisition flow expired", StatusStarted, StatusWaitingForUser, StatusCallbackReceived)
		if markErr != nil {
			return CredentialCandidate{}, expired, markErr
		}
		return CredentialCandidate{}, expired, ErrFlowExpired
	}
	if session.Status == StatusCallbackReceived && !session.UpdatedAt.IsZero() && now.Sub(session.UpdatedAt) >= DefaultOAuthCallbackLease {
		// 过期租约退回 started 并保留 PKCE/state，允许重新授权；租约内仍拒绝并发换码。
		reset, resetErr := store.UpdateStatusFrom(
			ctx, flowID, StatusStarted,
			"callback_lease_expired", "oauth callback lease expired before durable completion",
			StatusCallbackReceived,
		)
		if resetErr != nil {
			return CredentialCandidate{}, reset, resetErr
		}
		session = reset
	}
	if session.Status != StatusStarted && session.Status != StatusWaitingForUser {
		return CredentialCandidate{}, session, ErrFlowReplay
	}
	if !OAuthStateMatches(session.StateHash, state) {
		failed, markErr := store.UpdateStatusFrom(ctx, flowID, StatusFailed, "state_mismatch", "oauth state mismatch", StatusStarted, StatusWaitingForUser)
		if markErr != nil {
			return CredentialCandidate{}, failed, markErr
		}
		return CredentialCandidate{}, failed, ErrStateMismatch
	}
	if _, err := store.DecryptTransientPayload(ctx, session.EncryptedPKCEVerifier, session.NonceHash, pkceAADFromSession(session)); err != nil {
		failed, markErr := store.UpdateStatusFrom(ctx, flowID, StatusFailed, "pkce_decrypt_failed", "oauth verifier decrypt failed", StatusStarted, StatusWaitingForUser)
		if markErr != nil {
			return CredentialCandidate{}, failed, markErr
		}
		return CredentialCandidate{}, failed, err
	}
	callbackSession, err := store.UpdateStatusFrom(ctx, flowID, StatusCallbackReceived, "", "", StatusStarted, StatusWaitingForUser)
	if err != nil {
		return CredentialCandidate{}, callbackSession, err
	}
	if exchange == nil {
		return CredentialCandidate{}, callbackSession, ErrOAuthExchangerMissing
	}
	candidate, err := exchange(ctx, callbackSession, code)
	if err != nil {
		failed, markErr := store.UpdateStatusFrom(ctx, flowID, StatusFailed, "exchange_failed", "oauth exchange failed", StatusCallbackReceived)
		if markErr != nil {
			return CredentialCandidate{}, failed, markErr
		}
		return CredentialCandidate{}, failed, err
	}
	if candidate.Vendor == "" {
		candidate.Vendor = callbackSession.Vendor
	}
	if candidate.AuthMode == "" {
		candidate.AuthMode = callbackSession.AuthMode
	}
	if persist != nil {
		if persistErr := persist(ctx, callbackSession, candidate); persistErr != nil {
			// 候选未可靠落地时退回可重新授权状态，避免 validated 无候选的永久死锁。
			resetCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			reset, resetErr := store.UpdateStatusFrom(
				resetCtx, flowID, StatusStarted,
				"candidate_persist_failed", "oauth candidate persistence failed",
				StatusCallbackReceived,
			)
			cancel()
			if resetErr != nil {
				return CredentialCandidate{}, reset, errors.Join(persistErr, resetErr)
			}
			return CredentialCandidate{}, reset, persistErr
		}
	}
	validated, err := store.UpdateStatusFrom(ctx, flowID, StatusValidated, "", "", StatusCallbackReceived)
	return candidate, validated, err
}

func BuildAuthorizeURL(cfg OAuthClientConfig, state, codeChallenge string) string {
	if strings.TrimSpace(cfg.AuthURL) == "" || strings.TrimSpace(cfg.ClientID) == "" {
		return ""
	}
	u, err := url.Parse(cfg.AuthURL)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", strings.TrimSpace(cfg.ClientID))
	q.Set("redirect_uri", strings.TrimSpace(cfg.RedirectURI))
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	if len(cfg.Scopes) > 0 {
		q.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func HashOAuthState(state string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(state)))
	return sum[:]
}

func OAuthStateMatches(expectedHash []byte, gotState string) bool {
	got := HashOAuthState(gotState)
	return len(expectedHash) == len(got) && subtle.ConstantTimeCompare(expectedHash, got) == 1
}

func randomURLToken(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func pkceAADFromStart(in StartInput) credentialstore.AAD {
	return credentialstore.AAD{
		TenantID: in.TenantID, ProviderAccountID: in.ProviderAccountID,
		Vendor: in.Vendor, AuthMode: in.AuthMode,
	}
}

func pkceAADFromSession(session Session) credentialstore.AAD {
	return credentialstore.AAD{
		TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
		Vendor: session.Vendor, AuthMode: session.AuthMode,
	}
}
