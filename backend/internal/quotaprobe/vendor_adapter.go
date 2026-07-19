package quotaprobe

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/accountquota"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// VendorAdapter 把厂商只读额度接口转换为统一事实，不负责写数据库。
type VendorAdapter interface {
	Supports(provider.Credential, provider.AccountInfo) bool
	Source() accountquota.Source
	Fetch(context.Context, int64, provider.Credential, provider.AccountInfo, time.Time) (VendorResult, error)
}

type VendorResult struct {
	Source     accountquota.Source
	Facts      []accountquota.Fact
	Complete   bool
	ErrorClass string
}

type adapterHTTP struct {
	client        *http.Client
	proxyResolver provider.ProxyResolver
}

func newAdapterHTTP(client *http.Client, proxyResolver provider.ProxyResolver) adapterHTTP {
	if client == nil {
		client = http.DefaultClient
	}
	return adapterHTTP{client: client, proxyResolver: proxyResolver}
}

func (h adapterHTTP) clientForAccount(ctx context.Context, accountID int64) (*http.Client, error) {
	if h.client == nil {
		return nil, ErrNotConfigured
	}
	client := *h.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if h.proxyResolver == nil || accountID <= 0 {
		return &client, nil
	}
	proxyURL, err := h.proxyResolver.Resolve(ctx, accountID)
	if errors.Is(err, provider.ErrAccountNotFound) {
		return &client, nil
	}
	if err != nil {
		return nil, withErrorClass(ErrorClassProxyResolutionFailed, err)
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = provider.WrapTransportWithProxy(transport, proxyURL)
	return &client, nil
}

func accessToken(credential provider.Credential) string {
	value := strings.TrimSpace(credential.Value)
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		return strings.TrimSpace(value[len("bearer "):])
	}
	return value
}

func validUntil(observedAt time.Time) *time.Time {
	until := observedAt.UTC().Add(2 * time.Hour)
	return &until
}

// GeminiUnknownAdapter 明确记录当前没有可信上游额度接口，避免套用静态套餐估算。
type GeminiUnknownAdapter struct{}

func (GeminiUnknownAdapter) Supports(_ provider.Credential, info provider.AccountInfo) bool {
	return strings.EqualFold(strings.TrimSpace(info.Platform), "gemini")
}

func (GeminiUnknownAdapter) Source() accountquota.Source {
	return accountquota.SourceCapabilityContract
}

func (GeminiUnknownAdapter) Fetch(_ context.Context, _ int64, _ provider.Credential, _ provider.AccountInfo, observedAt time.Time) (VendorResult, error) {
	return VendorResult{Source: accountquota.SourceCapabilityContract, Complete: true, Facts: []accountquota.Fact{{
		MetricKey: "model_quota", State: accountquota.StateUnknown, ValidUntil: validUntil(observedAt),
	}}}, nil
}
