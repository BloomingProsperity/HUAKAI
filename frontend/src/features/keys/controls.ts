/*
 * API Key 细粒度控制 · 纯逻辑(可单测,无 DOM/网络副作用)。
 *
 * 镜像后端 userkeycontrolshttp + userkeycontrols 服务的真实契约:
 *   - 配额(quota):limit_usd 非负十进制字符串;"0"=无限额(后端 row.LimitUSD.IsZero 时不算 remaining,
 *     见 key_control_service.go:150);metric ∈ {cost-usd, request-count}(quota_group_handlers.go:267)。
 *   - 分组(group):group_id 为正 int64 或 null(null=清除绑定),见 quota_group_handlers.go:109。
 *   - IP 白/黑名单:每条为单 IP 或 CIDR(apikeyipallow.normalizeEntry / apikeyipdeny 同构);
 *     空数组=清空(白名单清空=放行全部)。非法条目后端 400(invalid_ip_allowlist/blacklist)。
 *   - 模型白名单:model id 字符串数组;空=放行全部模型。后端 Normalize 会 trim+小写+去重。
 *
 * 这些函数只做「前端先拦 + 表单<->请求体互转」,后端仍是权威。全部同步纯函数,便于 §14 变异测试打红。
 */

import type {
  KeyGroupView,
  KeyIPListView,
  KeyModelAllowlistView,
  KeyQuotaView,
  SetGroupBody,
  SetIPAllowlistBody,
  SetIPBlacklistBody,
  SetModelAllowlistBody,
  SetQuotaBody,
} from './controlsTypes'

// ── 配额 ──────────────────────────────────────────────────────────────────────

/** 配额度量,镜像后端 parseQuotaMetric 接受的值(quota_group_handlers.go:267)。 */
export type QuotaMetric = 'cost-usd' | 'request-count'

export interface QuotaForm {
  /** 上限输入(十进制字符串);空串视为未设置/无限额。 */
  limitUsd: string
  metric: QuotaMetric
  /**
   * 窗口/模式为只读 round-trip 字段:从 GET 原样取回、PUT 时原样回传。
   * 后端 PUT 是整行 upsert,且 normalizeQuotaRequest 把空 window_kind/mode 默认成
   * calendar_day + enforce(key_control_service.go:354/358);若不回传,保存限额会把
   * 原本 calendar_month/observe 等策略静默重置(money/quota-enforcement 回退)。故必须随行携带。
   */
  windowKind: string
  windowSeconds: number
  mode: string
}

/** 把 GET 回的 KeyQuotaView 拍平为表单初值;limit_usd 经 trimDecimal 去尾随 0;窗口/模式原样留存供回传。 */
export function quotaToForm(view: KeyQuotaView): QuotaForm {
  const metric: QuotaMetric = view.metric === 'requests' ? 'request-count' : 'cost-usd'
  return {
    limitUsd: trimDecimal(view.limit_usd ?? ''),
    metric,
    windowKind: view.window_kind ?? '',
    windowSeconds: view.window_seconds ?? 0,
    mode: view.mode ?? '',
  }
}

/** 无配额(GET 404)时的默认表单;窗口/模式留空 → 后端首次设置时默认 calendar_day+enforce。 */
export function emptyQuotaForm(): QuotaForm {
  return { limitUsd: '', metric: 'cost-usd', windowKind: '', windowSeconds: 0, mode: '' }
}

export type QuotaValidation = { ok: true; value: SetQuotaBody } | { ok: false; error: string }

/** 把 round-trip 的窗口/模式附加到 PUT 体(仅在非空时携带,避免覆盖成后端默认)。 */
function withWindowMode(form: QuotaForm, body: SetQuotaBody): SetQuotaBody {
  const out: SetQuotaBody = { ...body }
  if (form.windowKind) out.window_kind = form.windowKind
  if (form.windowSeconds > 0) out.window_seconds = form.windowSeconds
  if (form.mode) out.mode = form.mode
  return out
}

/**
 * 校验配额表单并构造 PUT 体。
 * 判别核心:limit_usd 必须是非负十进制(镜像后端 parseLimitUSD:空串或负数即 400)。
 * 空串归一为 "0"(= 无限额),让用户能「清除上限」。metric 直传(后端只认两值)。
 * 窗口/模式从 GET round-trip 回传(见 QuotaForm 注释),避免保存限额时静默重置策略。
 */
export function validateQuota(form: QuotaForm): QuotaValidation {
  const raw = form.limitUsd.trim()
  // 空串=无限额,显式化为 "0"。
  if (raw === '') {
    return { ok: true, value: withWindowMode(form, { limit_usd: '0', metric: form.metric }) }
  }
  // 非负十进制:允许小数,禁负号/非数字(与 moderation.validateConfig 的 fee 校验同构)。
  if (!/^[0-9]+(\.[0-9]+)?$/.test(raw)) {
    return { ok: false, error: '用量上限必须是非负的十进制数(如 0 或 25.00),0 表示不限' }
  }
  return { ok: true, value: withWindowMode(form, { limit_usd: raw, metric: form.metric }) }
}

/** 窗口类型 → 中文标签(用于只读展示当前窗口口径)。 */
export function windowKindLabel(kind: string): string {
  switch (kind) {
    case 'calendar_day':
      return '每日'
    case 'calendar_week':
      return '每周'
    case 'calendar_month':
      return '每月'
    case 'fixed':
      return '固定窗口'
    case 'none':
    case '':
      return '不限窗口'
    default:
      return kind
  }
}

/** 模式 → 中文标签(enforce=阻断、observe=仅观测等)。 */
export function modeLabel(mode: string): string {
  switch (mode) {
    case 'enforce':
      return '阻断(超限拒绝)'
    case 'observe':
      return '仅观测(不阻断)'
    case 'manual_first':
      return '人工优先'
    case 'disabled':
      return '已禁用'
    case '':
      return '默认'
    default:
      return mode
  }
}

/** 度量 → 中文标签。 */
export function metricLabel(metric: QuotaMetric): string {
  return metric === 'request-count' ? '请求次数' : '消费金额(USD)'
}

// ── 分组 ──────────────────────────────────────────────────────────────────────

export interface GroupForm {
  /** 分组 ID 输入框(空串=不绑定/清除);仅接受正整数串。 */
  groupId: string
}

export function groupToForm(view: KeyGroupView): GroupForm {
  return { groupId: view.group_id != null ? String(view.group_id) : '' }
}

export function emptyGroupForm(): GroupForm {
  return { groupId: '' }
}

export type GroupValidation = { ok: true; value: SetGroupBody } | { ok: false; error: string }

/**
 * 校验分组绑定并构造 PUT 体。
 * 判别核心:group_id 空串 → null(清除绑定);非空必须是正整数串(镜像后端 *GroupID <= 0 即 400)。
 */
export function validateGroup(form: GroupForm): GroupValidation {
  const raw = form.groupId.trim()
  if (raw === '') {
    // 清除绑定:下发 null。
    return { ok: true, value: { group_id: null } }
  }
  if (!/^[1-9][0-9]*$/.test(raw)) {
    return { ok: false, error: '分组 ID 必须是正整数,留空表示不绑定分组' }
  }
  return { ok: true, value: { group_id: Number(raw) } }
}

/** 分组展示文案:有名字显示名字,否则显示 ID,未绑定显示「未绑定」。 */
export function groupDisplay(view: KeyGroupView | null): string {
  if (!view || view.group_id == null) return '未绑定'
  if (view.group_name && view.group_name.trim() !== '') return view.group_name
  return `#${view.group_id}`
}

// ── IP 白/黑名单 + 模型白名单(多行文本 <-> 字符串数组)─────────────────────────

/**
 * 把多行文本拆成去重后的非空条目(trim 每行、丢空行、保序去重)。
 * 用于 IP 白/黑名单与模型白名单的输入框。
 */
export function parseList(text: string): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  for (const line of text.split(/\r?\n/)) {
    const v = line.trim()
    if (v === '') continue
    if (seen.has(v)) continue
    seen.add(v)
    out.push(v)
  }
  return out
}

/** 把字符串数组渲染回多行文本(用于回填输入框)。 */
export function listToText(entries: string[] | null | undefined): string {
  return (entries ?? []).join('\n')
}

/**
 * 单条 IP/CIDR 的前端预校验(镜像 apikeyipallow.normalizeEntry:先试 ParsePrefix 再试 ParseAddr)。
 * 判别核心:必须能解析成 IPv4/IPv6 地址或带掩码的 CIDR;否则返回该条文案。返回首个非法条目或 null。
 * 注:后端是权威(支持 IPv6 缩写等),这里只拦明显非法,避免无谓 400。
 */
export function firstInvalidIP(entries: string[]): string | null {
  for (const e of entries) {
    if (!isPlausibleIPorCIDR(e)) return e
  }
  return null
}

/** 构造 IP 白名单 PUT 体(空数组=清空=放行全部)。 */
export function buildIPAllowlist(entries: string[]): SetIPAllowlistBody {
  return { ip_allowlist: entries }
}

/** 构造 IP 黑名单 PUT 体(空数组=清空)。 */
export function buildIPBlacklist(entries: string[]): SetIPBlacklistBody {
  return { ip_blacklist: entries }
}

/** 构造模型白名单 PUT 体(空数组=放行全部模型)。 */
export function buildModelAllowlist(entries: string[]): SetModelAllowlistBody {
  return { allowed_models: entries }
}

/** 从 GET 视图取 IP 列表(白/黑名单视图同构,字段名不同)。 */
export function ipAllowlistFromView(view: KeyIPListView): string[] {
  return view.ip_allowlist ?? []
}
export function ipBlacklistFromView(view: KeyIPListView): string[] {
  return view.ip_blacklist ?? []
}
export function modelAllowlistFromView(view: KeyModelAllowlistView): string[] {
  return view.allowed_models ?? []
}

// ── 内部工具 ──────────────────────────────────────────────────────────────────

/**
 * 宽松判定一个条目是否像 IPv4/IPv6 地址或 CIDR。
 * IPv4:四段 0-255,可带 /0-32。IPv6:含「:」且仅 hex/冒号/点,可带 /0-128。
 * 故意宽松(后端权威),只拦掉「明显不是 IP」的输入(如域名、空格、字母串)。
 */
export function isPlausibleIPorCIDR(raw: string): boolean {
  const s = raw.trim()
  if (s === '') return false
  const [addr, mask, ...rest] = s.split('/')
  if (rest.length > 0) return false // 多于一个 '/' 非法
  if (mask !== undefined) {
    // 掩码必须是非负整数。
    if (!/^[0-9]+$/.test(mask)) return false
    const m = Number(mask)
    if (addr.includes(':')) {
      if (m > 128) return false
    } else if (m > 32) {
      return false
    }
  }
  if (addr.includes(':')) {
    // IPv6:只含 hex / ':' / '.'(IPv4 映射),且至少有一个冒号。
    return /^[0-9a-fA-F:.]+$/.test(addr)
  }
  // IPv4:四段十进制 0-255。
  const parts = addr.split('.')
  if (parts.length !== 4) return false
  return parts.every((p) => /^[0-9]+$/.test(p) && Number(p) <= 255)
}

/**
 * 裁掉十进制尾随 0:
 * "25.00000000" → "25";"1.50" → "1.5";整数原样;非十进制原样返回。
 */
export function trimDecimal(raw: string): string {
  const v = raw.trim()
  if (!/^[0-9]+(\.[0-9]+)?$/.test(v)) return v
  if (!v.includes('.')) return v
  const trimmed = v.replace(/0+$/, '').replace(/\.$/, '')
  return trimmed === '' ? '0' : trimmed
}
