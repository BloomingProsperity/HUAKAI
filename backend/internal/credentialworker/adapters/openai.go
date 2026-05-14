package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultOpenAITokenEndpoint = "https://auth.openai.com/oauth/token"
	defaultRefreshTimeout      = 5 * time.Second
)

// OpenAIRefresh 用标准 refresh_token grant 刷新 OpenAI / ChatGPT 账号。
type OpenAIRefresh struct {
	Endpoint   string
	ClientID   string
	Scope      string
	HTTPClient *http.Client
}

func (r OpenAIRefresh) RefreshForProvider(ctx context.Context, accountID int64, providerName string, currentCredential []byte) ([]byte, time.Time, error) {
	cred, err := parseCredential(currentCredential)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("openai refresh account %d: %w", accountID, err)
	}
	refreshToken := credentialString(cred, "refresh_token")
	if refreshToken == "" {
		return nil, time.Time{}, fmt.Errorf("openai refresh account %d: refresh_token is empty", accountID)
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	if clientID := firstNonEmpty(r.ClientID, credentialString(cred, "client_id")); clientID != "" {
		form.Set("client_id", clientID)
	}
	if clientSecret := credentialString(cred, "client_secret"); clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	if scope := firstNonEmpty(r.Scope, credentialString(cred, "scope")); scope != "" {
		form.Set("scope", scope)
	}

	resp, err := postTokenWithRetry(ctx, r.httpClient(), firstNonEmpty(r.Endpoint, credentialString(cred, "oauth_token_endpoint"), defaultOpenAITokenEndpoint), "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("openai refresh account %d: %w", accountID, err)
	}
	newCredential, expiresAt, err := mergeTokenResponse(cred, resp)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("openai refresh account %d: %w", accountID, err)
	}
	return newCredential, expiresAt, nil
}

func (r OpenAIRefresh) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return http.DefaultClient
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

func parseCredential(raw []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("credential json is empty")
	}
	var cred map[string]any
	if err := json.Unmarshal(raw, &cred); err != nil {
		return nil, fmt.Errorf("credential json invalid: %w", err)
	}
	return cred, nil
}

func credentialString(cred map[string]any, key string) string {
	switch v := cred[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strings.TrimSpace(strconv.FormatFloat(v, 'f', -1, 64))
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func postTokenWithRetry(ctx context.Context, client *http.Client, endpoint, contentType string, body io.Reader) (tokenResponse, error) {
	var payload []byte
	if body != nil {
		data, err := io.ReadAll(body)
		if err != nil {
			return tokenResponse{}, err
		}
		payload = data
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		reqCtx, cancel := context.WithTimeout(ctx, defaultRefreshTimeout)
		resp, err := postTokenOnce(reqCtx, client, endpoint, contentType, payload)
		cancel()
		if err == nil {
			return resp, nil
		}
		last = err
		if !isRetryableTokenError(err) {
			break
		}
	}
	return tokenResponse{}, last
}

type tokenHTTPError struct {
	status int
}

func (e tokenHTTPError) Error() string {
	return fmt.Sprintf("oauth token endpoint returned status %d", e.status)
}

func postTokenOnce(ctx context.Context, client *http.Client, endpoint, contentType string, payload []byte) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	resp, err := client.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return tokenResponse{}, tokenHTTPError{status: resp.StatusCode}
	}
	var out tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return tokenResponse{}, fmt.Errorf("oauth token response invalid: %w", err)
	}
	return out, nil
}

func isRetryableTokenError(err error) bool {
	var httpErr tokenHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.status == http.StatusTooManyRequests || httpErr.status >= 500
	}
	return true
}

func mergeTokenResponse(cred map[string]any, resp tokenResponse) ([]byte, time.Time, error) {
	if strings.TrimSpace(resp.AccessToken) == "" {
		return nil, time.Time{}, errors.New("oauth token response missing access_token")
	}
	ttl := resp.ExpiresIn
	if ttl <= 0 {
		ttl = 3600
	}
	expiresAt := time.Now().UTC().Add(time.Duration(ttl) * time.Second)
	cred["access_token"] = resp.AccessToken
	cred["expires_at"] = expiresAt.Format(time.RFC3339)
	if strings.TrimSpace(resp.RefreshToken) != "" {
		cred["refresh_token"] = resp.RefreshToken
	}
	if strings.TrimSpace(resp.IDToken) != "" {
		cred["id_token"] = resp.IDToken
	}
	if strings.TrimSpace(resp.TokenType) != "" {
		cred["token_type"] = resp.TokenType
	}
	if strings.TrimSpace(resp.Scope) != "" {
		cred["scope"] = resp.Scope
	}
	newCredential, err := json.Marshal(cred)
	return newCredential, expiresAt, err
}
