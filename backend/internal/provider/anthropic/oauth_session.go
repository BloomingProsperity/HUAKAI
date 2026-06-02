package anthropic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

type OAuthSessionAdapter struct {
	Endpoint string
	Now      func() time.Time
}

func (a *OAuthSessionAdapter) Platform() string {
	return "anthropic_claude_session"
}

func (a *OAuthSessionAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeOAuthAccessToken,
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

func (a *OAuthSessionAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if in.Credential.Type == provider.CredentialTypeAPIKey {
		return nil, errors.New("anthropic oauth session: api key credentials must use the messages passthrough adapter")
	}
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("anthropic oauth session: unsupported credential type %q", in.Credential.Type)
	}
	if strings.TrimSpace(in.Credential.Value) == "" {
		return nil, errors.New("anthropic oauth session: credential value is empty")
	}
	if err := a.rejectExpired(in.Credential); err != nil {
		return nil, err
	}
	endpoint := a.Endpoint
	if endpoint == "" {
		endpoint = defaultMessagesEndpoint
	}
	endpoint, err := provider.EndpointForCredential(endpoint, in.Credential)
	if err != nil {
		return nil, fmt.Errorf("anthropic oauth session: endpoint rejected: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("anthropic oauth session: build request: %w", err)
	}
	switch in.Credential.Type {
	case provider.CredentialTypeOAuthAccessToken, provider.CredentialTypeSessionToken:
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(in.Credential.Value))
	case provider.CredentialTypeUpstreamPassthrough:
		if header := strings.TrimSpace(in.Credential.Extra["auth_header"]); header != "" {
			req.Header.Set(header, in.Credential.Value)
		} else {
			req.Header.Set("Authorization", in.Credential.Value)
		}
	}
	version := in.Credential.Extra["anthropic_version"]
	if version == "" {
		version = defaultAnthropicVersion
	}
	req.Header.Set("Anthropic-Version", version)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if betas := in.Credential.Extra["anthropic_beta"]; betas != "" {
		req.Header.Set("Anthropic-Beta", betas)
	}
	return req, nil
}

func (a *OAuthSessionAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, ok := range a.AcceptableCredentialTypes() {
		if ok == t {
			return true
		}
	}
	return false
}

func (a *OAuthSessionAdapter) rejectExpired(cred provider.Credential) error {
	raw := strings.TrimSpace(cred.Extra["expires_at"])
	if raw == "" {
		return nil
	}
	expiresAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil
	}
	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}
	if now.After(expiresAt.UTC()) {
		return fmt.Errorf("%w: anthropic oauth access token", credentialstore.ErrCredentialExpired)
	}
	return nil
}

var _ provider.Adapter = (*OAuthSessionAdapter)(nil)
