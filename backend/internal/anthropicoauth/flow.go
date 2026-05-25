package anthropicoauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func (e Exchanger) StartOAuthFlow(ctx context.Context, store *credentialacq.PostgresSessionStore, in credentialacq.StartInput, cfg credentialacq.OAuthClientConfig) (credentialacq.OAuthStartResult, error) {
	if store == nil {
		return credentialacq.OAuthStartResult{}, errors.New("anthropicoauth: session store not configured")
	}
	cfg = mergeConfig(e.Config, cfg)
	state, err := randomURLToken(32)
	if err != nil {
		return credentialacq.OAuthStartResult{}, err
	}
	verifier, err := randomURLToken(64)
	if err != nil {
		return credentialacq.OAuthStartResult{}, err
	}
	challenge := pkceChallenge(verifier)
	in.Kind = credentialacq.FlowKindOAuth
	in.StateHash = credentialacq.HashOAuthState(state)
	in.Vendor = credentialstore.Normalize(firstNonEmpty(in.Vendor, credentialstore.VendorAnthropic))
	in.AuthMode = credentialstore.Normalize(firstNonEmpty(in.AuthMode, credentialstore.AuthModeClaudeAIOAuth))
	ciphertext, metadata, _, err := store.EncryptTransientPayload(ctx, []byte(verifier), aadFromStart(in))
	if err != nil {
		return credentialacq.OAuthStartResult{}, err
	}
	in.EncryptedPKCEVerifier = ciphertext
	in.NonceHash = metadata
	if in.ClientIdentitySource == "" {
		in.ClientIdentitySource = firstNonEmpty(cfg.Source, credentialacq.ClientSourcePublicCLI)
	}
	if in.RedirectURI == "" {
		in.RedirectURI = cfg.RedirectURI
	}
	if len(in.RequestedScopes) == 0 {
		in.RequestedScopes = append([]string(nil), cfg.Scopes...)
	}
	session, err := store.CreateFromStart(ctx, in)
	if err != nil {
		return credentialacq.OAuthStartResult{}, err
	}
	session.AuthType = credentialacq.AuthTypePKCE
	return credentialacq.OAuthStartResult{
		Session:       session,
		AuthType:      credentialacq.AuthTypePKCE,
		State:         state,
		CodeVerifier:  verifier,
		CodeChallenge: challenge,
		AuthorizeURL:  credentialacq.BuildAuthorizeURL(cfg, state, challenge),
	}, nil
}

func aadFromStart(in credentialacq.StartInput) credentialstore.AAD {
	return credentialstore.AAD{
		TenantID: in.TenantID, ProviderAccountID: in.ProviderAccountID,
		Vendor: in.Vendor, AuthMode: in.AuthMode,
	}
}

func aadFromSession(session credentialacq.Session) credentialstore.AAD {
	return credentialstore.AAD{
		TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
		Vendor: session.Vendor, AuthMode: session.AuthMode,
	}
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
