package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const defaultAnthropicTokenEndpoint = "https://api.anthropic.com/v1/oauth/token"

// AnthropicRefresh 用 Anthropic OAuth refresh_token grant 刷新 Claude 账号。
type AnthropicRefresh struct {
	Endpoint   string
	ClientID   string
	HTTPClient *http.Client
}

func (r AnthropicRefresh) RefreshForProvider(ctx context.Context, accountID int64, providerName string, currentCredential []byte) ([]byte, time.Time, error) {
	cred, err := parseCredential(currentCredential)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("anthropic refresh account %d: %w", accountID, err)
	}
	refreshToken := credentialString(cred, "refresh_token")
	if refreshToken == "" {
		return nil, time.Time{}, fmt.Errorf("anthropic refresh account %d: refresh_token is empty", accountID)
	}

	body := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}
	if clientID := firstNonEmpty(r.ClientID, credentialString(cred, "client_id")); clientID != "" {
		body["client_id"] = clientID
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("anthropic refresh account %d: marshal request: %w", accountID, err)
	}

	resp, err := postTokenWithRetry(ctx, r.httpClient(), firstNonEmpty(r.Endpoint, credentialString(cred, "oauth_token_endpoint"), defaultAnthropicTokenEndpoint), "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("anthropic refresh account %d: %w", accountID, err)
	}
	newCredential, expiresAt, err := mergeTokenResponse(cred, resp)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("anthropic refresh account %d: %w", accountID, err)
	}
	return newCredential, expiresAt, nil
}

func (r AnthropicRefresh) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return http.DefaultClient
}
