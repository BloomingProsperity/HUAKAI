package credentialworker

import (
	"context"
	"errors"
	"fmt"
)

// Refresher 是 credential worker 依赖的最小刷新接口。
type Refresher interface {
	Refresh(ctx context.Context, accountID int64) error
}

// NoopRefresher 只用于测试或显式 dry-run；生产不应把它当真实刷新实现。
type NoopRefresher struct{}

func (NoopRefresher) Refresh(context.Context, int64) error { return nil }

// ProviderAwareRefresher 可利用 scheduler 已扫出的 provider_id 做分发。
type ProviderAwareRefresher interface {
	RefreshForProvider(ctx context.Context, providerID, accountID int64) error
}

// ErrProviderAdapterMissing 表示对应 provider 的刷新 adapter 尚未接入。
var ErrProviderAdapterMissing = errors.New("credentialworker: provider refresh adapter missing")

// ProviderDispatchRefresher 是真实刷新入口的占位壳：本 lane 只定义分发边界，
// provider-specific adapter 由后续子模块注册。
type ProviderDispatchRefresher struct {
	Adapters map[int64]Refresher
}

func (r ProviderDispatchRefresher) Refresh(context.Context, int64) error {
	return ErrProviderAdapterMissing
}

func (r ProviderDispatchRefresher) RefreshForProvider(ctx context.Context, providerID, accountID int64) error {
	adapter, ok := r.Adapters[providerID]
	if !ok || adapter == nil {
		return fmt.Errorf("%w: provider_id=%d", ErrProviderAdapterMissing, providerID)
	}
	return adapter.Refresh(ctx, accountID)
}
