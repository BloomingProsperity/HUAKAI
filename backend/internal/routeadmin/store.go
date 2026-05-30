// HUAKAI · iKun

package routeadmin

import "context"

// Store 是 routes 表的写侧持久化抽象(裸 pgx 实现见 store_postgres.go, 测试内存实现见 store_memory.go)。
type Store interface {
	Create(ctx context.Context, in CreateInput) (Route, error)
	// List 返回该租户全部未软删的 route(按 match_priority 升序, 同序按 id)。
	List(ctx context.Context, tenantID int64) ([]Route, error)
	Get(ctx context.Context, tenantID, id int64) (Route, error)
	// SoftDelete 把 route 标记软删(deleted_at=now)并返回删前快照; 不存在/已删返 ErrRouteNotFound。
	SoftDelete(ctx context.Context, tenantID, id int64) (Route, error)
}

// AuditSink 接收管理操作审计(可为 nil — 不注入则不记)。adminID 是发起操作的管理员令牌身份,
// 用于审计归属, 不写入 routes 表本身。
type AuditSink interface {
	RouteCreated(ctx context.Context, r Route, adminID int64)
	RouteDeleted(ctx context.Context, r Route, adminID int64)
}
