package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultGeminiTokenEndpoint = "https://oauth2.googleapis.com/token"

// GeminiRefresh 用 Google OAuth refresh_token grant 刷新 Gemini 账号。
type GeminiRefresh struct {
	Endpoint                 string
	ClientID                 string
	ClientSecret             string
	HTTPClient               *http.Client
	AllowCrossClientFallback bool
	SourceClientFamily       string
	TierCacheTTL             time.Duration
}

type GeminiFallbackError struct {
	FromClient string
	ToClient   string
	Err        error
}

func (e *GeminiFallbackError) Error() string {
	if e == nil || e.Err == nil {
		return "gemini cross-client fallback failed"
	}
	return e.Err.Error()
}

func (e *GeminiFallbackError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
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
	clientID := firstNonEmpty(r.ClientID, credentialString(cred, "client_id"))
	if clientID != "" {
		form.Set("client_id", clientID)
	}
	if clientSecret := firstNonEmpty(r.ClientSecret, credentialString(cred, "client_secret")); clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}

	resp, err := postTokenWithRetry(ctx, r.httpClient(), firstNonEmpty(r.Endpoint, credentialString(cred, "oauth_token_endpoint"), defaultGeminiTokenEndpoint), "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil && r.AllowCrossClientFallback {
		fromClient, toClient := r.fallbackFamilies(cred, providerName)
		if fallbackClientID := credentialString(cred, "fallback_client_id"); fallbackClientID != "" && fallbackClientID != clientID && ApprovedGeminiCrossClientFallback(fromClient, toClient) {
			form.Set("client_id", fallbackClientID)
			cred["cross_client_fallback_attempted"] = true
			cred["cross_client_fallback_from"] = fromClient
			cred["cross_client_fallback_to"] = toClient
			resp, err = postTokenWithRetry(ctx, r.httpClient(), firstNonEmpty(r.Endpoint, credentialString(cred, "oauth_token_endpoint"), defaultGeminiTokenEndpoint), "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
			if err != nil {
				return nil, time.Time{}, &GeminiFallbackError{FromClient: fromClient, ToClient: toClient, Err: fmt.Errorf("gemini refresh account %d: %w", accountID, err)}
			}
		}
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("gemini refresh account %d: %w", accountID, err)
	}
	if cred["cross_client_fallback_attempted"] == true {
		cred["cross_client_fallback_success"] = true
	}
	newCredential, expiresAt, err := mergeTokenResponse(cred, resp)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("gemini refresh account %d: %w", accountID, err)
	}
	newCredential, err = applyGeminiTierCacheMetadata(newCredential, r.TierCacheTTL)
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

func (r GeminiRefresh) fallbackFamilies(cred map[string]any, providerName string) (string, string) {
	fromClient := normalizeGeminiClientFamily(firstNonEmpty(credentialString(cred, "client_family"), r.SourceClientFamily, providerName))
	toClient := normalizeGeminiClientFamily(firstNonEmpty(credentialString(cred, "fallback_client_family"), "ai_studio"))
	return fromClient, toClient
}

func ApprovedGeminiCrossClientFallback(fromClient, toClient string) bool {
	fromClient = normalizeGeminiClientFamily(fromClient)
	toClient = normalizeGeminiClientFamily(toClient)
	if fromClient == "" || toClient == "" || fromClient == toClient {
		return false
	}
	allowed := map[string]map[string]bool{
		"code_assist": {"ai_studio": true, "google_one": true},
		"google_one":  {"ai_studio": true, "code_assist": true},
		"ai_studio":   {"code_assist": false, "google_one": false},
	}
	return allowed[fromClient][toClient]
}

func GeminiCrossClientFallbackMetadata(raw []byte) (fromClient, toClient string, attempted bool) {
	cred, err := parseCredential(raw)
	if err != nil {
		return "", "", false
	}
	if cred["cross_client_fallback_attempted"] != true {
		return "", "", false
	}
	return normalizeGeminiClientFamily(credentialString(cred, "cross_client_fallback_from")),
		normalizeGeminiClientFamily(credentialString(cred, "cross_client_fallback_to")),
		true
}

func normalizeGeminiClientFamily(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "code_assist", "gemini_code_assist", "gemini":
		return "code_assist"
	case "google_one", "googleone":
		return "google_one"
	case "ai_studio", "aistudio", "aistudio_api_key":
		return "ai_studio"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func applyGeminiTierCacheMetadata(raw []byte, ttl time.Duration) ([]byte, error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	cred, err := parseCredential(raw)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if checked := credentialString(cred, "drive_tier_checked_at"); checked != "" {
		if parsed, err := time.Parse(time.RFC3339, checked); err == nil && now.Sub(parsed) < ttl {
			cred["drive_tier_cache_status"] = "fresh"
			return json.Marshal(cred)
		}
	}
	cred["drive_tier_cache_status"] = "stale"
	cred["drive_tier_checked_at"] = now.Format(time.RFC3339)
	return json.Marshal(cred)
}
