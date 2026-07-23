// Package registry:错误类别(D4 + D6 映射)。
//
// HTTP 映射(在 chat handler 中处理):
//
//	ErrUnknownModel     -> 404 model_not_available
//	ErrModelDisabled    -> 404 model_not_available  (按 D4 反枚举,统一)
//	ErrTenantNoAccess   -> 404 model_not_available
//	ErrRegistryBackend  -> 503 registry_backend_error
//
// 之所以分成这四类，是为了让日志与结构化字段记录精确的内部原因，
// 同时保持对外响应稳定。

package registry

import "errors"

// ErrUnknownModel 在以下情形返回:(tenant_id, alias_normalized) 这一对
// 没有匹配的 alias 行,且该租户要么没有 inherit_global_catalog 策略,
// 要么全局查找也未命中。
var ErrUnknownModel = errors.New("registry: unknown model alias")

// ErrModelDisabled 在以下情形返回:alias 能解析,但 alias 行或规范模型行的
// status != 'active'。租户级被禁用的 alias 还会阻断全局兜底(D3 显式拒绝),
// 并以 ErrModelDisabled 形式暴露 —— 绝不会暴露成 ErrUnknownModel。
var ErrModelDisabled = errors.New("registry: model disabled")

// ErrTenantNoAccess 在以下情形返回:alias 能解析、模型也处于 active,
// 但经过 effective_from/until 过滤后没有任何启用的 pool binding 存活。
// 从运维视角看,该模型「已配置但未路由」。
var ErrTenantNoAccess = errors.New("registry: model has no eligible pool binding")

// ErrRegistryBackend 包装解析期间任何数据存储层故障。handler 把它映射成
// HTTP 503 —— 而非 404 —— 这样在基础设施故障期间,合法客户端不会被告知
// 其有效的 alias 不存在。与 auth.ErrAuthBackend 镜像对应。
var ErrRegistryBackend = errors.New("registry: backend datastore error")

// ErrInvalidModelCapability 由 admin 写入方在触及数据存储之前返回:当某个
// model-capability 绑定使用了 HUAKAI 已知模型能力词表之外的取值时。
var ErrInvalidModelCapability = errors.New("registry: invalid model capability")
