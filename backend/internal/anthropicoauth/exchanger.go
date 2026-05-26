package anthropicoauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

type Exchanger struct {
	Config           credentialacq.OAuthClientConfig
	HTTPClient       *http.Client
	Now              func() time.Time
	TransportFactory *transport.Factory
	MimicryRegistry  *mimicry.TemplateRegistry
}

func NewExchanger() Exchanger {
	return Exchanger{}
}

func RegisterDefault(exchanger Exchanger) error {
	if err := credentialacq.RegisterOrReplaceExchanger(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth), exchanger); err != nil {
		return err
	}
	return credentialacq.RegisterOrReplaceExchanger(credentialstore.VendorAnthropic, exchanger)
}

func RegisterInto(registry *credentialacq.ExchangerRegistry, exchanger Exchanger) error {
	if registry == nil {
		return errors.New("anthropicoauth: exchanger registry is nil")
	}
	if err := registry.RegisterOrReplaceExchanger(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth), exchanger); err != nil {
		return err
	}
	return registry.RegisterOrReplaceExchanger(credentialstore.VendorAnthropic, exchanger)
}

func (e Exchanger) ExchangeOAuthCode(ctx context.Context, session credentialacq.Session, code string) (credentialacq.CredentialCandidate, error) {
	return credentialacq.CredentialCandidate{}, errors.New("anthropicoauth: pkce verifier store is required")
}

func (e Exchanger) ExchangeOAuthCodeWithStore(ctx context.Context, store *credentialacq.PostgresSessionStore, session credentialacq.Session, state, code string) (credentialacq.CredentialCandidate, error) {
	if store == nil {
		return credentialacq.CredentialCandidate{}, errors.New("anthropicoauth: session store not configured")
	}
	verifier, err := store.DecryptTransientPayload(ctx, session.EncryptedPKCEVerifier, session.NonceHash, aadFromSession(session))
	if err != nil {
		return credentialacq.CredentialCandidate{}, err
	}
	cfg := mergeConfig(e.Config, credentialacq.OAuthClientConfig{
		RedirectURI: session.RedirectURI,
		Scopes:      session.RequestedScopes,
		Source:      session.ClientIdentitySource,
	})
	token, err := e.exchange(ctx, cfg, state, code, string(verifier), session)
	if err != nil {
		return credentialacq.CredentialCandidate{}, err
	}
	payload, err := json.Marshal(token)
	if err != nil {
		return credentialacq.CredentialCandidate{}, err
	}
	return credentialacq.CredentialCandidate{
		TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
		Vendor: credentialstore.VendorAnthropic, AuthMode: session.AuthMode,
		Payload: payload, ActorID: session.ActorID,
		RedactedContext: map[string]any{
			"client_id_source": token.ClientIDSource,
			"email_present":    token.Email != "",
		},
	}, nil
}

func (e Exchanger) exchange(ctx context.Context, cfg credentialacq.OAuthClientConfig, state, code, verifier string, session credentialacq.Session) (Token, error) {
	code = strings.TrimSpace(code)
	verifier = strings.TrimSpace(verifier)
	if code == "" {
		return Token{}, fmt.Errorf("%w: missing authorization code", credentialacq.ErrInvalidTokenShape)
	}
	if verifier == "" {
		return Token{}, fmt.Errorf("%w: missing pkce verifier", credentialacq.ErrInvalidTokenShape)
	}
	body := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     cfg.ClientID,
		"redirect_uri":  cfg.RedirectURI,
		"code":          code,
		"code_verifier": verifier,
	}
	if strings.TrimSpace(state) != "" {
		body["state"] = strings.TrimSpace(state)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Token{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, bytes.NewReader(raw))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := e.httpClient().Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("anthropicoauth: token exchange request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Token{}, fmt.Errorf("anthropicoauth: token exchange read failed: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Token{}, fmt.Errorf("anthropicoauth: token exchange status %d", resp.StatusCode)
	}
	var decoded tokenResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return Token{}, fmt.Errorf("anthropicoauth: token response decode failed: %w", err)
	}
	token := decoded.toToken(e.now(), cfg, session)
	if token.AccessToken == "" || token.RefreshToken == "" {
		return Token{}, fmt.Errorf("%w: anthropic oauth requires access_token and refresh_token", credentialacq.ErrInvalidTokenShape)
	}
	return token, nil
}

func (e Exchanger) httpClient() *http.Client {
	if e.HTTPClient != nil {
		return e.HTTPClient
	}
	if e.Config.HTTPClient != nil {
		return e.Config.HTTPClient
	}
	return mimicryHTTPClient(e.TransportFactory, e.MimicryRegistry, "token_exchange")
}

func (e Exchanger) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
	ExpiresAt    string `json:"expires_at"`
	Email        string `json:"email"`
	Account      struct {
		EmailAddress string `json:"email_address"`
	} `json:"account"`
}

func (r tokenResponse) toToken(now time.Time, cfg credentialacq.OAuthClientConfig, session credentialacq.Session) Token {
	expiresAt := parseExpiresAt(r.ExpiresAt)
	if expiresAt.IsZero() && r.ExpiresIn > 0 {
		expiresAt = now.Add(time.Duration(r.ExpiresIn) * time.Second)
	}
	email := firstNonEmpty(r.Email, r.Account.EmailAddress)
	return Token{
		AccessToken: r.AccessToken, RefreshToken: r.RefreshToken, IDToken: r.IDToken,
		Email: email, ExpiresAt: expiresAt.UTC(), AuthMode: session.AuthMode,
		ClientID: cfg.ClientID, ClientIDSource: firstNonEmpty(session.ClientIdentitySource, cfg.Source),
		TokenEndpoint: cfg.TokenURL,
	}
}

func parseExpiresAt(raw string) time.Time {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func mergeConfig(base, override credentialacq.OAuthClientConfig) credentialacq.OAuthClientConfig {
	cfg := OAuthConfig("")
	applyConfig := func(in credentialacq.OAuthClientConfig) {
		if in.ClientID != "" {
			cfg.ClientID = in.ClientID
		}
		if in.ClientSecret != "" {
			cfg.ClientSecret = in.ClientSecret
		}
		if in.AuthURL != "" {
			cfg.AuthURL = in.AuthURL
		}
		if in.TokenURL != "" {
			cfg.TokenURL = in.TokenURL
		}
		if in.RedirectURI != "" {
			cfg.RedirectURI = in.RedirectURI
		}
		if len(in.Scopes) > 0 {
			cfg.Scopes = append([]string(nil), in.Scopes...)
		}
		if in.Source != "" {
			cfg.Source = in.Source
		}
		if in.HTTPClient != nil {
			cfg.HTTPClient = in.HTTPClient
		}
	}
	applyConfig(base)
	applyConfig(override)
	return cfg
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
