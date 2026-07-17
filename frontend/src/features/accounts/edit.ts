import type { ProviderAccount } from './types'
import type { Proxy } from '../proxies/types'
import {
  advancedFormFromAccount,
  buildAdvancedUpdate,
  type AccountAdvancedFormState,
  type AccountAdvancedPayload,
} from './advancedFields'

export {
  buildTempUnschedulableRules,
  parseErrorCodes,
  proxyModeFromAccount,
  rulesToForm,
} from './advancedFields'
export type {
  PoolModeChoice,
  ProxyBindingMode,
  TempRulesMode,
  TempUnschedulableRuleForm,
} from './advancedFields'

/*
 * 账号编辑(池调优旋钮 + 出站/高级设置)纯逻辑(可单测)。PATCH /{id} 是部分更新:
 * 只下发【实际改动】的字段,未改字段省略(避免无谓覆盖)。对齐 routing 的 buildBindingUpdate 模式。
 * 后端契约字段:proxy_binding / probe_model / model_allow_list / capability_flags /
 * custom_error_codes(_enabled) / pool_mode / temp_unschedulable_enabled /
 * temp_unschedulable_rules / extra
 * (backend/internal/gatewayhttp/admin_pool_accounts_handler.go 的 updateProviderAccountRequest)。
 */

export interface ProxyGroupSummary {
  groupId: string
  total: number
  active: number
}

/** 按组汇总代理总成员与 active 成员；未分组代理不进入候选。 */
export function summarizeProxyGroups(proxies: Proxy[]): ProxyGroupSummary[] {
  const groups = new Map<string, ProxyGroupSummary>()
  for (const proxy of proxies) {
    const groupId = proxy.group_id?.trim()
    if (!groupId) continue
    const summary = groups.get(groupId) ?? { groupId, total: 0, active: 0 }
    summary.total += 1
    if (proxy.status === 'active') summary.active += 1
    groups.set(groupId, summary)
  }
  return [...groups.values()].sort((a, b) => a.groupId.localeCompare(b.groupId))
}

/** 返回当前输入组的汇总；未知组按零成员处理，供同一计数与预警逻辑消费。 */
export function selectedProxyGroupSummary(
  groups: ProxyGroupSummary[],
  rawGroupId: string,
): ProxyGroupSummary {
  const groupId = rawGroupId.trim()
  return groups.find((group) => group.groupId === groupId) ?? { groupId, total: 0, active: 0 }
}

/** 零 active 成员必须显式提示请求会 fail-closed；有成员时不显示危险文案。 */
export function proxyGroupBindingWarning(summary: ProxyGroupSummary): string | null {
  if (summary.active > 0) return null
  return '此代理组当前没有 active 成员，绑定后请求将 fail-closed，不会直连。'
}

export interface AccountEditForm extends AccountAdvancedFormState {
  priority: string
  staticWeight: string
  capConcurrency: string
  /** 逗号分隔的标签串。 */
  tags: string
  /** 探测模型(留空=清空)。 */
  probeModel: string
  /** 逗号分隔的模型白名单串(空=不限)。 */
  modelAllowList: string
  /** 逗号分隔的能力标记串。 */
  capabilityFlags: string
  /** provider 专属自由扩展 JSON 对象。 */
  extraJson: string
  reason: string
}

export interface AccountUpdateBody extends AccountAdvancedPayload {
  priority?: number
  static_weight?: number
  cap_concurrency?: number
  tags?: string[]
  probe_model?: string
  model_allow_list?: string[]
  capability_flags?: string[]
  extra?: Record<string, unknown>
  reason?: string
}

/** 把账号现状填充成编辑表单初值。缺省字段(后端可能回 null)一律降级为安全空值。 */
export function formFromAccount(a: ProviderAccount): AccountEditForm {
  return {
    ...advancedFormFromAccount(a),
    priority: String(a.priority),
    staticWeight: String(a.static_weight),
    capConcurrency: String(a.cap_concurrency),
    tags: a.tags.join(', '),
    probeModel: a.probe_model ?? '',
    modelAllowList: (a.model_allow_list ?? []).join(', '),
    capabilityFlags: (a.capability_flags ?? []).join(', '),
    extraJson: JSON.stringify(a.extra ?? {}, null, 2),
    reason: '',
  }
}

/** 逗号分隔串 → 去空去首尾空白的字符串数组(标签/白名单/能力标记通用)。 */
export function parseTags(raw: string): string[] {
  return raw
    .split(',')
    .map((t) => t.trim())
    .filter((t) => t.length > 0)
}

function listEqual<T>(a: T[], b: T[]): boolean {
  if (a.length !== b.length) return false
  return a.every((v, i) => v === b[i])
}

/** 校验 extra 的 JSON 语法及对象形状;数组/null 不是后端接受的扩展对象。 */
export function parseExtraJson(raw: string): Record<string, unknown> | { error: string } {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return { error: '扩展 JSON 格式无效' }
  }
  if (parsed === null || Array.isArray(parsed) || typeof parsed !== 'object') {
    return { error: '扩展 JSON 必须是 JSON 对象' }
  }
  return parsed as Record<string, unknown>
}

function canonicalJson(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(',')}]`
  if (value !== null && typeof value === 'object') {
    const obj = value as Record<string, unknown>
    return `{${Object.keys(obj)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${canonicalJson(obj[key])}`)
      .join(',')}}`
  }
  return JSON.stringify(value)
}

export type BuildResult = AccountUpdateBody | { error: string } | { noop: true }

/**
 * 构造 PATCH 体:逐字段与原值比较,只收改动项。数字字段非法(NaN/负)报错;
 * 全无改动返回 noop(不发空 PATCH)。reason 仅在有实际改动时附带。
 */
export function buildAccountUpdate(original: ProviderAccount, form: AccountEditForm): BuildResult {
  const body: AccountUpdateBody = {}

  const numField = (raw: string, orig: number, key: 'priority' | 'static_weight' | 'cap_concurrency', label: string): string | null => {
    const n = Number(raw.trim())
    if (!Number.isInteger(n) || n < 0) return `${label}必须是非负整数`
    if (n !== orig) body[key] = n
    return null
  }

  const e1 = numField(form.priority, original.priority, 'priority', '优先级')
  if (e1) return { error: e1 }
  const e2 = numField(form.staticWeight, original.static_weight, 'static_weight', '静态权重')
  if (e2) return { error: e2 }
  const e3 = numField(form.capConcurrency, original.cap_concurrency, 'cap_concurrency', '并发上限')
  if (e3) return { error: e3 }

  const nextTags = parseTags(form.tags)
  if (!listEqual(nextTags, original.tags)) body.tags = nextTags

  // 探测模型:留空表示清空,后端 cleanOptionalString 处理。
  const nextProbe = form.probeModel.trim()
  if (nextProbe !== (original.probe_model ?? '')) body.probe_model = nextProbe

  const nextAllow = parseTags(form.modelAllowList)
  if (!listEqual(nextAllow, original.model_allow_list ?? [])) body.model_allow_list = nextAllow

  const nextFlags = parseTags(form.capabilityFlags)
  if (!listEqual(nextFlags, original.capability_flags ?? [])) body.capability_flags = nextFlags

  const extra = parseExtraJson(form.extraJson)
  if ('error' in extra) return extra
  if (canonicalJson(extra) !== canonicalJson(original.extra ?? {})) body.extra = extra

  const advanced = buildAdvancedUpdate(original, form)
  if ('error' in advanced) return advanced
  Object.assign(body, advanced)

  if (Object.keys(body).length === 0) return { noop: true }
  const r = form.reason.trim()
  if (r) body.reason = r
  return body
}
