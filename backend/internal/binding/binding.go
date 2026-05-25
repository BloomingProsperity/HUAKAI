// Package binding 提供 user/api-key/account binding 的查询接口（U1-A atomic）。
//
// **当前阶段（U1-A）只做 interface + noop stub**——production routing 完全
// 不感知 binding。U1-B/U1-C 启用 binding-aware routing 之前必须先有 Owner 批
// 准的 schema migration（推荐 0013_*.up.sql 加 api_key_bindings 表）。
//
// 设计参考: backend/internal/registry/cache.go 的 Cache interface + noop stub
// 模式。
//
// 本包当前**无 routing/health 副作用**：
//   - production routing 当前不导入本包；仅 internal/pool selector 的未来 atomic
//     才会接入
//   - noop 实现总是返回 (zero, false, nil) — 路径不会因绑定不存在被阻塞
//   - 不持有任何持久化连接
package binding

import (
	"context"
	"errors"
)

// BindingKind 标记一条 binding 关系的目标类型。
//
// 当前预留三种：
//   - PoolGroup: 该 user/key 必须落在某 pool group（最常见，软绑定）
//   - ProviderAccount: 该 user/key 必须落在某具体 provider account（强绑定）
//   - TenantDefault: 该 user/key 走 tenant 全局默认（无显式绑定）
type BindingKind string

const (
	BindingKindPoolGroup       BindingKind = "pool_group"
	BindingKindProviderAccount BindingKind = "provider_account"
	BindingKindTenantDefault   BindingKind = "tenant_default"
	// BindingKindAPIKeyInherited 表示该 binding 通过 api_key.user_id 继承自
	// owner user 的绑定（U1-B schema 加 api_key_bindings 后，api_key 无显式
	// 行时回退到 user 维度）。U1-A 预留枚举值防 U1-B 引入破坏性 enum 变更。
	// （sonnet U1-A debug renew F2 finding）
	BindingKindAPIKeyInherited BindingKind = "api_key_inherited"
)

// BindingScope 是查询 BindingCache 的 key tuple。
//
// TenantID 必需; APIKeyID / UserID 至少一个非零；Model 用于 model-scoped
// 绑定（如 user X 只能用 model Y 的特定账号）。
type BindingScope struct {
	TenantID int64
	APIKeyID int64 // 0 = 不限定
	UserID   int64 // 0 = 不限定
	Model    string
}

// Valid 检查 scope 字段最小完整性 (sonnet U1-A debug renew F1 修复——
// 当前 noop 总 miss，对 degenerate scope 静默；U1-B 真实现下 degenerate
// scope 可能命中"全 tenant default"行造成 routing leak)。
//
// 规则:
//   - TenantID 必需 (>0)
//   - APIKeyID 与 UserID 必须至少一个非零 (key/user 维度查询锚点)
//
// caller 应在 Lookup 前先 Valid()；degenerate input fail-fast 比命中错绑定
// 安全。
func (s BindingScope) Valid() bool {
	if s.TenantID <= 0 {
		return false
	}
	if s.APIKeyID == 0 && s.UserID == 0 {
		return false
	}
	return true
}

// BindingSnapshot 是 BindingCache.Lookup 的返回 payload。
//
// noop 永远返回零值（hit=false 表示无绑定）。
type BindingSnapshot struct {
	BindingID         int64       // 绑定记录 PK（U1-B+ schema 后填）
	Kind              BindingKind // 见 BindingKind 注释
	PoolGroupID       int64       // BindingKindPoolGroup 时有效
	ProviderAccountID int64       // BindingKindProviderAccount 时有效
	Priority          int         // 同 user 多绑定时的偏好顺序
	Version           int64       // 增量缓存对账用
}

// BindingCache 是 binding 查询接口。
//
// 实现需保证 Lookup 是 hot-path safe：当前 noop 实现是 O(1) 无锁。
// 真实实现（U1-B 之后）应基于内存索引 + 增量 reload。
type BindingCache interface {
	// Lookup 查 (TenantID, APIKeyID/UserID, Model) → BindingSnapshot。
	// hit=false 表示未绑定（**不**是错误）。生产端应据此让请求继续走全局
	// routing；不要把 miss 翻译成 "fail-closed 拒绝请求"——除非 Policy 明确
	// 要求 strict-binding (U1-C 才决策)。
	Lookup(ctx context.Context, scope BindingScope) (BindingSnapshot, bool, error)
}

// ErrBindingCacheNotInitialized 表示 caller 试图用 nil cache 查询。
// 调用方应在启动期注入 noopCache 而不是接受 nil。
var ErrBindingCacheNotInitialized = errors.New("binding: cache 未初始化")
