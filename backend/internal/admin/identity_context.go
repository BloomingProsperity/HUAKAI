package admin

import "context"

// identityCtxKey 是注入/读取已认证 admin 身份的私有 context key(空结构体避免跨包碰撞)。
type identityCtxKey struct{}

// IdentityToContext 把已认证的 admin 身份放进 context, 供下游 handler 读取 —— 取代"信任请求体里的
// 身份/归属字段(actor/admin_id 等)"这一可伪造模式。由认证 + RBAC 边界(如 adminGate)在放行后调用。
func IdentityToContext(ctx context.Context, id AdminIdentity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// IdentityFromContext 取回 IdentityToContext 注入的 admin 身份。ok=false 表示未注入(未经认证边界 /
// 测试未设置) —— 调用方此时【绝不可】回退去信任请求体的身份字段, 应置空或拒。
func IdentityFromContext(ctx context.Context) (AdminIdentity, bool) {
	id, ok := ctx.Value(identityCtxKey{}).(AdminIdentity)
	return id, ok
}
