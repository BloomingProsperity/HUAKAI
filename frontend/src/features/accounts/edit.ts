import type { ProviderAccount } from './types'

/*
 * 账号编辑(池调优旋钮 + 出站/高级设置)纯逻辑(可单测)。PATCH /{id} 是部分更新:
 * 只下发【实际改动】的字段,未改字段省略(避免无谓覆盖)。对齐 routing 的 buildBindingUpdate 模式。
 * 后端契约字段:proxy_binding / probe_model / model_allow_list / capability_flags /
 * custom_error_codes(_enabled) / temp_unschedulable_enabled
 * (backend/internal/gatewayhttp/admin_pool_accounts_handler.go 的 updateProviderAccountRequest)。
 */

/** 出站代理绑定模式:直连 / 单代理 / 代理组(三者互斥,后端按 mode 构造性写两列)。 */
export type ProxyBindingMode = 'direct' | 'proxy' | 'group'

/** proxy_binding 请求体:mode 必填;proxy/group 各自带对应字段。 */
export interface ProxyBindingBody {
  mode: ProxyBindingMode
  proxy_id?: number
  proxy_group_id?: string
}

export interface AccountEditForm {
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
  /** 自定义错误码开关。 */
  customErrorCodesEnabled: boolean
  /** 逗号分隔的自定义错误码串(HTTP 状态码)。 */
  customErrorCodes: string
  /** 临时不可调度开关。 */
  tempUnschedulableEnabled: boolean
  /** 出站代理模式。 */
  proxyMode: ProxyBindingMode
  /** 选中的单代理 id(proxyMode=proxy 时用)。 */
  proxyId: string
  /** 代理组标识(proxyMode=group 时用)。 */
  proxyGroupId: string
  reason: string
}

export interface AccountUpdateBody {
  priority?: number
  static_weight?: number
  cap_concurrency?: number
  tags?: string[]
  probe_model?: string
  model_allow_list?: string[]
  capability_flags?: string[]
  custom_error_codes_enabled?: boolean
  custom_error_codes?: number[]
  temp_unschedulable_enabled?: boolean
  proxy_binding?: ProxyBindingBody
  reason?: string
}

/** 从账号现状推导当前出站代理模式(proxy_id 优先于 proxy_group_id)。 */
export function proxyModeFromAccount(a: ProviderAccount): ProxyBindingMode {
  if (a.proxy_id != null) return 'proxy'
  if (a.proxy_group_id) return 'group'
  return 'direct'
}

/** 把账号现状填充成编辑表单初值。缺省字段(后端可能回 null)一律降级为安全空值。 */
export function formFromAccount(a: ProviderAccount): AccountEditForm {
  return {
    priority: String(a.priority),
    staticWeight: String(a.static_weight),
    capConcurrency: String(a.cap_concurrency),
    tags: a.tags.join(', '),
    probeModel: a.probe_model ?? '',
    modelAllowList: (a.model_allow_list ?? []).join(', '),
    capabilityFlags: (a.capability_flags ?? []).join(', '),
    customErrorCodesEnabled: a.custom_error_codes_enabled ?? false,
    customErrorCodes: (a.custom_error_codes ?? []).join(', '),
    tempUnschedulableEnabled: a.temp_unschedulable_enabled ?? false,
    proxyMode: proxyModeFromAccount(a),
    proxyId: a.proxy_id != null ? String(a.proxy_id) : '',
    proxyGroupId: a.proxy_group_id ?? '',
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

/** 解析自定义错误码串:逗号/空白分隔,每项须为 100-599 的整数。非法返回 {error}。 */
export function parseErrorCodes(raw: string): number[] | { error: string } {
  const parts = raw
    .split(/[,，\s]+/)
    .map((t) => t.trim())
    .filter(Boolean)
  const out: number[] = []
  for (const p of parts) {
    const n = Number(p)
    if (!Number.isInteger(n) || n < 100 || n > 599) {
      return { error: `自定义错误码须为 100-599 的整数:${p}` }
    }
    out.push(n)
  }
  return out
}

export type BuildResult = AccountUpdateBody | { error: string } | { noop: true }

/**
 * 依当前表单推导 proxy_binding 改动:与账号现状对比,无变化返回 null,非法返回 {error},
 * 有变化返回构造好的 body。互斥语义交后端按 mode 落两列。
 */
function buildProxyBinding(
  original: ProviderAccount,
  form: AccountEditForm,
): ProxyBindingBody | { error: string } | null {
  const origMode = proxyModeFromAccount(original)
  const origProxyId = original.proxy_id ?? null
  const origGroup = original.proxy_group_id ?? ''

  if (form.proxyMode === 'direct') {
    return origMode === 'direct' ? null : { mode: 'direct' }
  }
  if (form.proxyMode === 'proxy') {
    const n = Number(form.proxyId.trim())
    if (!Number.isInteger(n) || n <= 0) return { error: '请选择一个出站代理' }
    if (origMode === 'proxy' && origProxyId === n) return null
    return { mode: 'proxy', proxy_id: n }
  }
  // group
  const g = form.proxyGroupId.trim()
  if (!g) return { error: '请填写代理组标识(proxy_group_id)' }
  if (origMode === 'group' && origGroup === g) return null
  return { mode: 'group', proxy_group_id: g }
}

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

  if (form.customErrorCodesEnabled !== (original.custom_error_codes_enabled ?? false)) {
    body.custom_error_codes_enabled = form.customErrorCodesEnabled
  }
  const codes = parseErrorCodes(form.customErrorCodes)
  if (!Array.isArray(codes)) return codes
  if (!listEqual(codes, original.custom_error_codes ?? [])) body.custom_error_codes = codes

  if (form.tempUnschedulableEnabled !== (original.temp_unschedulable_enabled ?? false)) {
    body.temp_unschedulable_enabled = form.tempUnschedulableEnabled
  }

  const proxy = buildProxyBinding(original, form)
  if (proxy && 'error' in proxy) return proxy
  if (proxy) body.proxy_binding = proxy

  if (Object.keys(body).length === 0) return { noop: true }
  const r = form.reason.trim()
  if (r) body.reason = r
  return body
}
