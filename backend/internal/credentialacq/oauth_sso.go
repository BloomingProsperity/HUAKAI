package credentialacq

import (
	"context"
	"errors"
)

type ssoExchanger struct{}

func NewSSOExchanger() Exchanger {
	return ssoExchanger{}
}

func (ssoExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	return startDeviceAuthorization(ctx, store, in, cfg, AuthTypeSSO)
}

func (ssoExchanger) ExchangeOAuthCode(context.Context, Session, string) (CredentialCandidate, error) {
	return CredentialCandidate{}, errors.New("credentialacq: sso flow does not use oauth callback exchange")
}

func PollSSOToken(ctx context.Context, session Session, cfg OAuthClientConfig, opts ...DeviceCodeOption) (CredentialCandidate, error) {
	return pollDeviceAuthorizationToken(ctx, session, cfg, AuthTypeSSO, opts...)
}
