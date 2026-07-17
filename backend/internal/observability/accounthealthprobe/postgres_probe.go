// Package accounthealthprobe 把历史命名的 account_health_probe handler 接到数据库写入。
// 该 handler 由请求完成事件触发,记录的是被动请求观测,不执行主动上游探测。
//
// 当前写入使用独立 last_request_observed_at 存储列,不再占用主动探测字段。
// 该写入纯可观测,不碰钱 / auth / health_state。
// 写发生在异步 eventbus handler(Tier=MED、Critical=false、带超时、失败走 DLQ),
// 不在同步请求转发热路径上,因此不会拖慢请求。
package accounthealthprobe

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/observability"
)

// ObservationStore 是被动请求观测写入所需的最小存储面。生产实现是 *admin.Queries。
type ObservationStore interface {
	TouchProviderAccountRequestObservedAt(ctx context.Context, arg admin.TouchProviderAccountRequestObservedAtParams) error
}

// ErrNoStore 表示构造 probe 时未提供存储面;返回它而非 panic,便于上层降级处理。
var ErrNoStore = errors.New("accounthealthprobe: nil store")

// NewPostgresProbe 构造历史 handler 所需的回调;函数名仅为内部兼容命名。
func NewPostgresProbe(store ObservationStore) func(context.Context, observability.AccountHealthSignal) error {
	if store == nil {
		// store 缺失时返回可观测错误,避免静默空转。
		return func(context.Context, observability.AccountHealthSignal) error {
			return ErrNoStore
		}
	}
	return func(ctx context.Context, sig observability.AccountHealthSignal) error {
		// 无有效账号则无处可写,直接返回(非错误:某些请求可能在拿到账号前就完成/失败)。
		if sig.AccountID <= 0 || sig.TenantID <= 0 {
			return nil
		}
		return store.TouchProviderAccountRequestObservedAt(ctx, admin.TouchProviderAccountRequestObservedAtParams{
			ObservedAt: pgtype.Timestamptz{Time: sig.At.UTC(), Valid: true},
			ID:         sig.AccountID,
			TenantID:   sig.TenantID,
		})
	}
}
