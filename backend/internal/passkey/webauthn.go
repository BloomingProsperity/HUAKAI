package passkey

import (
	"context"
	"encoding/json"

	"github.com/go-webauthn/webauthn/protocol"
	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
)

type WebAuthnEngine struct{}

func NewWebAuthnEngine() *WebAuthnEngine {
	return &WebAuthnEngine{}
}

func (e *WebAuthnEngine) BeginRegistration(ctx context.Context, cfg Config, user WebAuthnUser, exclude []CredentialRecord) (CeremonyOptions, []byte, error) {
	rp, err := newRelyingParty(cfg)
	if err != nil {
		return nil, nil, err
	}
	options, session, err := rp.BeginRegistration(
		webAuthnUserAdapter{user: user},
		webauthnlib.WithExclusions(descriptorsFromRecords(exclude)),
		webauthnlib.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthnlib.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			RequireResidentKey: protocol.ResidentKeyRequired(),
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			UserVerification:   protocol.VerificationRequired,
		}),
	)
	if err != nil {
		return nil, nil, err
	}
	optionsJSON, err := json.Marshal(options)
	if err != nil {
		return nil, nil, err
	}
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return nil, nil, err
	}
	_ = ctx
	return CeremonyOptions(optionsJSON), sessionJSON, nil
}

func (e *WebAuthnEngine) FinishRegistration(ctx context.Context, cfg Config, user WebAuthnUser, sessionData, credentialJSON []byte) (VerifiedCredential, error) {
	rp, err := newRelyingParty(cfg)
	if err != nil {
		return VerifiedCredential{}, err
	}
	var session webauthnlib.SessionData
	if err := json.Unmarshal(sessionData, &session); err != nil {
		return VerifiedCredential{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(credentialJSON)
	if err != nil {
		return VerifiedCredential{}, err
	}
	credential, err := rp.CreateCredential(webAuthnUserAdapter{user: user}, session, parsed)
	if err != nil {
		return VerifiedCredential{}, err
	}
	_ = ctx
	return verifiedFromWebAuthnCredential(*credential, 0), nil
}

func (e *WebAuthnEngine) BeginDiscoverableLogin(ctx context.Context, cfg Config) (CeremonyOptions, []byte, error) {
	rp, err := newRelyingParty(cfg)
	if err != nil {
		return nil, nil, err
	}
	options, session, err := rp.BeginDiscoverableLogin(
		webauthnlib.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		return nil, nil, err
	}
	optionsJSON, err := json.Marshal(options)
	if err != nil {
		return nil, nil, err
	}
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return nil, nil, err
	}
	_ = ctx
	return CeremonyOptions(optionsJSON), sessionJSON, nil
}

func (e *WebAuthnEngine) FinishDiscoverableLogin(ctx context.Context, cfg Config, sessionData, credentialJSON []byte, resolve DiscoverableResolver) (DiscoverableLoginResult, error) {
	rp, err := newRelyingParty(cfg)
	if err != nil {
		return DiscoverableLoginResult{}, err
	}
	var session webauthnlib.SessionData
	if err := json.Unmarshal(sessionData, &session); err != nil {
		return DiscoverableLoginResult{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(credentialJSON)
	if err != nil {
		return DiscoverableLoginResult{}, err
	}
	assertedCount := parsed.Response.AuthenticatorData.Counter
	var resolved ResolvedCredential
	handler := func(rawID, userHandle []byte) (webauthnlib.User, error) {
		var err error
		resolved, err = resolve(ctx, rawID, userHandle)
		if err != nil {
			return nil, err
		}
		return webAuthnUserAdapter{user: resolved.User}, nil
	}
	_, credential, err := rp.ValidatePasskeyLogin(handler, session, parsed)
	if err != nil {
		return DiscoverableLoginResult{}, err
	}
	return DiscoverableLoginResult{
		User:              resolved.User,
		Credential:        verifiedFromWebAuthnCredential(*credential, assertedCount),
		MatchedCredential: resolved.MatchedCredential,
		AssertedSignCount: assertedCount,
	}, nil
}

func newRelyingParty(cfg Config) (*webauthnlib.WebAuthn, error) {
	return webauthnlib.New(&webauthnlib.Config{
		RPID: cfg.RPID, RPDisplayName: cfg.RPDisplayName, RPOrigins: append([]string(nil), cfg.RPOrigins...),
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			RequireResidentKey: protocol.ResidentKeyRequired(),
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			UserVerification:   protocol.VerificationRequired,
		},
	})
}

type webAuthnUserAdapter struct {
	user WebAuthnUser
}

func (u webAuthnUserAdapter) WebAuthnID() []byte {
	return u.user.Handle()
}

func (u webAuthnUserAdapter) WebAuthnName() string {
	if u.user.User.Email != "" {
		return u.user.User.Email
	}
	return u.user.User.DisplayName
}

func (u webAuthnUserAdapter) WebAuthnDisplayName() string {
	if u.user.User.DisplayName != "" {
		return u.user.User.DisplayName
	}
	return u.WebAuthnName()
}

func (u webAuthnUserAdapter) WebAuthnCredentials() []webauthnlib.Credential {
	out := make([]webauthnlib.Credential, 0, len(u.user.Credentials))
	for _, record := range u.user.Credentials {
		out = append(out, webAuthnCredentialFromRecord(record))
	}
	return out
}

func webAuthnCredentialFromRecord(record CredentialRecord) webauthnlib.Credential {
	return webauthnlib.Credential{
		ID:              append([]byte(nil), record.CredentialID...),
		PublicKey:       append([]byte(nil), record.PublicKey...),
		AttestationType: stringsOrEmpty(record.AttestationType),
		Transport:       transportsFromStrings(record.Transports),
		Authenticator: webauthnlib.Authenticator{
			AAGUID:       append([]byte(nil), record.AAGUID...),
			SignCount:    record.SignCount,
			CloneWarning: record.CloneWarning,
		},
	}
}

func verifiedFromWebAuthnCredential(credential webauthnlib.Credential, fallbackCount uint32) VerifiedCredential {
	signCount := credential.Authenticator.SignCount
	if signCount == 0 && fallbackCount > 0 {
		signCount = fallbackCount
	}
	return VerifiedCredential{
		CredentialID:    append([]byte(nil), credential.ID...),
		PublicKey:       append([]byte(nil), credential.PublicKey...),
		SignCount:       signCount,
		AAGUID:          append([]byte(nil), credential.Authenticator.AAGUID...),
		AttestationType: credential.AttestationType,
		Transports:      stringsFromTransports(credential.Transport),
		CloneWarning:    credential.Authenticator.CloneWarning,
	}
}

func descriptorsFromRecords(records []CredentialRecord) []protocol.CredentialDescriptor {
	out := make([]protocol.CredentialDescriptor, 0, len(records))
	for _, record := range records {
		out = append(out, protocol.CredentialDescriptor{
			Type:         protocol.PublicKeyCredentialType,
			CredentialID: protocol.URLEncodedBase64(record.CredentialID),
			Transport:    transportsFromStrings(record.Transports),
		})
	}
	return out
}

func transportsFromStrings(values []string) []protocol.AuthenticatorTransport {
	out := make([]protocol.AuthenticatorTransport, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, protocol.AuthenticatorTransport(value))
		}
	}
	return out
}

func stringsFromTransports(values []protocol.AuthenticatorTransport) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, string(value))
		}
	}
	return out
}

func stringsOrEmpty(value string) string {
	return value
}
