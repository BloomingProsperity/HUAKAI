// Package accounthealthprobe 把 account_health_probe handler 的 probe 回调接到真实
// 数据库写上。此前 cmd/gateway/middleware.go 给该 handler 传的 probe 是 nil,handler
// 每次请求完成都被触发但直接空转(account_health_probe_handler.go: if probe==nil return),
// 导致 provider_accounts.last_probe_at 永远为 NULL、运维健康面板恒空。
//
// 这里提供一个最小、低侵入的 probe 实现:对 signal 携带的池账号盖一个"最近探测时间"戳。
// 纯可观测,不碰钱 / auth / health_state,不引入 schema 迁移(列在迁移 0110 早已存在)。
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

// ProbeStore 是 probe 写入所需的最小存储面。生产实现是 *admin.Queries(由 pgxpool 支撑);
// 抽成接口是为了让单测能注入 fake 验证 probe 被调用 + 参数正确,而无需真实 DB。
type ProbeStore interface {
	TouchProviderAccountProbe(ctx context.Context, arg admin.TouchProviderAccountProbeParams) error
}

// ErrNoStore 表示构造 probe 时未提供存储面;返回它而非 panic,便于上层降级处理。
var ErrNoStore = errors.New("accounthealthprobe: nil store")

// NewPostgresProbe 用一个 pgxpool 支撑的 admin.Queries 构造 probe 回调。
// 上层把返回值直接传给 observability.NewAccountHealthProbeHandler 的 probe 参数,
// 从而把"建了没用"的死开关点亮。
func NewPostgresProbe(store ProbeStore) func(context.Context, observability.AccountHealthSignal) error {
	if store == nil {
		// 防御:store 为 nil 时返回一个返回错误的 probe,而不是返回 nil。
		// 返回 nil 会让 handler 再次退化成空转(原 bug),返回报错的闭包能在
		// 接线缺失时立刻通过 DLQ / 日志暴露问题,而不是悄悄静默。
		return func(context.Context, observability.AccountHealthSignal) error {
			return ErrNoStore
		}
	}
	return func(ctx context.Context, sig observability.AccountHealthSignal) error {
		// 无有效账号则无处可写,直接返回(非错误:某些请求可能在拿到账号前就完成/失败)。
		if sig.AccountID <= 0 || sig.TenantID <= 0 {
			return nil
		}
		return store.TouchProviderAccountProbe(ctx, admin.TouchProviderAccountProbeParams{
			ProbedAt: pgtype.Timestamptz{Time: sig.At.UTC(), Valid: true},
			ID:       sig.AccountID,
			TenantID: sig.TenantID,
		})
	}
}
