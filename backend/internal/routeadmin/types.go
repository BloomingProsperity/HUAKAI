// HUAKAI · iKun

// Package routeadmin 提供 routes 表(订阅档→pool_group 路由规则, F-POOL-001 §5.2)的
// 管理员写侧 CRUD。与只读、跑在选号热路径上的 internal/subscriptionenforce 按职责分包:
// 那边只读 routes 做 GroupPolicyGate 强制, 这边负责管理员创建/列出/删除路由配置。
package routeadmin

import (
	"errors"
	"time"
)

var (
	// ErrStoreNotConfigured 表示 store 未注入(nil 安全防御)。
	ErrStoreNotConfigured = errors.New("routeadmin: store not configured")
	// ErrRouteNotFound 表示该租户下无此 route(或已软删)。
	ErrRouteNotFound = errors.New("routeadmin: route not found")
	// ErrInvalidInput 表示必填字段缺失或非法(name/user_group_match 空、tenant/pool_group <=0)。
	ErrInvalidInput = errors.New("routeadmin: invalid input")
	// ErrInvalidModelPattern 表示 model_pattern_match 含中段/多个通配; '*' 仅允许作整串或末尾后缀。
	ErrInvalidModelPattern = errors.New("routeadmin: invalid model pattern (wildcard '*' only allowed as whole pattern or trailing suffix)")
	// ErrDuplicateName 表示同租户下已存在同名 route(uq_routes_tenant_name)。
	ErrDuplicateName = errors.New("routeadmin: route name already exists for tenant")
	// ErrPoolGroupNotFound 表示目标 pool_group 不存在(FK 违反)。
	ErrPoolGroupNotFound = errors.New("routeadmin: target pool_group not found for tenant")
)

// Route 是 routes 表的管理视图（只含管理 CRUD 关心的核心字段）。富表的 26 个
// override/weight/streaming 历史列当前无运行时消费。
type Route struct {
	ID                int64
	TenantID          int64
	Name              string
	UserGroupMatch    string
	ModelPatternMatch string
	PoolGroupID       int64
	MatchPriority     int
	Enabled           bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CreateInput 是创建一条 route 的入参。MatchPriority 为 nil 时用 DB 默认(100)。
// AdminID 仅用于审计归属, 不写入 routes 表本身。
type CreateInput struct {
	TenantID          int64
	Name              string
	UserGroupMatch    string
	ModelPatternMatch string
	PoolGroupID       int64
	MatchPriority     *int
	AdminID           int64 // 兼容内部调用；HTTP 入口使用下列可追责身份字段。
	ActorID           string
	ActorRole         string
	RequestID         string
}

// UpdateInput 是全替换一条 route 可编辑字段的入参 (PUT 语义)。
// TenantID+ID 定位行且不可改 (防跨租户搬移); 改 Name/UserGroupMatch/ModelPatternMatch/
// PoolGroupID/MatchPriority; 保留 Enabled/CreatedAt, bump UpdatedAt。
// MatchPriority 为 nil 时回落 DB 默认(100) —— 与 Create 同的全替换语义, 非局部 patch。
// AdminID 仅用于审计归属, 不写入 routes 表本身。
type UpdateInput struct {
	TenantID          int64
	ID                int64
	Name              string
	UserGroupMatch    string
	ModelPatternMatch string
	PoolGroupID       int64
	MatchPriority     *int
	AdminID           int64 // 兼容内部调用；HTTP 入口使用下列可追责身份字段。
	ActorID           string
	ActorRole         string
	RequestID         string
}

// MutationLog 是路由启停和删除使用的操作归属。ActorID 必须来自认证身份，
// RequestID 只用于关联请求，不作为权限或幂等来源。
type MutationLog struct {
	ActorID       string
	ActorRole     string
	RequestID     string
	LegacyAdminID int64
}
