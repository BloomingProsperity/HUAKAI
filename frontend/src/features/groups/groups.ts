import type { CreatePoolRequest, UpdatePoolRequest } from './types'

/*
 * 分组管理(池组)纯逻辑(可单测)。表单构造 / 校验 / 枚举与展示标签。
 * 全部以后端真实约束为准(见各处中文注释引用的 handler 校验点)。
 */

/**
 * capability_default 取值 —— 后端 validateAdminPoolCapabilityDefault 只允许这两枚:
 *   exact_capability_only / safe_equivalent_allowed。其它一律 400 invalid_capability_default。
 * 故下拉只暴露这两项,选别的必失败。
 */
export const CAPABILITY_DEFAULTS: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'exact_capability_only', label: '仅精确能力' },
  { value: 'safe_equivalent_allowed', label: '允许安全等价' },
]

export function capabilityLabel(value: string): string {
  return CAPABILITY_DEFAULTS.find((c) => c.value === value)?.label ?? value
}

/** 池组名长度上限 —— 后端 maxAdminPoolNameRunes=64(按 rune 计)。 */
export const MAX_POOL_NAME_RUNES = 64

/** top_k_default 取值范围 —— 后端 validateAdminPoolTopK:1..10。 */
export const TOP_K_MIN = 1
export const TOP_K_MAX = 10

export interface PoolForm {
  name: string
  topKDefault: number
  capabilityDefault: string
  allowLastResort: boolean
}

export const EMPTY_POOL_FORM: PoolForm = {
  name: '',
  topKDefault: 1, // 后端 defaultAdminPoolTopKDefault=1
  capabilityDefault: 'exact_capability_only', // 后端 defaultAdminPoolCapabilityDefault
  allowLastResort: false,
}

/** 用 rune(码点)而非 UTF-16 code unit 计数,与后端 utf8.RuneCountInString 对齐(中文名也算 1)。 */
export function runeCount(s: string): number {
  return [...s].length
}

/**
 * 校验池组名 —— 与后端 validateAdminPoolName 对齐:trim 后非空、rune 数 ≤ 64。
 * 客户端先挡,给清晰中文提示,避免无谓往返。
 */
export function validatePoolName(rawName: string): string | null {
  const name = rawName.trim()
  if (!name) return '请填写分组名称'
  if (runeCount(name) > MAX_POOL_NAME_RUNES) return `名称不能超过 ${MAX_POOL_NAME_RUNES} 个字符`
  return null
}

/**
 * 构造新建请求 —— 校验通过返回 CreatePoolRequest,否则 {error}。
 * 仅发送后端会落库的字段(name/top_k_default/capability_default/allow_last_resort);
 * tenant_id 由调用方(platform_admin 需显式选租户)按需补在 query 上,不进 body 默认值。
 */
export function buildCreatePool(form: PoolForm): CreatePoolRequest | { error: string } {
  const nameErr = validatePoolName(form.name)
  if (nameErr) return { error: nameErr }
  if (form.topKDefault < TOP_K_MIN || form.topKDefault > TOP_K_MAX) {
    return { error: `top_k_default 取值需在 ${TOP_K_MIN}..${TOP_K_MAX}` }
  }
  if (!CAPABILITY_DEFAULTS.some((c) => c.value === form.capabilityDefault)) {
    return { error: '能力默认值非法' }
  }
  return {
    name: form.name.trim(),
    top_k_default: form.topKDefault,
    capability_default: form.capabilityDefault,
    allow_last_resort: form.allowLastResort,
  }
}

/**
 * 构造编辑请求 —— 仅包含相对 original 真正变化的字段(PATCH 语义:present 才改)。
 * 全无变化时返回 {error},避免后端 admin_bad_request(至少一个字段)。
 */
export function buildUpdatePool(
  form: PoolForm,
  original: PoolForm,
): UpdatePoolRequest | { error: string } {
  const nameErr = validatePoolName(form.name)
  if (nameErr) return { error: nameErr }
  if (form.topKDefault < TOP_K_MIN || form.topKDefault > TOP_K_MAX) {
    return { error: `top_k_default 取值需在 ${TOP_K_MIN}..${TOP_K_MAX}` }
  }
  if (!CAPABILITY_DEFAULTS.some((c) => c.value === form.capabilityDefault)) {
    return { error: '能力默认值非法' }
  }
  const patch: UpdatePoolRequest = {}
  if (form.name.trim() !== original.name.trim()) patch.name = form.name.trim()
  if (form.topKDefault !== original.topKDefault) patch.top_k_default = form.topKDefault
  if (form.capabilityDefault !== original.capabilityDefault) patch.capability_default = form.capabilityDefault
  if (form.allowLastResort !== original.allowLastResort) patch.allow_last_resort = form.allowLastResort
  if (Object.keys(patch).length === 0) return { error: '没有需要保存的改动' }
  return patch
}

/**
 * 启停目标 —— enabled=true → 翻成禁用(false);否则启用(true)。
 * 后端无 DELETE 端点,"删除/下线"池组以 PATCH enabled=false 表达(软停用,保留路由历史)。
 */
export function toggleEnabledTarget(enabled: boolean): boolean {
  return !enabled
}
