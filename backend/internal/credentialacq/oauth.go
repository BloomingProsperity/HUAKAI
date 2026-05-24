package credentialacq

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const (
	// Public client IDs are intentionally not hardcoded until the Phase B
	// verifier lane records current approved public sources. Operator config
	// and per-account override remain available production paths.
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

func StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	if exc, ok := defaultExchangers.Lookup(exchangerKey(in.Vendor, in.AuthMode)); ok {
		return exc.StartOAuthFlow(ctx, store, in, cfg)
	}
	return startPKCEOAuthFlow(ctx, store, in, cfg)
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
	if registry == nil {
		registry = defaultExchangers
	}
	return CompleteOAuthCallback(ctx, store, flowID, state, code,
		func(ctx context.Context, session Session, code string) (CredentialCandidate, error) {
			exc, ok := registry.Lookup(exchangerKey(session.Vendor, session.AuthMode))
			if !ok {
				return CredentialCandidate{}, errors.New("credentialacq: oauth exchanger missing")
			}
			return exc.ExchangeOAuthCode(ctx, session, code)
		})
}

func CompleteOAuthCallback(ctx context.Context, store *PostgresSessionStore, flowID, state, code string, exchange OAuthExchanger) (CredentialCandidate, Session, error) {
	if store == nil {
		return CredentialCandidate{}, Session{}, errors.New("credentialacq: session store not configured")
	}
	session, err := store.Get(ctx, flowID)
	if err != nil {
		return CredentialCandidate{}, Session{}, err
	}
	if !session.ConsumedAt.IsZero() || session.Status == StatusFinalized {
		return CredentialCandidate{}, session, ErrFlowReplay
	}
	now := store.now().UTC()
	if now.After(session.ExpiresAt) {
		expired, _ := store.UpdateStatus(ctx, flowID, StatusExpired, "expired", "acquisition flow expired")
		return CredentialCandidate{}, expired, ErrFlowExpired
	}
	if !OAuthStateMatches(session.StateHash, state) {
		failed, _ := store.UpdateStatus(ctx, flowID, StatusFailed, "state_mismatch", "oauth state mismatch")
		return CredentialCandidate{}, failed, ErrStateMismatch
	}
	if _, err := store.DecryptTransientPayload(ctx, session.EncryptedPKCEVerifier, session.NonceHash, pkceAADFromSession(session)); err != nil {
		failed, _ := store.UpdateStatus(ctx, flowID, StatusFailed, "pkce_decrypt_failed", "oauth verifier decrypt failed")
		return CredentialCandidate{}, failed, err
	}
	callbackSession, err := store.UpdateStatus(ctx, flowID, StatusCallbackReceived, "", "")
	if err != nil {
		return CredentialCandidate{}, session, err
	}
	if exchange == nil {
		return CredentialCandidate{}, callbackSession, errors.New("credentialacq: oauth exchanger missing")
	}
	candidate, err := exchange(ctx, callbackSession, code)
	if err != nil {
		failed, _ := store.UpdateStatus(ctx, flowID, StatusFailed, "exchange_failed", "oauth exchange failed")
		return CredentialCandidate{}, failed, err
	}
	if candidate.Vendor == "" {
		candidate.Vendor = callbackSession.Vendor
	}
	if candidate.AuthMode == "" {
		candidate.AuthMode = callbackSession.AuthMode
	}
	validated, err := store.UpdateStatus(ctx, flowID, StatusValidated, "", "")
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
