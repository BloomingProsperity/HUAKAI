package proxyadmin

import (
	"errors"
	"time"
)

var (
	ErrInvalidInput       = errors.New("proxyadmin: invalid input")
	ErrInvalidStatus      = errors.New("proxyadmin: invalid status")
	ErrBackend            = errors.New("proxyadmin: backend failure")
	ErrNotFound           = errors.New("proxyadmin: proxy not found")
	ErrInUse              = errors.New("proxyadmin: proxy is in use")
	ErrTenantNotFound     = errors.New("proxyadmin: tenant not found")
	ErrStoreNotConfigured = errors.New("proxyadmin: tenant default store not configured")
	// ErrUnsafeHost 表示租户代理 host 指向了 loopback/内网/link-local/metadata
	// 等不安全目标(SSRF)。映射 HTTP 400,与 ErrInvalidInput 同档但语义更明确。
	ErrUnsafeHost = errors.New("proxyadmin: unsafe proxy host")
)

// Proxy 是代理行的"不含凭据"投影。它刻意省略 auth_secret:加密后的凭据是只写的,
// 任何读取路径(list/get)都绝不返回它,因此代理凭据无法经此面泄露。
type Proxy struct {
	ID           int64
	TenantID     int64
	Name         string
	Protocol     string
	Host         string
	Port         int32
	AuthUsername *string
	GroupID      *string
	Status       string
	LastCheckAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// DeleteImpact 是删除前的租户内引用投影。它不暴露账号、租户或代理凭据，只返回
// 前端确认弹窗和删除守卫所需的数量。
type DeleteImpact struct {
	ProxyID                   int64
	DirectAccountCount        int64
	DefaultTenantCount        int64
	GroupAccountCount         int64
	GroupRemainingActiveCount int64
}

func (i DeleteImpact) CanDelete() bool {
	if i.DirectAccountCount > 0 || i.DefaultTenantCount > 0 {
		return false
	}
	return i.GroupAccountCount == 0 || i.GroupRemainingActiveCount > 0
}

type InUseError struct {
	Impact DeleteImpact
}

func (e *InUseError) Error() string {
	return ErrInUse.Error()
}

func (e *InUseError) Unwrap() error {
	return ErrInUse
}

type CreateInput struct {
	TenantID     int64
	Name         string
	Protocol     string
	Host         string
	Port         int32
	AuthUsername *string
	AuthSecret   *string
	GroupID      *string
	Status       string
}

type UpdateInput struct {
	TenantID     int64
	ID           int64
	Name         string
	Protocol     string
	Host         string
	Port         int32
	AuthUsername *string
	AuthSecret   *string
	GroupID      *string
}

// PatchField 表示 PATCH 中一个字段是否出现。Value 可以是指针，以表达显式清空。
type PatchField[T any] struct {
	Set   bool
	Value T
}

// PatchInput 保留字段级更新语义；没有出现的字段必须由数据库原子保留。
type PatchInput struct {
	TenantID     int64
	ID           int64
	Name         PatchField[string]
	Protocol     PatchField[string]
	Host         PatchField[string]
	Port         PatchField[int32]
	AuthUsername PatchField[*string]
	AuthSecret   PatchField[*string]
	GroupID      PatchField[*string]
}

// TenantDefaultProxy 是租户默认出口的最小读取投影；nil 表示未设置、继续直连。
type TenantDefaultProxy struct {
	ProxyID *int64
}

// TenantDefaultAudit 只携带管理员归属与请求关联，不接收代理地址或凭据。
type TenantDefaultAudit struct {
	ActorID   string
	ActorRole string
	RequestID string
}

// MutationAudit 只接受认证层生成的操作者与请求关联信息。代理凭据、地址和请求体
// 不属于该结构，避免业务层误把秘密写进操作日志。
type MutationAudit struct {
	ActorID   string
	ActorRole string
	RequestID string
	Reason    *string
}
