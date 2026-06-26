package adapters

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrCodexOAuthConfigRequired = errors.New("codex refresh: operator OAuth config required")

// CodexRefresh 使用与 OpenAI 兼容的 refresh token 交换，但不接受来自 credential
// JSON 的 OAuth endpoint/client/scope。
type CodexRefresh struct {
	OpenAI OpenAIRefresh
}

func (r CodexRefresh) RefreshForProvider(ctx context.Context, accountID int64, providerName string, currentCredential []byte) ([]byte, time.Time, error) {
	cred, err := parseCredential(currentCredential)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("codex refresh account %d: %w", accountID, err)
	}
	refreshToken := credentialString(cred, "refresh_token")
	if refreshToken == "" {
		return nil, time.Time{}, fmt.Errorf("codex refresh account %d: refresh_token is empty", accountID)
	}
	endpoint := strings.TrimSpace(r.OpenAI.Endpoint)
	clientID := strings.TrimSpace(r.OpenAI.ClientID)
	scope := strings.TrimSpace(r.OpenAI.Scope)
	if endpoint == "" || clientID == "" || scope == "" {
		return nil, time.Time{}, fmt.Errorf("codex refresh account %d: %w", accountID, ErrCodexOAuthConfigRequired)
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	form.Set("scope", scope)
	resp, err := postTokenWithRetry(ctx, r.OpenAI.httpClient(), endpoint, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("codex refresh account %d: %w", accountID, err)
	}
	newCredential, expiresAt, err := mergeTokenResponse(cred, resp)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("codex refresh account %d: %w", accountID, err)
	}
	newCredential, err = applyOpenAIRefreshMetadata(newCredential, r.OpenAI.PrivacyMutationEnabled)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("codex refresh account %d: %w", accountID, err)
	}
	return newCredential, expiresAt, nil
}

func NewCodexRefresh(endpoint, clientID, scope string, client *http.Client) CodexRefresh {
	return CodexRefresh{OpenAI: OpenAIRefresh{
		Endpoint:   endpoint,
		ClientID:   clientID,
		Scope:      scope,
		HTTPClient: client,
	}}
}
