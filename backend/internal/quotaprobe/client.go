package quotaprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

const DefaultUsageEndpoint = "https://api.anthropic.com/api/oauth/usage"

const (
	oauthUsageBeta       = "oauth-2025-04-20"
	maxUsageResponseSize = 64 << 10
)

type HTTPUsageFetcher struct {
	client        *http.Client
	proxyResolver provider.ProxyResolver
	endpoint      string
}

func NewHTTPUsageFetcher(client *http.Client, proxyResolver provider.ProxyResolver) *HTTPUsageFetcher {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPUsageFetcher{client: client, proxyResolver: proxyResolver, endpoint: DefaultUsageEndpoint}
}

func (f *HTTPUsageFetcher) FetchUsage(ctx context.Context, accountID int64, accessToken string) (UsageSnapshot, error) {
	if f == nil || f.client == nil || strings.TrimSpace(f.endpoint) == "" {
		return UsageSnapshot{}, withErrorClass(ErrorClassConfigurationInvalid, ErrNotConfigured)
	}
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return UsageSnapshot{}, withErrorClass(ErrorClassCredentialUnavailable, errors.New("quota probe 缺少 access token"))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.endpoint, nil)
	if err != nil {
		return UsageSnapshot{}, withErrorClass(ErrorClassConfigurationInvalid, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Anthropic-Beta", oauthUsageBeta)
	req.Header.Set("User-Agent", "HUAKAI-Quota-Probe/1.0")

	client, err := f.clientForAccount(ctx, accountID)
	if err != nil {
		return UsageSnapshot{}, withErrorClass(ErrorClassProxyResolutionFailed, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return UsageSnapshot{}, withErrorClass(ErrorClassUpstreamUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return UsageSnapshot{}, upstreamStatusError(resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUsageResponseSize+1))
	if err != nil {
		return UsageSnapshot{}, withErrorClass(ErrorClassUpstreamResponseInvalid, err)
	}
	if len(body) > maxUsageResponseSize {
		return UsageSnapshot{}, withErrorClass(ErrorClassUpstreamResponseInvalid, errors.New("quota probe usage 响应过大"))
	}
	var decoded struct {
		FiveHour usageWindowJSON `json:"five_hour"`
		SevenDay usageWindowJSON `json:"seven_day"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return UsageSnapshot{}, withErrorClass(ErrorClassUpstreamResponseInvalid, fmt.Errorf("quota probe usage 响应格式无效: %w", err))
	}
	fiveHour, err := decoded.FiveHour.window()
	if err != nil {
		return UsageSnapshot{}, withErrorClass(ErrorClassUpstreamResponseInvalid, fmt.Errorf("quota probe 5h 窗口无效: %w", err))
	}
	sevenDay, err := decoded.SevenDay.window()
	if err != nil {
		return UsageSnapshot{}, withErrorClass(ErrorClassUpstreamResponseInvalid, fmt.Errorf("quota probe 7d 窗口无效: %w", err))
	}
	return UsageSnapshot{FiveHour: fiveHour, SevenDay: sevenDay}, nil
}

type usageWindowJSON struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

func (w usageWindowJSON) window() (UsageWindow, error) {
	window := UsageWindow{Utilization: w.Utilization}
	if w.ResetsAt == nil || strings.TrimSpace(*w.ResetsAt) == "" {
		return window, nil
	}
	resetAt, err := time.Parse(time.RFC3339, strings.TrimSpace(*w.ResetsAt))
	if err != nil {
		return UsageWindow{}, err
	}
	resetAt = resetAt.UTC()
	window.ResetsAt = &resetAt
	return window, nil
}

func (f *HTTPUsageFetcher) clientForAccount(ctx context.Context, accountID int64) (*http.Client, error) {
	client := *f.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if f.proxyResolver == nil || accountID <= 0 {
		return &client, nil
	}
	proxyURL, err := f.proxyResolver.Resolve(ctx, accountID)
	if errors.Is(err, provider.ErrAccountNotFound) {
		return &client, nil
	}
	if err != nil {
		return nil, withErrorClass(ErrorClassProxyResolutionFailed, fmt.Errorf("quota probe 解析账号代理失败: %w", err))
	}
	transport := f.client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = provider.WrapTransportWithProxy(transport, proxyURL)
	return &client, nil
}
