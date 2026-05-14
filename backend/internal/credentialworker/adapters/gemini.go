package adapters

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultGeminiTokenEndpoint = "https://oauth2.googleapis.com/token"

// GeminiRefresh 用 Google OAuth refresh_token grant 刷新 Gemini 账号。
type GeminiRefresh struct {
	Endpoint     string
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
}

func (r GeminiRefresh) RefreshForProvider(ctx context.Context, accountID int64, providerName string, currentCredential []byte) ([]byte, time.Time, error) {
	cred, err := parseCredential(currentCredential)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("gemini refresh account %d: %w", accountID, err)
	}
	refreshToken := credentialString(cred, "refresh_token")
	if refreshToken == "" {
		return nil, time.Time{}, fmt.Errorf("gemini refresh account %d: refresh_token is empty", accountID)
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	if clientID := firstNonEmpty(r.ClientID, credentialString(cred, "client_id")); clientID != "" {
		form.Set("client_id", clientID)
	}
	if clientSecret := firstNonEmpty(r.ClientSecret, credentialString(cred, "client_secret")); clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}

	resp, err := postTokenWithRetry(ctx, r.httpClient(), firstNonEmpty(r.Endpoint, credentialString(cred, "oauth_token_endpoint"), defaultGeminiTokenEndpoint), "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("gemini refresh account %d: %w", accountID, err)
	}
	newCredential, expiresAt, err := mergeTokenResponse(cred, resp)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("gemini refresh account %d: %w", accountID, err)
	}
	return newCredential, expiresAt, nil
}

func (r GeminiRefresh) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return http.DefaultClient
}
