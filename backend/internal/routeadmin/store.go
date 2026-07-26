// HUAKAI · iKun

package routeadmin

import "context"

// Store 是 routes 表的写侧持久化抽象(裸 pgx 实现见 store_postgres.go, 测试内存实现见 store_memory.go)。
type Store interface {
	Create(ctx context.Context, in CreateInput) (Route, error)
	// List 返回该租户全部未软删的 route(按 match_priority 升序, 同序按 id)。
	List(ctx context.Context, tenantID int64) ([]Route, error)
	Get(ctx context.Context, tenantID, id int64) (Route, error)
	// Update 全替换一条未软删 route 的可编辑字段并返回更新后快照。
	// 行不存在/已软删/非本租户 → ErrRouteNotFound; 目标 pool_group 非同租户/已删 → ErrPoolGroupNotFound;
	// 改名撞同租户另一活路由名 → ErrDuplicateName(排除自身); 中段通配在 Service 层已先拒。
	Update(ctx context.Context, in UpdateInput) (Route, error)
	// SetEnabled 翻转一条未软删 route 的 enabled 闸并返回更新后快照(独立窄动作, 不碰其它列)。
	// 停用 → 热路径仲裁排除该路由(subscriptionenforce: AND enabled=true); 启用 → 重新参与。
	// 行不存在/已软删/非本租户 → ErrRouteNotFound; 幂等: 设成当前值仍命中该行返回快照(不报错)。
	SetEnabled(ctx context.Context, tenantID, id int64, enabled bool) (Route, error)
	// SoftDelete 把 route 标记软删(deleted_at=now)并返回删前快照; 不存在/已删返 ErrRouteNotFound。
	SoftDelete(ctx context.Context, tenantID, id int64) (Route, error)
}

// AtomicStore 把路由变更和分类操作日志提交在同一事务。生产 PostgreSQL store 必须实现；
// Service 仅为内存测试 store 保留旧的 AuditSink 兼容路径。
type AtomicStore interface {
	CreateWithLog(context.Context, CreateInput, MutationLog) (Route, error)
	UpdateWithLog(context.Context, UpdateInput, MutationLog) (Route, error)
	SetEnabledWithLog(context.Context, int64, int64, bool, MutationLog) (Route, error)
	SoftDeleteWithLog(context.Context, int64, int64, MutationLog) (Route, error)
}

// AuditSink 接收管理操作审计(可为 nil — 不注入则不记)。adminID 是发起操作的管理员令牌身份,
// 用于审计归属, 不写入 routes 表本身。
type AuditSink interface {
	RouteCreated(ctx context.Context, r Route, adminID int64)
	RouteUpdated(ctx context.Context, r Route, adminID int64)
	RouteDeleted(ctx context.Context, r Route, adminID int64)
}
