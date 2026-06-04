package modelsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultOpenAIModelsURL    = "https://api.openai.com/v1/models"
	DefaultAnthropicModelsURL = "https://api.anthropic.com/v1/models"
	DefaultGeminiModelsURL    = "https://generativelanguage.googleapis.com/v1beta/models"

	defaultAnthropicVersion = "2023-06-01"
	defaultFetchTimeout     = 30 * time.Second
	maxModelListBytes       = 8 << 20
	maxCatalogPages         = 16
)

var ErrPaginationLimit = errors.New("modelsync: vendor model-list pagination limit reached")

type HTTPFetcherConfig struct {
	Vendor           Vendor
	URL              string
	APIKey           string
	Client           *http.Client
	Timeout          time.Duration
	AnthropicVersion string
}

type HTTPFetcher struct {
	vendor           Vendor
	rawURL           string
	apiKey           string
	client           *http.Client
	timeout          time.Duration
	anthropicVersion string
}

func NewHTTPFetcher(cfg HTTPFetcherConfig) *HTTPFetcher {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultFetchTimeout
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	// 安全:本 fetcher 携带 vendor API key(Gemini 在 query、其他在 header)。
	// 拒绝跟随重定向 —— 防止恶意/被攻陷上游用 3xx 把 key 泄漏到攻击者主机。
	// ErrUseLastResponse 让 3xx 原样返回(getJSON 按非 2xx 报错),key 永不外发。
	if client.CheckRedirect == nil {
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	rawURL := strings.TrimSpace(cfg.URL)
	if rawURL == "" {
		rawURL = DefaultURLForVendor(cfg.Vendor)
	}
	version := strings.TrimSpace(cfg.AnthropicVersion)
	if version == "" {
		version = defaultAnthropicVersion
	}
	return &HTTPFetcher{
		vendor:           cfg.Vendor,
		rawURL:           rawURL,
		apiKey:           strings.TrimSpace(cfg.APIKey),
		client:           client,
		timeout:          timeout,
		anthropicVersion: version,
	}
}

func DefaultURLForVendor(v Vendor) string {
	switch v {
	case VendorOpenAI:
		return DefaultOpenAIModelsURL
	case VendorAnthropic:
		return DefaultAnthropicModelsURL
	case VendorGemini:
		return DefaultGeminiModelsURL
	default:
		return ""
	}
}

func (f *HTTPFetcher) FetchCatalog(ctx context.Context) (Catalog, error) {
	if f == nil {
		return Catalog{}, fmt.Errorf("modelsync: nil HTTP fetcher")
	}
	switch f.vendor {
	case VendorOpenAI:
		return f.fetchOpenAI(ctx)
	case VendorAnthropic:
		return f.fetchAnthropic(ctx)
	case VendorGemini:
		return f.fetchGemini(ctx)
	default:
		return Catalog{}, fmt.Errorf("modelsync: unsupported vendor %q", f.vendor)
	}
}

func (f *HTTPFetcher) fetchOpenAI(ctx context.Context) (Catalog, error) {
	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := f.getJSON(ctx, f.rawURL, func(req *http.Request) {
		if f.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+f.apiKey)
		}
	}, &payload); err != nil {
		return Catalog{}, err
	}
	models := make([]Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if !isOpenAIChatModelID(id) {
			continue
		}
		created := time.Time{}
		if item.Created > 0 {
			created = time.Unix(item.Created, 0).UTC()
		}
		owner := strings.TrimSpace(item.OwnedBy)
		if owner == "" {
			owner = "openai"
		}
		models = append(models, Model{
			ID:             id,
			DisplayName:    id,
			OwnedBy:        owner,
			ProtocolFamily: "openai_chat",
			CreatedAt:      created,
			Capabilities:   []string{"chat"},
		})
	}
	return Catalog{Vendor: VendorOpenAI, Models: models}, nil
}

func (f *HTTPFetcher) fetchAnthropic(ctx context.Context) (Catalog, error) {
	models := make([]Model, 0)
	nextURL := f.rawURL
	for page := 0; page < maxCatalogPages; page++ {
		var payload struct {
			Data []struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
				CreatedAt   string `json:"created_at"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := f.getJSON(ctx, nextURL, func(req *http.Request) {
			if f.apiKey != "" {
				req.Header.Set("X-Api-Key", f.apiKey)
			}
			req.Header.Set("Anthropic-Version", f.anthropicVersion)
		}, &payload); err != nil {
			return Catalog{}, err
		}
		for _, item := range payload.Data {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				continue
			}
			display := strings.TrimSpace(item.DisplayName)
			if display == "" {
				display = id
			}
			models = append(models, Model{
				ID:             id,
				DisplayName:    display,
				OwnedBy:        "anthropic",
				ProtocolFamily: "anthropic_messages",
				CreatedAt:      parseRFC3339UTC(item.CreatedAt),
				Capabilities:   []string{"messages"},
			})
		}
		lastID := strings.TrimSpace(payload.LastID)
		if !payload.HasMore || lastID == "" {
			break
		}
		if page == maxCatalogPages-1 {
			return Catalog{}, fmt.Errorf("%w: %s has more pages", ErrPaginationLimit, f.vendor)
		}
		nextURL = withQueryParam(f.rawURL, "after_id", lastID)
	}
	return Catalog{Vendor: VendorAnthropic, Models: models}, nil
}

func (f *HTTPFetcher) fetchGemini(ctx context.Context) (Catalog, error) {
	models := make([]Model, 0)
	nextURL := f.geminiURLWithKey(f.rawURL)
	for page := 0; page < maxCatalogPages; page++ {
		var payload struct {
			Models []struct {
				Name                       string   `json:"name"`
				DisplayName                string   `json:"displayName"`
				InputTokenLimit            int      `json:"inputTokenLimit"`
				SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := f.getJSON(ctx, nextURL, nil, &payload); err != nil {
			return Catalog{}, err
		}
		for _, item := range payload.Models {
			id := strings.TrimPrefix(strings.TrimSpace(item.Name), "models/")
			if id == "" {
				continue
			}
			display := strings.TrimSpace(item.DisplayName)
			if display == "" {
				display = id
			}
			models = append(models, Model{
				ID:             id,
				DisplayName:    display,
				OwnedBy:        "google",
				ProtocolFamily: "gemini",
				ContextWindow:  item.InputTokenLimit,
				Capabilities:   compactStrings(item.SupportedGenerationMethods),
			})
		}
		token := strings.TrimSpace(payload.NextPageToken)
		if token == "" {
			break
		}
		if page == maxCatalogPages-1 {
			return Catalog{}, fmt.Errorf("%w: %s has more pages", ErrPaginationLimit, f.vendor)
		}
		nextURL = withQueryParam(f.geminiURLWithKey(f.rawURL), "pageToken", token)
	}
	return Catalog{Vendor: VendorGemini, Models: models}, nil
}

func (f *HTTPFetcher) getJSON(ctx context.Context, rawURL string, customize func(*http.Request), dst any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("modelsync: %s request build failed: %w", f.vendor, err)
	}
	req.Header.Set("Accept", "application/json")
	if customize != nil {
		customize(req)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("modelsync: %s fetch failed: %w", f.vendor, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("modelsync: %s fetch status %d", f.vendor, resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxModelListBytes)
	if err := json.NewDecoder(limited).Decode(dst); err != nil {
		return fmt.Errorf("modelsync: %s decode failed: %w", f.vendor, err)
	}
	return nil
}

func (f *HTTPFetcher) geminiURLWithKey(rawURL string) string {
	if f.apiKey == "" {
		return rawURL
	}
	return withQueryParam(rawURL, "key", f.apiKey)
}

func withQueryParam(rawURL, key, value string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}

func parseRFC3339UTC(raw string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func isOpenAIChatModelID(id string) bool {
	lower := strings.ToLower(strings.TrimSpace(id))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"embedding", "audio", "whisper", "tts", "dall-e", "image",
		"moderation", "realtime", "transcribe", "speech",
	} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	if strings.HasPrefix(lower, "ft:gpt-") {
		return true
	}
	for _, prefix := range []string{"gpt-", "o1", "o3", "o4", "chatgpt-"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}
