package credentialworker

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrProviderAdapterMissing 表示凭据模式没有可执行的刷新适配器。
var ErrProviderAdapterMissing = errors.New("credentialworker: provider refresh adapter missing")

// RefreshAdapter 是 vendor OAuth 刷新的最小契约。
// 输入 currentCredential 必须是 provider_accounts.credentials 的原始 JSONB；
// 返回 newCredential 仍是可直接写回该字段的 JSON 字节。
type RefreshAdapter interface {
	RefreshForProvider(ctx context.Context, accountID int64, providerName string, currentCredential []byte) (newCredential []byte, expiresAt time.Time, err error)
}

func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
