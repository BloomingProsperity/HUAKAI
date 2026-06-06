package moderation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

const (
	DefaultExternalModerationModel      = "omni-moderation-latest"
	DefaultExternalModerationTimeoutMS  = 3000
	DefaultExternalModerationRetryCount = 2
	MaxExternalModerationTimeoutMS      = 30000
	MaxExternalModerationRetryCount     = 5
	ExternalImageRawMaxBytes            = 8 * 1024 * 1024
	ExternalImageDataURLMaxBytes        = 12 * 1024 * 1024

	externalRateLimitFreeze  = time.Minute
	externalAuthFreeze       = 10 * time.Minute
	externalHTTPErrorFreeze  = 10 * time.Second
	externalResponseMaxBytes = 1 << 20
)

var ErrExternalModerationConfig = errors.New("moderation: invalid external moderation config")

type ExternalModeratorDeps struct {
	HTTPClient *http.Client
	Now        func() time.Time
}

type ExternalModeratorClient struct {
	httpClient *http.Client
	now        func() time.Time

	mu        sync.Mutex
	nextKey   int
	frozenKey map[string]time.Time
}

func DefaultExternalModerationConfig() ExternalModerationConfig {
	return ExternalModerationConfig{
		Model:      DefaultExternalModerationModel,
		TimeoutMS:  DefaultExternalModerationTimeoutMS,
		RetryCount: DefaultExternalModerationRetryCount,
	}
}

func NewExternalModerator(deps ExternalModeratorDeps) *ExternalModeratorClient {
	client := deps.HTTPClient
	if client == nil {
		client = auth.NewSSRFProtectedOAuthClient(nil)
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ExternalModeratorClient{
		httpClient: client,
		now:        now,
		frozenKey:  make(map[string]time.Time),
	}
}

func (m *ExternalModeratorClient) ScreenExternal(ctx context.Context, req ScreenRequest, cfg ExternalModerationConfig) (ExternalModerationResult, error) {
	if m == nil {
		return ExternalModerationResult{}, errors.New("moderation: external moderator not configured")
	}
	cfg = normalizeExternalModerationRuntimeConfig(cfg)
	if !cfg.Enabled {
		return ExternalModerationResult{}, nil
	}
	if res, ok := externalImageCapResult(req, cfg); ok {
		return res, nil
	}
	if err := validateExternalEndpoint(cfg.BaseURL); err != nil {
		return ExternalModerationResult{}, err
	}
	keys := normalizedExternalAPIKeys(cfg.APIKeys)
	if len(keys) == 0 {
		return ExternalModerationResult{}, fmt.Errorf("%w: no api keys", ErrExternalModerationConfig)
	}
	payload, err := json.Marshal(externalModerationRequest{
		Model: cfg.Model,
		Input: externalModerationInput(req, cfg),
	})
	if err != nil {
		return ExternalModerationResult{}, err
	}

	var lastErr error
	for attempt := 0; attempt <= cfg.RetryCount; attempt++ {
		key, ok := m.selectKey(keys)
		if !ok {
			if lastErr != nil {
				return ExternalModerationResult{}, lastErr
			}
			return ExternalModerationResult{}, fmt.Errorf("%w: all api keys frozen", ErrExternalModerationConfig)
		}
		res, status, err := m.post(ctx, cfg, key, payload)
		if err == nil {
			return res, nil
		}
		lastErr = err
		m.freezeKey(key, freezeDurationForStatus(status))
	}
	return ExternalModerationResult{}, lastErr
}

type externalModerationRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

type externalModerationResponse struct {
	Results []externalModerationResponseResult `json:"results"`
}

type externalModerationResponseResult struct {
	Flagged        bool               `json:"flagged"`
	Categories     map[string]bool    `json:"categories"`
	CategoryScores map[string]float64 `json:"category_scores"`
}

func (m *ExternalModeratorClient) post(ctx context.Context, cfg ExternalModerationConfig, apiKey string, payload []byte) (ExternalModerationResult, int, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMS)*time.Millisecond)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, cfg.BaseURL, bytes.NewReader(payload))
	if err != nil {
		return ExternalModerationResult{}, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return ExternalModerationResult{}, 0, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, externalResponseMaxBytes+1))
	if readErr != nil {
		return ExternalModerationResult{}, resp.StatusCode, readErr
	}
	if len(body) > externalResponseMaxBytes {
		return ExternalModerationResult{}, resp.StatusCode, errors.New("moderation: external response too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ExternalModerationResult{}, resp.StatusCode, fmt.Errorf("moderation: external status %d", resp.StatusCode)
	}
	var decoded externalModerationResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ExternalModerationResult{}, resp.StatusCode, err
	}
	return evaluateExternalModeration(decoded, cfg), resp.StatusCode, nil
}

func normalizeExternalModerationRuntimeConfig(cfg ExternalModerationConfig) ExternalModerationConfig {
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Model == "" {
		cfg.Model = DefaultExternalModerationModel
	}
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = DefaultExternalModerationTimeoutMS
	}
	if cfg.TimeoutMS > MaxExternalModerationTimeoutMS {
		cfg.TimeoutMS = MaxExternalModerationTimeoutMS
	}
	if cfg.RetryCount < 0 {
		cfg.RetryCount = DefaultExternalModerationRetryCount
	}
	if cfg.RetryCount > MaxExternalModerationRetryCount {
		cfg.RetryCount = MaxExternalModerationRetryCount
	}
	return cfg
}

func validateExternalEndpoint(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%w: base url must be http(s)", ErrExternalModerationConfig)
	}
	return nil
}

func normalizedExternalAPIKeys(in []string) []string {
	out := make([]string, 0, len(in))
	for _, key := range in {
		key = strings.TrimSpace(key)
		if key != "" {
			out = append(out, key)
		}
	}
	return out
}

func (m *ExternalModeratorClient) selectKey(keys []string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(keys) == 0 {
		return "", false
	}
	now := m.now()
	start := m.nextKey % len(keys)
	for i := 0; i < len(keys); i++ {
		idx := (start + i) % len(keys)
		key := keys[idx]
		if until, frozen := m.frozenKey[key]; frozen && now.Before(until) {
			continue
		}
		delete(m.frozenKey, key)
		m.nextKey = (idx + 1) % len(keys)
		return key, true
	}
	return "", false
}

func (m *ExternalModeratorClient) freezeKey(key string, d time.Duration) {
	if d <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.frozenKey[key] = m.now().Add(d)
}

func freezeDurationForStatus(status int) time.Duration {
	switch status {
	case http.StatusTooManyRequests:
		return externalRateLimitFreeze
	case http.StatusUnauthorized, http.StatusForbidden:
		return externalAuthFreeze
	default:
		return externalHTTPErrorFreeze
	}
}

func evaluateExternalModeration(resp externalModerationResponse, cfg ExternalModerationConfig) ExternalModerationResult {
	for _, result := range resp.Results {
		if len(cfg.Thresholds) > 0 {
			categories := sortedFloatKeys(cfg.Thresholds)
			for _, category := range categories {
				score, ok := result.CategoryScores[category]
				if !ok {
					continue
				}
				threshold := cfg.Thresholds[category]
				if score >= threshold {
					return ExternalModerationResult{
						Blocked:    true,
						ReasonCode: externalReasonCode(category),
						Category:   category,
						Score:      score,
						Threshold:  threshold,
					}
				}
			}
			continue
		}
		if result.Flagged {
			category := firstFlaggedExternalCategory(result.Categories)
			return ExternalModerationResult{
				Blocked:    true,
				ReasonCode: externalReasonCode(category),
				Category:   category,
				Score:      result.CategoryScores[category],
			}
		}
	}
	return ExternalModerationResult{}
}

func sortedFloatKeys(values map[string]float64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func firstFlaggedExternalCategory(categories map[string]bool) string {
	keys := make([]string, 0, len(categories))
	for category, flagged := range categories {
		if flagged {
			keys = append(keys, category)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func externalReasonCode(category string) string {
	category = sanitizeExternalCategory(category)
	if category == "" {
		return "external_moderation_flagged"
	}
	return "external_moderation:" + category
}

func sanitizeExternalCategory(category string) string {
	category = strings.TrimSpace(strings.ToLower(category))
	var b strings.Builder
	for _, r := range category {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return strings.Trim(b.String(), "_")
}

func externalModerationInput(req ScreenRequest, cfg ExternalModerationConfig) any {
	if !cfg.ImageEnabled || len(req.ImageDataURLs) == 0 {
		return string(req.Body)
	}
	parts := make([]map[string]any, 0, 1+len(req.ImageDataURLs))
	if len(req.Body) > 0 {
		parts = append(parts, map[string]any{
			"type": "text",
			"text": string(req.Body),
		})
	}
	for _, imageURL := range req.ImageDataURLs {
		parts = append(parts, map[string]any{
			"type": "image_url",
			"image_url": map[string]string{
				"url": imageURL,
			},
		})
	}
	return parts
}

func externalImageCapResult(req ScreenRequest, cfg ExternalModerationConfig) (ExternalModerationResult, bool) {
	if !cfg.ImageEnabled {
		return ExternalModerationResult{}, false
	}
	for _, imageURL := range req.ImageDataURLs {
		if err := validateExternalImageDataURL(imageURL); err != nil {
			return ExternalModerationResult{
				Blocked:    true,
				ReasonCode: "external_image_too_large",
			}, true
		}
	}
	return ExternalModerationResult{}, false
}

func validateExternalImageDataURL(imageURL string) error {
	if len(imageURL) > ExternalImageDataURLMaxBytes {
		return errors.New("moderation: image data url too large")
	}
	prefix, payload, ok := strings.Cut(imageURL, ",")
	if !ok || !strings.HasPrefix(strings.ToLower(prefix), "data:image/") {
		return errors.New("moderation: invalid image data url")
	}
	if !strings.Contains(strings.ToLower(prefix), ";base64") {
		if len(payload) > ExternalImageRawMaxBytes {
			return errors.New("moderation: image payload too large")
		}
		return nil
	}
	if base64.StdEncoding.DecodedLen(len(payload)) > ExternalImageRawMaxBytes+2 {
		return errors.New("moderation: image raw bytes too large")
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return err
	}
	if len(decoded) > ExternalImageRawMaxBytes {
		return errors.New("moderation: image raw bytes too large")
	}
	return nil
}

var _ ExternalModerator = (*ExternalModeratorClient)(nil)
