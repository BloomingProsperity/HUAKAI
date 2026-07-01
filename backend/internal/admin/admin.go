// Package admin 实现 HUAKAI 面向运维的能力面:
//   - admin token 认证(独立于客户的 api_keys)
//   - api_keys 的签发 / 吊销 / 列举(取代手工 SQL INSERT 到 api_keys 的工作流)
//   - admin_audit_events 写入
//
// 边界契约(docs/specs/_invariants/cross-module-boundaries.md):
// 本包【绝不可】被 internal/router 或 internal/auth 的热路径 resolver 引入。
//     入站 resolver 查 api_keys;admin 工作写 api_keys。两个不同的能力面。
// 明文 bearer 仅在 IssueResult.Plaintext 中暴露,
//     用于一次性回给运维。【绝不】存储、【绝不】记日志、
//     【绝不】持久化到 admin_audit_events.payload。
// 本包写入 admin_tokens、api_keys 和 admin_audit_events,
//     绝不写 billing、pool 或 registry 表。
//
// 依据 docs/process/plans/2026-05-01-n4b-admin-keys.md。

package admin

import "errors"

// ErrAdminUnauthorized 在调用方的 admin 凭证缺失、格式错误、已过期、已吊销,
// 或不具备执行所请求操作所需角色时返回。handler 将其映射为 401。
var ErrAdminUnauthorized = errors.New("admin: unauthorized")

// ErrAdminForbidden 在调用方通过认证但 scope 检查拒绝该操作时返回 ——
// 例如 tenant_operator 试图为其他 tenant 签发。handler 将其映射为 403。
var ErrAdminForbidden = errors.New("admin: forbidden")

// ErrAdminRateLimited 在调用方超出 per-actor 签发速率窗口时返回
//(D4 默认:30 次签发 / 小时)。handler 将其映射为 429。
var ErrAdminRateLimited = errors.New("admin: rate limited")

// ErrAdminBadRequest 覆盖那些数据库本就会拒绝的结构性非法输入
//(例如缺少必填字段)。400。
var ErrAdminBadRequest = errors.New("admin: bad request")

// ErrAdminNotFound 在目标资源(api_keys 行、admin_tokens 行)不存在
// 或已被软删除时返回。404。
var ErrAdminNotFound = errors.New("admin: target not found")

// ErrAdminBackend 包装 admin 工作期间发生的任何数据存储故障。
// handler 将其映射为 503 —— 而非 401 —— 这样在基础设施故障期间,
// 合法运维不会被告知其有效凭证无效。
// 与 auth.ErrAuthBackend 对应。
var ErrAdminBackend = errors.New("admin: backend datastore error")

// step-up 二次校验错误(role 制单登录 P3):session 通道对被标注 SessionStepUp
// 的写端点要求新鲜的密码/2FA 证明。与凭证失败(ErrAdminUnauthorized/401)刻意区分,
// 因为 step-up 是「可操作信号」——客户端应据此弹出二次校验、而非当作凭证失效。
// token 通道豁免(programmatic 持有即授权),故这些错误只在 session 源产生。
//
// ⚠️ 映射契约由消费 handler 履行,且【必须】随「放开真 SessionStepUp 路由」的切片一并落地:
// 承载该路由的包须在其 writeAdminAuthError 里补下列映射 + 一条 handler 端到端测试。
// 本机制切片不放开任何真路由,故当前无 handler 产出这些错误(SessionStepUp 分支生产不可达);
// 现有 writeAdminAuthError 副本的 default→401 对它们是 fail-closed 兜底(更严、绝不误授权)。
//
// ErrAdminStepUpRequired:未带 step-up 证明。映射 403(带专用 code 供前端弹二次校验)。
var ErrAdminStepUpRequired = errors.New("admin: step-up required")

// ErrAdminStepUpInvalid:证明错误(密码/2FA 不符)。映射 401。
var ErrAdminStepUpInvalid = errors.New("admin: step-up invalid")

// ErrAdminStepUpLocked:底层 2FA 多次失败临时锁定。映射 429(建议带 Retry-After)。
var ErrAdminStepUpLocked = errors.New("admin: step-up locked")

// Role 枚举,与 admin_tokens.role 的 CHECK 约束对应。
const (
	RolePlatformAdmin  = "platform_admin"
	RoleTenantOperator = "tenant_operator"
)
