package credentialworker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// RefreshAdapter 是 vendor OAuth 刷新的最小契约。
// 输入 currentCredential 必须是 provider_accounts.credentials 的原始 JSONB；
// 返回 newCredential 仍是可直接写回该字段的 JSON 字节。
type RefreshAdapter interface {
	RefreshForProvider(ctx context.Context, accountID int64, providerName string, currentCredential []byte) (newCredential []byte, expiresAt time.Time, err error)
}

// AdapterRegistry 保存 provider name 到刷新 adapter 的注册关系。
// registry 只按规范化后的 provider 名称匹配，避免大小写或空白导致重复路径。
type AdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[string]RefreshAdapter
}

// NewAdapterRegistry 创建一个空 registry。
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{adapters: make(map[string]RefreshAdapter)}
}

// Register 注册一个 provider adapter；重复注册或 nil adapter 都会拒绝。
func (r *AdapterRegistry) Register(name string, a RefreshAdapter) error {
	if r == nil {
		return errors.New("credentialworker: adapter registry is nil")
	}
	if a == nil {
		return errors.New("credentialworker: refresh adapter is nil")
	}
	key := normalizeProviderName(name)
	if key == "" {
		return errors.New("credentialworker: provider name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adapters == nil {
		r.adapters = make(map[string]RefreshAdapter)
	}
	if _, exists := r.adapters[key]; exists {
		return fmt.Errorf("credentialworker: refresh adapter already registered: provider=%s", key)
	}
	r.adapters[key] = a
	return nil
}

// Lookup 查找 provider adapter。
func (r *AdapterRegistry) Lookup(name string) (RefreshAdapter, bool) {
	if r == nil {
		return nil, false
	}
	key := normalizeProviderName(name)
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[key]
	return a, ok
}

// Names 返回当前注册的 provider 名称，主要用于测试和启动报告。
func (r *AdapterRegistry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.adapters))
	for name := range r.adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ErrMockOnly 表示该 vendor 在当前 Owner scope 中只允许 mock 路径。
var ErrMockOnly = errors.New("credentialworker: provider is mock-only")

// MockOnlyProviders 是本 lane 明确登记的 mock-only vendor。
var MockOnlyProviders = []string{
	"cursor",
	"copilot",
	"kiro",
	"windsurf",
	"bedrock",
	"perplexity",
}

// IsMockOnlyProvider 判断 provider 是否在 mock-only 白名单中。
func IsMockOnlyProvider(name string) bool {
	key := normalizeProviderName(name)
	for _, mock := range MockOnlyProviders {
		if key == mock {
			return true
		}
	}
	return false
}

// MockOnlyAdapter 明确表达“该 vendor 目前不做真账号刷新”，避免误报 missing adapter。
type MockOnlyAdapter struct{}

func (MockOnlyAdapter) RefreshForProvider(_ context.Context, accountID int64, providerName string, _ []byte) ([]byte, time.Time, error) {
	return nil, time.Time{}, fmt.Errorf("%w: provider=%s account_id=%d", ErrMockOnly, normalizeProviderName(providerName), accountID)
}

func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
