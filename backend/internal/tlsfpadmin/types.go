// Package tlsfpadmin 为 TLS 指纹 profile 的管理端 CRUD 提供
// 校验 + 错误映射的 service 层。internal/db/admin 中的 sqlc/querier
// 层已实现 SQL;本包补充输入校验、带类型的 sentinel 错误,以及
// 通过预检存在性检查来检测 `:exec` 类 SetStatus/SoftDelete 查询
// (零行时返回 nil)的 not-found。本包不 import 任何 router/auth/gateway 包。
package tlsfpadmin

import (
	"errors"
	"time"
)

// Sentinel 错误。HTTP 层把它们映射为状态码;调用方用 errors.Is 判断。
// ErrBackend 包裹任何意料之外的 querier/DB 失败。
var (
	ErrInvalidInput  = errors.New("tlsfpadmin: invalid input")
	ErrInvalidStatus = errors.New("tlsfpadmin: invalid status")
	ErrNotFound      = errors.New("tlsfpadmin: profile not found")
	ErrDuplicateName = errors.New("tlsfpadmin: duplicate profile name")
	ErrBackend       = errors.New("tlsfpadmin: backend failure")
)

// adminSettableStatuses 是 platform_admin 可以通过 status 端点设置的状态值。
// "drift_detected" 被刻意排除——只有 drift 检测 worker 才会设置它,且是直接
// 通过 sqlc 层写入。把一个 drift_detected 的 profile 设为 "active" 是刻意保留的
// 管理员覆盖式「清除 drift」路径(SQL 会刷新 last_validated_at)。
var adminSettableStatuses = map[string]bool{"active": true, "disabled": true}

// Profile 是面向管理端的 DTO。drift 元数据(ExpectedJA3Hash、
// LastValidatedAt)以只读方式暴露;它由 drift worker 维护。
type Profile struct {
	ID                   int64      `json:"id"`
	TenantID             int64      `json:"tenant_id"`
	Name                 string     `json:"name"`
	Description          *string    `json:"description,omitempty"`
	GreaseEnabled        bool       `json:"grease_enabled"`
	CipherSuites         []int32    `json:"cipher_suites"`
	SupportedCurves      []int32    `json:"supported_curves"`
	EcPointFormats       []int32    `json:"ec_point_formats"`
	SignatureAlgorithms  []int32    `json:"signature_algorithms"`
	AlpnProtocols        []string   `json:"alpn_protocols"`
	TLSSupportedVersions []int32    `json:"tls_supported_versions"`
	KeyShareGroups       []int32    `json:"key_share_groups"`
	PskModes             []int32    `json:"psk_modes"`
	ExtensionsOrder      []int32    `json:"extensions_order"`
	ExpectedJA3Hash      string     `json:"expected_ja3_hash"`
	Status               string     `json:"status"`
	LastValidatedAt      *time.Time `json:"last_validated_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// CreateInput 保存创建 profile 所需的字段(status 在 DB 层默认为
// 'active';创建时不可设置)。
type CreateInput struct {
	TenantID             int64
	Name                 string
	Description          *string
	GreaseEnabled        bool
	CipherSuites         []int32
	SupportedCurves      []int32
	EcPointFormats       []int32
	SignatureAlgorithms  []int32
	AlpnProtocols        []string
	TLSSupportedVersions []int32
	KeyShareGroups       []int32
	PskModes             []int32
	ExtensionsOrder      []int32
	ExpectedJA3Hash      string
}

// UpdateInput 是一次全字段的内容更新。Status 被刻意省略——状态变更
// 只通过 SetStatus 进行。
type UpdateInput struct {
	TenantID             int64
	ID                   int64
	Name                 string
	Description          *string
	GreaseEnabled        bool
	CipherSuites         []int32
	SupportedCurves      []int32
	EcPointFormats       []int32
	SignatureAlgorithms  []int32
	AlpnProtocols        []string
	TLSSupportedVersions []int32
	KeyShareGroups       []int32
	PskModes             []int32
	ExtensionsOrder      []int32
	ExpectedJA3Hash      string
}

// SetStatusInput 保存一次状态转移请求。
type SetStatusInput struct {
	TenantID int64
	ID       int64
	Status   string
}
